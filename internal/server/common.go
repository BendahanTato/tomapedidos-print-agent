package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON serialises body and writes it with the supplied status code.
// The Content-Type is set to application/json; charset=utf-8.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		// At this point the status is already written. Best we can do is
		// log and let the client see a truncated body.
		_ = err
	}
}

// writeError emits a small JSON error envelope. Use for 4xx/5xx.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": message,
	})
}

// decodeJSON parses the request body into out. On any error it writes
// a 400 and logs the failure. The caller should check r.Context().Err()
// if they need to distinguish.
func decodeJSON(w http.ResponseWriter, r *http.Request, log *slog.Logger, out any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		log.Warn("invalid request body",
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return false
	}
	return true
}
