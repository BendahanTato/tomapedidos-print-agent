package queue

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// store is the SQLite-backed persistence layer for the print queue.
type store struct {
	db     *sql.DB
	closed bool
}

// openStore opens (or creates) the SQLite database at path and runs
// a minimal migration to ensure the two tables exist.
func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) migrate() error {
	const ddl = `
	CREATE TABLE IF NOT EXISTS jobs (
		id            TEXT PRIMARY KEY,
		printer_id    TEXT    NOT NULL,
		payload       BLOB    NOT NULL,
		preview       TEXT    DEFAULT '',
		bytes         INTEGER NOT NULL DEFAULT 0,
		status        TEXT    NOT NULL DEFAULT 'queued',
		attempts      INTEGER NOT NULL DEFAULT 0,
		max_attempts  INTEGER NOT NULL DEFAULT 1,
		last_error    TEXT    DEFAULT '',
		created_at_ms INTEGER NOT NULL,
		started_at_ms INTEGER NOT NULL DEFAULT 0,
		finished_at_ms INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS dedup (
		job_id        TEXT PRIMARY KEY,
		created_at_ms INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_status  ON jobs(status);
	CREATE INDEX IF NOT EXISTS idx_jobs_printer ON jobs(printer_id, status);
	`
	_, err := s.db.Exec(ddl)
	if err != nil {
		return err
	}
	// Migration: add preview column if missing (for existing databases).
	s.db.Exec(`ALTER TABLE jobs ADD COLUMN preview TEXT DEFAULT ''`)
	return nil
}

func (s *store) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// insertJob writes a new job row.
func (s *store) insertJob(j *Job) error {
	const q = `
	INSERT INTO jobs (id, printer_id, payload, preview, bytes, status, attempts,
		max_attempts, last_error, created_at_ms, started_at_ms, finished_at_ms)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(q,
		j.ID, j.PrinterID, j.Payload, j.Preview, j.Bytes, string(j.Status),
		j.Attempts, j.MaxAttempts, j.LastError,
		millis(j.CreatedAt), millis(j.StartedAt), millis(j.FinishedAt),
	)
	return err
}

// updateJob overwrites mutable columns for an existing job.
func (s *store) updateJob(j *Job) error {
	const q = `
	UPDATE jobs SET status=?, attempts=?, max_attempts=?, last_error=?,
		started_at_ms=?, finished_at_ms=?
	WHERE id=?
	`
	_, err := s.db.Exec(q,
		string(j.Status), j.Attempts, j.MaxAttempts, j.LastError,
		millis(j.StartedAt), millis(j.FinishedAt),
		j.ID,
	)
	return err
}

// deleteJob removes a finished job from the store.
func (s *store) deleteJob(id string) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

// loadActive returns all jobs that are NOT in a terminal state.
// Jobs left in 'printing' are reset to 'queued'.
func (s *store) loadActive() ([]*Job, error) {
	rows, err := s.db.Query(
		`SELECT id, printer_id, payload, preview, bytes, status, attempts, max_attempts,
			last_error, created_at_ms, started_at_ms, finished_at_ms
		 FROM jobs
		 WHERE status IN ('queued','printing')
		 ORDER BY created_at_ms`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j := &Job{}
		var status string
		var ca, sa, fa int64
		if err := rows.Scan(
			&j.ID, &j.PrinterID, &j.Payload, &j.Preview, &j.Bytes, &status,
			&j.Attempts, &j.MaxAttempts, &j.LastError,
			&ca, &sa, &fa,
		); err != nil {
			return nil, err
		}
		j.Status = Status(status)
		j.CreatedAt = time.UnixMilli(ca)
		j.StartedAt = time.UnixMilli(sa)
		j.FinishedAt = time.UnixMilli(fa)
		if j.Status == StatusPrinting {
			j.Status = StatusQueued
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// loadByID returns a job from the store by ID, including terminal
// jobs. Used by Get() when the in-memory map does not have it.
func (s *store) loadByID(id string) (*Job, bool) {
	j := &Job{}
	var status string
	var ca, sa, fa int64
	err := s.db.QueryRow(
		`SELECT id, printer_id, payload, preview, bytes, status, attempts, max_attempts,
			last_error, created_at_ms, started_at_ms, finished_at_ms
		 FROM jobs WHERE id=?`, id,
	).Scan(
		&j.ID, &j.PrinterID, &j.Payload, &j.Preview, &j.Bytes, &status,
		&j.Attempts, &j.MaxAttempts, &j.LastError,
		&ca, &sa, &fa,
	)
	if err != nil {
		return nil, false
	}
	j.Status = Status(status)
	j.CreatedAt = time.UnixMilli(ca)
	j.StartedAt = time.UnixMilli(sa)
	j.FinishedAt = time.UnixMilli(fa)
	return j, true
}

// listStored returns recent stored jobs matching statusFilter,
// newest first. Used by List() to supplement in-memory results.
func (s *store) listStored(limit int, statusFilter Status) []*Job {
	var rows *sql.Rows
	var err error
	if statusFilter == "" {
		rows, err = s.db.Query(
			`SELECT id, printer_id, payload, preview, bytes, status, attempts, max_attempts,
				last_error, created_at_ms, started_at_ms, finished_at_ms
			 FROM jobs ORDER BY created_at_ms DESC LIMIT ?`, limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, printer_id, payload, preview, bytes, status, attempts, max_attempts,
				last_error, created_at_ms, started_at_ms, finished_at_ms
			 FROM jobs WHERE status=? ORDER BY created_at_ms DESC LIMIT ?`,
			string(statusFilter), limit,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j := &Job{}
		var st string
		var ca, sa, fa int64
		if err := rows.Scan(
			&j.ID, &j.PrinterID, &j.Payload, &j.Preview, &j.Bytes, &st,
			&j.Attempts, &j.MaxAttempts, &j.LastError,
			&ca, &sa, &fa,
		); err != nil {
			continue
		}
		j.Status = Status(st)
		j.CreatedAt = time.UnixMilli(ca)
		j.StartedAt = time.UnixMilli(sa)
		j.FinishedAt = time.UnixMilli(fa)
		out = append(out, j)
	}
	return out
}

// insertDedup records an external job_id so the dedup window is
// persisted across restarts.
func (s *store) insertDedup(jobID string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO dedup (job_id, created_at_ms) VALUES (?, ?)`,
		jobID, millis(now),
	)
	return err
}

// isDuplicate returns true if the supplied jobID was seen within the
// configured dedup window (in milliseconds). Expired rows are cleaned
// as a side effect.
func (s *store) isDuplicate(jobID string, window time.Duration) (bool, error) {
	cutoff := time.Now().Add(-window)
	if _, err := s.db.Exec(
		`DELETE FROM dedup WHERE created_at_ms < ?`, millis(cutoff),
	); err != nil {
		return false, err
	}
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM dedup WHERE job_id=?`, jobID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
