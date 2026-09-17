package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPersistSurvivesRestart verifies that a job submitted before
// closing the queue is recovered when a second Queue opens the same
// database on the next process life.
func TestPersistSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "jobs.db")

	// First "process life": submit a job.
	q1, err := New(2, time.Minute, dbPath, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j1, err := q1.Submit("printer-a", "persist-1", []byte("payload-1"), "preview-1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if j1.Status != StatusQueued {
		t.Errorf("Status = %q, want queued", j1.Status)
	}
	_ = q1.Close()

	// Second "process life": open the same database.
	q2, err := New(2, time.Minute, dbPath, nil, nil)
	if err != nil {
		t.Fatalf("New #2: %v", err)
	}
	// Job should have been reloaded.
	j2, ok := q2.Get("persist-1")
	if !ok {
		t.Fatalf("expected job to survive restart")
	}
	if j2.PrinterID != "printer-a" {
		t.Errorf("PrinterID = %q, want printer-a", j2.PrinterID)
	}
	if j2.Bytes != 9 {
		t.Errorf("Bytes = %d, want 9", j2.Bytes)
	}
	if j2.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 (not yet popped)", j2.Attempts)
	}

	// Pop, fail once, then close.
	popped := q2.Pop("printer-a")
	if popped == nil || popped.ID != "persist-1" {
		t.Fatalf("Pop returned wrong job")
	}
	q2.MarkFailed(popped, "temporary error")
	_ = q2.Close()

	// Third life: the failed job should be there (requeued).
	q3, err := New(2, time.Minute, dbPath, nil, nil)
	if err != nil {
		t.Fatalf("New #3: %v", err)
	}
	j3, ok := q3.Get("persist-1")
	if !ok {
		t.Fatalf("expected failed-then-requeued job to survive second restart")
	}
	if j3.Status != StatusQueued {
		t.Errorf("Status = %q, want queued", j3.Status)
	}
	if j3.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", j3.Attempts)
	}
	_ = q3.Close()
}

// TestPersistInMemoryFallback ensures that passing "" for persistPath
// results in the old pure-in-memory behaviour.
func TestPersistInMemoryFallback(t *testing.T) {
	q, err := New(0, 0, "", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = q.Submit("p1", "j1", []byte("x"), "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := q.DepthFor("p1"); got != 1 {
		t.Errorf("DepthFor = %d, want 1", got)
	}
	_ = q.Close()
	// Opening the same (empty) path again would be a new in-memory
	// queue, so no assertion about persistence. This just proves
	// that the empty-string path works without error.
}

func TestPersistDedupAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dedup.db")

	// First life: submit and finish a job.
	q1, _ := New(1, time.Hour, dbPath, nil, nil)
	q1.Submit("p1", "dedup-99", []byte("a"), "")
	popped := q1.Pop("p1")
	q1.MarkPrinted(popped)
	_ = q1.Close()

	// Second life: same job_id should still be dedup'd.
	q2, _ := New(1, time.Hour, dbPath, nil, nil)
	_, err := q2.Submit("p1", "dedup-99", []byte("b"), "")
	if !os.IsTimeout(nil) && err != ErrDuplicate {
		// Actually check properly.
		if err != ErrDuplicate {
			t.Errorf("expected ErrDuplicate, got %v", err)
		}
	}
	_ = q2.Close()
}

func TestVacuumOldJobs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vacuum.db")

	q, err := New(0, time.Hour, dbPath, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// 1. Submit and mark a job as printed.
	_, err = q.Submit("printer-a", "vac-1", []byte("payload"), "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	popped := q.Pop("printer-a")
	q.MarkPrinted(popped)

	// 2. Submit a job that stays queued (should NOT be vacuumed).
	_, err = q.Submit("printer-a", "vac-2", []byte("payload"), "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Forzar la fecha de creación a hace 8 días en la BD para vac-1.
	_, err = q.st.db.Exec(`UPDATE jobs SET created_at_ms = ? WHERE id = 'vac-1'`, time.Now().Add(-8*24*time.Hour).UnixMilli())
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Ejecutar deleteOldJobs con un TTL de 7 días.
	deleted, err := q.st.deleteOldJobs(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("deleteOldJobs: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Verificar que vac-1 se borró, pero vac-2 se mantuvo.
	_, found1 := q.st.loadByID("vac-1")
	if found1 {
		t.Errorf("vac-1 should have been deleted")
	}
	_, found2 := q.st.loadByID("vac-2")
	if !found2 {
		t.Errorf("vac-2 should not have been deleted")
	}
}

