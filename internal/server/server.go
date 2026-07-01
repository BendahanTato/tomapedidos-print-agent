// Package server wires the HTTP/WWSS API the cashier browser (and the
// local web panel) will call. All endpoints bind to 127.0.0.1 only;
// the agent is never reachable from the LAN or the public internet.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
)

// webFS embeds the static panel assets (index.html, js/, css/).
//
//go:embed web
var webFS embed.FS

// Deps is the bundle of objects the HTTP handlers need. It is created
// once at startup and passed to New().
type Deps struct {
	Config    *config.Store
	Registry  *printer.Registry
	Queue     *queue.Queue
	Log       *slog.Logger
	StartedAt time.Time
}

var panelSubFS fs.FS

func init() {
	var err error
	panelSubFS, err = fs.Sub(webFS, "web")
	if err != nil {
		panic("embedded web/ not found: " + err.Error())
	}
}

// New returns a chi router configured with every endpoint the agent
// exposes. The caller is responsible for http.ListenAndServe.
func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(d.Log))

	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}

	// Auth endpoints (always accessible).
	r.Post("/auth/login", authLoginHandler(d))
	r.Post("/auth/logout", authLogoutHandler())

	// Public endpoints (called by the SaaS browser, no PIN required).
	r.Get("/health", healthHandler(d))
	r.Get("/printers", listPrintersHandler(d))
	r.Get("/printers/{id}", getPrinterHandler(d))

	r.Post("/print", printHandler(d))
	r.Post("/print/batch", printBatchHandler(d))
	r.Post("/cash-drawer/kick", cashDrawerHandler(d))

	r.Get("/jobs", listJobsHandler(d))
	r.Get("/jobs/{id}", getJobHandler(d))
	r.Post("/jobs/{id}/reprint", reprintHandler(d))
	r.Delete("/jobs/{id}", cancelJobHandler(d))

	// Protected routes (require PIN via cookie).
	r.Group(func(pr chi.Router) {
		pr.Use(pinAuth(d))

		pr.Get("/config", configGetHandler(d))
		pr.Put("/config", configPutHandler(d))
		pr.Get("/printers/detect", detectPrintersHandler(d))
	})

	// SPA panel — all unmatched routes fall back to index.html so
	// the JS client-side router (pushState) works.
	r.Handle("/*", http.FileServer(http.FS(panelSubFS)))

	return r
}

// requestLogger emits one structured log line per request.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			log.Info("http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("dur", time.Since(start)),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}

// authLoginHandler compares the PIN from the request body against
// the configured panel.pin and sets a session cookie on success.
func authLoginHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PIN string `json:"pin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PIN == "" {
			writeError(w, http.StatusBadRequest, "invalid_payload", "pin is required")
			return
		}
		expected := d.Config.Get().Panel.PIN
		if expected == "" {
			expected = "0000"
		}
		if req.PIN != expected {
			writeError(w, http.StatusUnauthorized, "bad_pin", "")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "tpd_agent_session",
			Value:    req.PIN,
			Path:     "/",
			MaxAge:   8 * 3600,
			HttpOnly: false, // JS needs to know if we're logged in
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func authLogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:   "tpd_agent_session",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// pinAuth is a middleware that checks the session cookie against the
// configured PIN. If no PIN is configured, the middleware allows
// everything.
func pinAuth(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expected := d.Config.Get().Panel.PIN
			if expected == "" {
				expected = "0000"
			}
			cookie, err := r.Cookie("tpd_agent_session")
			if err != nil || cookie.Value != expected {
				writeError(w, http.StatusUnauthorized, "unauthorized", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// configGetHandler returns the full active configuration.
func configGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Config.Get())
	}
}

// configPutHandler replaces the in-memory configuration and persists it.
func configPutHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
			return
		}
		if err := d.Config.Replace(cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}
		d.Log.Info("config replaced via panel")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// detectPrintersHandler scans the OS for installed printers and
// returns the raw list for the panel to display in a dropdown.
func detectPrintersHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		names, err := detectSystemPrinters(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "detect_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"printers": names})
	}
}

// errPrinterNotFound is returned when a request targets a printer that
// is not registered. Surfaces as HTTP 404.
type errPrinterNotFound struct{ ID string }

func (e errPrinterNotFound) Error() string { return fmt.Sprintf("printer %q not found", e.ID) }

// firstNonEmpty returns the first non-empty string among args.
func firstNonEmpty(args ...string) string {
	for _, a := range args {
		if a != "" {
			return a
		}
	}
	return ""
}

// IsNotFound reports whether err is a not-found error from this package.
func IsNotFound(err error) bool {
	return errors.As(err, new(errPrinterNotFound))
}
