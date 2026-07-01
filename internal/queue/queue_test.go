package queue

import (
	"errors"
	"testing"
	"time"
)

// helper — creates an in-memory queue (no persistence).
func newMemQueue(maxRetries int, dedupTTL time.Duration) *Queue {
	q, err := New(maxRetries, dedupTTL, "", nil)
	if err != nil {
		panic(err)
	}
	return q
}

func TestSubmitPopPrint(t *testing.T) {
	q := newMemQueue(0, 0)
	job, err := q.Submit("p1", "j1", []byte("hello"))
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
	_, _ = q.Submit("p1", "j1", []byte("a"))
	_, err := q.Submit("p1", "j1", []byte("b"))
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestRetryRequeue(t *testing.T) {
	q := newMemQueue(2, 0) // maxRetries=2 → MaxAttempts=3
	job, _ := q.Submit("p1", "j1", []byte("x"))
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
	_, _ = q.Submit("p1", "j1", []byte("x"))
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

func TestCancel(t *testing.T) {
	q := newMemQueue(0, 0)
	_, _ = q.Submit("p1", "j1", []byte("x"))
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
		_, _ = q.Submit("p1", "id-"+string(rune('a'+i)), []byte("x"))
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
