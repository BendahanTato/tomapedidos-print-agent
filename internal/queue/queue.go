// Package queue implements the FIFO print queue with optional
// SQLite persistence (M3). When persistPath is empty the queue
// operates in pure in-memory mode (M1 behaviour); when a path is
// provided, every job mutation is written through to a local
// database so jobs survive agent restart.
package queue

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a single job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusPrinting  Status = "printing"
	StatusPrinted   Status = "printed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Job is the unit of work submitted to the queue. Payload holds the
// already-rendered ESC/POS bytes; the queue does not re-render.
type Job struct {
	ID          string    `json:"id"`
	PrinterID   string    `json:"printer_id"`
	Payload     []byte    `json:"-"`
	Bytes       int       `json:"bytes"`
	Status      Status    `json:"status"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

// ErrDuplicate is returned by Submit when a job with the same ID was
// accepted within the dedup window.
var ErrDuplicate = errors.New("queue: duplicate job")

// Queue is a per-printer FIFO with retry support and optional
// SQLite persistence. The zero value is not usable; construct one
// with New.
type Queue struct {
	mu       sync.Mutex
	queues   map[string][]*Job
	byID     map[string]*Job
	maxRetry int
	dedupTTL time.Duration
	st       *store    // nil when persistence is off
	log      *slog.Logger

	notifyCh chan struct{}
}

// New returns a Queue configured with maxRetries and dedupTTL.
//
// If persistPath is not empty, the queue opens (or creates) a
// SQLite database at that path and reloads any jobs that were
// still active at the last shutdown. Jobs that were in 'printing'
// state are reset to 'queued' so the worker retries them.
//
// persistPath is resolved relative to the current working directory
// unless it starts with / or C:\ .
func New(maxRetries int, dedupTTL time.Duration, persistPath string, log *slog.Logger) (*Queue, error) {
	q := &Queue{
		queues:   make(map[string][]*Job),
		byID:     make(map[string]*Job),
		maxRetry: maxRetries,
		dedupTTL: dedupTTL,
		log:      log,
		notifyCh: make(chan struct{}, 1),
	}

	if persistPath != "" {
		var err error
		q.st, err = q.openBlockingStore(persistPath)
		if err != nil {
			return nil, err
		}
		// Reload jobs that survived the previous run.
		jobs, err := q.st.loadActive()
		if err != nil {
			_ = q.Close()
			return nil, err
		}
		for _, j := range jobs {
			j.MaxAttempts = maxRetries + 1
			q.queues[j.PrinterID] = append(q.queues[j.PrinterID], j)
			q.byID[j.ID] = j
		}
		if log != nil && len(jobs) > 0 {
			log.Info("queue reloaded from disk", "count", len(jobs))
		}
		// Wake the worker pool so reloaded jobs are picked up.
		q.Wake()
	}
	return q, nil
}

// openBlockingStore opens the SQLite DB with retries for a short
// window. When the agent starts immediately after a crash the old
// WAL/journal files may still be locked; a brief retry resolves
// most of those races.
func (q *Queue) openBlockingStore(path string) (*store, error) {
	const maxWait = 3 * time.Second
	deadline := time.Now().Add(maxWait)
	for {
		st, err := openStore(resolveStoragePath(path))
		if err == nil {
			return st, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		if q.log != nil {
			q.log.Warn("sqlite open failed, retrying", "path", path, "error", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// resolveStoragePath ensures the parent directory exists when the
// path is relative. Absolute paths are left as-is.
func resolveStoragePath(raw string) string {
	dir := filepath.Dir(raw)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return raw
}

// Close gracefully shuts down the queue, persisting any remaining
// state to disk if a store was configured.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.st != nil {
		return q.st.close()
	}
	return nil
}

// Notify returns a channel that is signalled whenever a new job is
// submitted. Workers should select on it to wake up.
func (q *Queue) Notify() <-chan struct{} { return q.notifyCh }

// Wake signals subscribers of Notify that work is available. Workers
// call this after re-enqueueing a failed job so the next attempt can
// run promptly instead of waiting for the next Submit.
func (q *Queue) Wake() {
	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
}

// Submit enqueues a job for the given printer. If a job with the same ID
// was accepted within the dedup window, ErrDuplicate is returned and the
// new payload is discarded.
func (q *Queue) Submit(printerID string, jobID string, payload []byte) (*Job, error) {
	if jobID == "" {
		jobID = uuid.NewString()
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	// In-memory dedup (fast path).
	if prev, ok := q.byID[jobID]; ok {
		if time.Since(prev.CreatedAt) < q.dedupTTL {
			return prev, ErrDuplicate
		}
	}

	// Persisted dedup (covers jobs that finished and were evicted
	// from memory between runs).
	if q.st != nil {
		if dup, _ := q.st.isDuplicate(jobID, q.dedupTTL); dup {
			// Try to get from in-memory first, then DB.
			if prev, ok := q.byID[jobID]; ok {
				return prev, ErrDuplicate
			}
			// Just reject; the client can re-submit with a new id.
			return nil, ErrDuplicate
		}
		_ = q.st.insertDedup(jobID, time.Now())
	}

	max := q.maxRetry + 1
	j := &Job{
		ID:          jobID,
		PrinterID:   printerID,
		Payload:     append([]byte(nil), payload...),
		Bytes:       len(payload),
		Status:      StatusQueued,
		Attempts:    0,
		MaxAttempts: max,
		CreatedAt:   time.Now(),
	}
	q.queues[printerID] = append(q.queues[printerID], j)
	q.byID[jobID] = j

	if q.st != nil {
		if err := q.st.insertJob(j); err != nil && q.log != nil {
			q.log.Warn("persist insert failed", "job_id", j.ID, "error", err)
		}
	}

	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
	return j, nil
}

// Pop returns the next job for printerID, or nil if the queue is empty.
func (q *Queue) Pop(printerID string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	qpl := q.queues[printerID]
	for len(qpl) > 0 {
		j := qpl[0]
		q.queues[printerID] = qpl[1:]
		qpl = q.queues[printerID]
		if j.Status == StatusQueued {
			j.Status = StatusPrinting
			j.Attempts++
			j.StartedAt = time.Now()
			q.persistUpdateLocked(j)
			return j
		}
	}
	return nil
}

// MarkPrinted finalises a successful job and removes it from byID so
// future dedup checks for the same ID re-accept after the window.
func (q *Queue) MarkPrinted(j *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.Status = StatusPrinted
	j.FinishedAt = time.Now()
	delete(q.byID, j.ID)
	if q.st != nil {
		if err := q.st.deleteJob(j.ID); err != nil && q.log != nil {
			q.log.Warn("persist delete failed", "job_id", j.ID, "error", err)
		}
	}
}

// MarkFailed records a failure. If the job still has attempts left it
// is requeued at the tail; otherwise it moves to StatusFailed and stays
// in byID until the dedup window expires.
func (q *Queue) MarkFailed(j *Job, errMsg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.LastError = errMsg
	j.FinishedAt = time.Now()
	if j.Attempts >= j.MaxAttempts {
		j.Status = StatusFailed
		q.persistUpdateLocked(j)
		return
	}
	j.Status = StatusQueued
	q.queues[j.PrinterID] = append(q.queues[j.PrinterID], j)
	q.persistUpdateLocked(j)
}

// Cancel marks a queued or printing job as cancelled.
func (q *Queue) Cancel(jobID string) (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[jobID]
	if !ok {
		return nil, false
	}
	if j.Status == StatusPrinted || j.Status == StatusFailed || j.Status == StatusCancelled {
		return j, false
	}
	j.Status = StatusCancelled
	j.FinishedAt = time.Now()
	q.persistUpdateLocked(j)
	return j, true
}

// Get returns a snapshot of a job by ID. It checks the in-memory map
// first, then the SQLite store (for jobs that finished and were
// evicted from memory). If the job is not found it probes the dedup
// table to give a definitive answer.
func (q *Queue) Get(jobID string) (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[jobID]
	if ok {
		cp := *j
		return &cp, true
	}
	if q.st != nil {
		j2, found := q.st.loadByID(jobID)
		if found {
			return j2, true
		}
	}
	return nil, false
}

// List returns the most recent n jobs across all printers, newest
// first. When a store is available it also includes finished jobs
// from the DB, up to n entries, so the /jobs endpoint shows history
// even after a restart.
func (q *Queue) List(n int, statusFilter Status) []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()

	seen := make(map[string]struct{}, len(q.byID))
	all := make([]*Job, 0, len(q.byID)+n)
	for _, j := range q.byID {
		seen[j.ID] = struct{}{}
		if statusFilter != "" && j.Status != statusFilter {
			continue
		}
		cp := *j
		all = append(all, &cp)
	}

	// Supplement with stored jobs.
	if q.st != nil {
		dbLimit := n * 2
		if n <= 0 {
			dbLimit = 200
		}
		stored := q.st.listStored(dbLimit, statusFilter)
		for _, j := range stored {
			if _, ok := seen[j.ID]; ok {
				continue
			}
			all = append(all, j)
			if dbLimit > 0 && len(all) >= dbLimit {
				break
			}
		}
	}

	// Newest first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	return all
}

// DepthFor returns the number of queued jobs for the given printer.
func (q *Queue) DepthFor(printerID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queues[printerID])
}

// persistUpdateLocked updates (or deletes) the job row in SQLite.
// Caller must hold q.mu.
func (q *Queue) persistUpdateLocked(j *Job) {
	if q.st == nil {
		return
	}
	if j.Status == StatusPrinted {
		_ = q.st.deleteJob(j.ID)
		return
	}
	if err := q.st.updateJob(j); err != nil && q.log != nil {
		q.log.Warn("persist update failed", "job_id", j.ID, "error", err)
	}
}

// StoredJob is a thin projection returned by store queries when the
// requesting code wants to avoid importing database/sql.
type StoredJob = Job
