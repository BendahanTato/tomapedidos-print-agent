package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
)

// helper — creates an in-memory queue (no persistence).
func newMemQueue(maxRetries int, dedupTTL time.Duration) *Queue {
	q, err := New(maxRetries, dedupTTL, "", nil, nil)
	if err != nil {
		panic(err)
	}
	return q
}

func TestSubmitPopPrint(t *testing.T) {
	q := newMemQueue(0, 0)
	job, err := q.Submit("p1", "j1", []byte("hello"), "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if job.ID != "j1" {
		t.Errorf("ID = %q, want j1", job.ID)
	}
	if job.Status != StatusQueued {
		t.Errorf("Status = %q, want queued", job.Status)
	}
	if got := q.DepthFor("p1"); got != 1 {
		t.Errorf("DepthFor = %d, want 1", got)
	}

	got := q.Pop("p1")
	if got == nil {
		t.Fatalf("Pop returned nil")
	}
	if got.Status != StatusPrinting {
		t.Errorf("Status = %q, want printing", got.Status)
	}
	if got.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", got.Attempts)
	}
	q.MarkPrinted(got)
	if q.DepthFor("p1") != 0 {
		t.Errorf("DepthFor after MarkPrinted = %d, want 0", q.DepthFor("p1"))
	}
}

func TestSubmitDedup(t *testing.T) {
	q := newMemQueue(0, time.Minute)
	_, _ = q.Submit("p1", "j1", []byte("a"), "")
	_, err := q.Submit("p1", "j1", []byte("b"), "")
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestRetryRequeue(t *testing.T) {
	q := newMemQueue(2, 0) // maxRetries=2 → MaxAttempts=3
	job, _ := q.Submit("p1", "j1", []byte("x"), "")
	for i := 1; i <= 3; i++ {
		got := q.Pop("p1")
		if got == nil {
			t.Fatalf("Pop #%d returned nil", i)
		}
		if got.Attempts != i {
			t.Errorf("attempt #%d: Attempts = %d, want %d", i, got.Attempts, i)
		}
		if i < 3 {
			q.MarkFailed(got, "boom")
		} else {
			q.MarkPrinted(got)
		}
	}
	_, ok := q.Get(job.ID)
	if ok {
		t.Errorf("expected job to be evicted from dedup map after success")
	}
}

func TestFailedJobStaysForInspection(t *testing.T) {
	q := newMemQueue(1, time.Minute) // maxRetries=1 → MaxAttempts=2
	_, _ = q.Submit("p1", "j1", []byte("x"), "")
	got := q.Pop("p1")
	q.MarkFailed(got, "boom")
	got = q.Pop("p1")
	if got == nil {
		t.Fatalf("second Pop returned nil")
	}
	q.MarkFailed(got, "boom again")
	j, ok := q.Get("j1")
	if !ok {
		t.Fatalf("expected failed job to remain in dedup map")
	}
	if j.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", j.Status)
	}
}

type workerTestPrinter struct {
	id       string
	writeErr error
}

func (p *workerTestPrinter) ID() string                          { return p.id }
func (p *workerTestPrinter) Open(context.Context) error          { return nil }
func (p *workerTestPrinter) Write(context.Context, []byte) error { return p.writeErr }
func (p *workerTestPrinter) Close() error                        { return nil }
func (p *workerTestPrinter) Ping(context.Context) error          { return nil }

func TestWorkerDoesNotRetryAmbiguousWriteFailures(t *testing.T) {
	tests := []struct {
		name          string
		writeErr      error
		wantStatus    Status
		wantQueueSize int
	}{
		{
			name:          "ambiguous write is terminal",
			writeErr:      fmt.Errorf("socket write: %w", printer.ErrAmbiguousWrite),
			wantStatus:    StatusFailed,
			wantQueueSize: 0,
		},
		{
			name:          "ordinary write error is retried",
			writeErr:      errors.New("temporary printer error"),
			wantStatus:    StatusQueued,
			wantQueueSize: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := New(2, time.Minute, filepath.Join(t.TempDir(), "jobs.db"), nil, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer q.Close()

			const printerID = "printer-a"
			reg := printer.NewRegistry()
			reg.Add(&workerTestPrinter{id: printerID, writeErr: tt.writeErr}, printer.Info{ID: printerID})
			job, err := q.Submit(printerID, "job-1", []byte("ticket"), "")
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			job = q.Pop(printerID)
			if job == nil {
				t.Fatal("Pop returned nil")
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			worker := NewWorker(printerID, q, reg, config.Queue{RetryBackoffMs: []int{10}}, logger)
			worker.processOne(context.Background(), job)

			got, ok := q.Get(job.ID)
			if !ok {
				t.Fatal("job missing after worker processed the write error")
			}
			if got.Status != tt.wantStatus {
				t.Errorf("job status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Attempts != 1 {
				t.Errorf("job attempts = %d, want 1", got.Attempts)
			}
			if got := q.DepthFor(printerID); got != tt.wantQueueSize {
				t.Errorf("queued depth = %d, want %d", got, tt.wantQueueSize)
			}
			if errors.Is(tt.writeErr, printer.ErrAmbiguousWrite) {
				if got.MaxAttempts != got.Attempts {
					t.Errorf("ambiguous job MaxAttempts = %d, want %d", got.MaxAttempts, got.Attempts)
				}
			} else {
				if got.MaxAttempts != 3 {
					t.Errorf("ordinary failure MaxAttempts = %d, want 3", got.MaxAttempts)
				}
				if got.NextRetryAt.IsZero() {
					t.Error("ordinary failure did not schedule a retry")
				}
			}
		})
	}
}

func TestCancel(t *testing.T) {
	q := newMemQueue(0, 0)
	_, _ = q.Submit("p1", "j1", []byte("x"), "")
	j, ok := q.Cancel("j1")
	if !ok {
		t.Fatalf("Cancel: not found")
	}
	if j.Status != StatusCancelled {
		t.Errorf("Status = %q, want cancelled", j.Status)
	}
	_, ok = q.Cancel("j1")
	if ok {
		t.Errorf("second Cancel should report no change")
	}
}

func TestListOrder(t *testing.T) {
	q := newMemQueue(0, 0)
	for i := 0; i < 3; i++ {
		_, _ = q.Submit("p1", "id-"+string(rune('a'+i)), []byte("x"), "")
		time.Sleep(2 * time.Millisecond)
	}
	all := q.List(0, "")
	if len(all) != 3 {
		t.Fatalf("List = %d, want 3", len(all))
	}
	if all[0].ID != "id-c" {
		t.Errorf("newest first: got %q, want id-c", all[0].ID)
	}
}
