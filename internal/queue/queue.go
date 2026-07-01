// Package queue implements the in-memory FIFO queue used by the print
// agent in M1. A persistent backend (SQLite) is planned for M3; the
// Queue and Job types below are designed to be swappable without changes
// in the HTTP layer.
package queue

import (
	"errors"
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

// Queue is a per-printer FIFO with retry support. The zero value is not
// usable; construct one with New.
type Queue struct {
	mu       sync.Mutex
	queues   map[string][]*Job
	byID     map[string]*Job
	maxRetry int
	dedupTTL time.Duration

	notifyCh chan struct{}
}

// New returns a Queue configured with maxRetries and dedupTTL. The notify
// channel is buffered to 1; workers should select on it to wake up.
func New(maxRetries int, dedupTTL time.Duration) *Queue {
	return &Queue{
		queues:   make(map[string][]*Job),
		byID:     make(map[string]*Job),
		maxRetry: maxRetries,
		dedupTTL: dedupTTL,
		notifyCh: make(chan struct{}, 1),
	}
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
// new payload is discarded. The job's CreatedAt and MaxAttempts are
// populated automatically.
func (q *Queue) Submit(printerID string, jobID string, payload []byte) (*Job, error) {
	if jobID == "" {
		jobID = uuid.NewString()
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if prev, ok := q.byID[jobID]; ok {
		if time.Since(prev.CreatedAt) < q.dedupTTL {
			return prev, ErrDuplicate
		}
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

	select {
	case q.notifyCh <- struct{}{}:
	default:
	}
	return j, nil
}

// Pop returns the next job for printerID, or nil if the queue is empty.
// If a popped job is in a terminal state it is skipped.
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
}

// MarkFailed records a failure. If the job still has attempts left it
// is requeued at the tail; otherwise it moves to StatusFailed and stays
// in byID until the dedup window expires (so a quick re-submit of the
// same job is still caught).
func (q *Queue) MarkFailed(j *Job, errMsg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.LastError = errMsg
	j.FinishedAt = time.Now()
	if j.Attempts >= j.MaxAttempts {
		j.Status = StatusFailed
		return
	}
	j.Status = StatusQueued
	q.queues[j.PrinterID] = append(q.queues[j.PrinterID], j)
}

// Cancel marks a queued or printing job as cancelled. If the job is
// already in a terminal state it is left alone.
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
	return j, true
}

// Get returns a snapshot of a job by ID.
func (q *Queue) Get(jobID string) (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.byID[jobID]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// List returns the most recent n jobs across all printers, newest first.
func (q *Queue) List(n int, statusFilter Status) []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	all := make([]*Job, 0, len(q.byID))
	for _, j := range q.byID {
		if statusFilter != "" && j.Status != statusFilter {
			continue
		}
		cp := *j
		all = append(all, &cp)
	}
	// Newest first
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
