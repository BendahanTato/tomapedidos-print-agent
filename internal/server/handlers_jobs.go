package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tomapedidos/print-agent/internal/queue"
)

// listJobsHandler returns recent jobs, newest first. Supports ?status,
// ?printer_id and ?limit query parameters.
func listJobsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 50
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		statusFilter := queue.Status(q.Get("status"))
		printerFilter := q.Get("printer_id")
		jobs := d.Queue.List(0, statusFilter)
		out := make([]*jobView, 0, len(jobs))
		for _, j := range jobs {
			if printerFilter != "" && j.PrinterID != printerFilter {
				continue
			}
			out = append(out, projectJob(j))
			if len(out) >= limit {
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
	}
}

func getJobHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		j, ok := d.Queue.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "job_not_found", "job "+id+" not found or already evicted")
			return
		}
		writeJSON(w, http.StatusOK, projectJob(j))
	}
}

func reprintHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		j, ok := d.Queue.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "job_not_found", "job "+id+" not found or already evicted")
			return
		}
		// Resubmit with a fresh id (dedup window blocks the same id).
		_, err := d.Queue.Submit(j.PrinterID, "", j.Payload)
		if err != nil && err.Error() != "queue: duplicate job" {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "printer_id": j.PrinterID})
	}
}

func cancelJobHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		_, ok := d.Queue.Cancel(id)
		if !ok {
			writeError(w, http.StatusNotFound, "job_not_found", "job "+id+" not found or already terminal")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// jobView is the HTTP-friendly projection of queue.Job. Payload bytes
// are not exposed by default; the dedicated /jobs/:id endpoint can be
// extended to include them in M5.
type jobView struct {
	ID          string `json:"id"`
	PrinterID   string `json:"printer_id"`
	Status      string `json:"status"`
	Bytes       int    `json:"bytes"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	LastError   string `json:"last_error,omitempty"`
	CreatedAt   string `json:"created_at"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
}

func projectJob(j *queue.Job) *jobView {
	if j == nil {
		return nil
	}
	v := &jobView{
		ID:          j.ID,
		PrinterID:   j.PrinterID,
		Status:      string(j.Status),
		Bytes:       j.Bytes,
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		LastError:   j.LastError,
	}
	if !j.CreatedAt.IsZero() {
		v.CreatedAt = j.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !j.StartedAt.IsZero() {
		v.StartedAt = j.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !j.FinishedAt.IsZero() {
		v.FinishedAt = j.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return v
}
