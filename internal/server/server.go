// Package server wires the HTTP/WS API the cashier browser (and the
// future local panel) will call. All endpoints bind to 127.0.0.1 only;
// the agent is never reachable from the LAN or the public internet.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
	"github.com/tomapedidos/print-agent/internal/version"
)

// Deps is the bundle of objects the HTTP handlers need. It is created
// once at startup and passed to New().
type Deps struct {
	Config    *config.Store
	Registry  *printer.Registry
	Queue     *queue.Queue
	Log       *slog.Logger
	StartedAt time.Time
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

	// Static panel placeholder. M5 ships the full SPA.
	r.Get("/", rootHandler())

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

// rootHandler returns a minimal HTML page so the operator can browse to
// the agent and confirm the service is running. M5 will replace this
// with the full panel SPA.
func rootHandler() http.HandlerFunc {
	body := `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <title>TomaPedidos Print Agent</title>
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; max-width: 640px; margin: 4rem auto; padding: 0 1rem; color: #1f2937; }
    h1 { font-size: 1.5rem; margin: 0 0 0.5rem; }
    code { background: #f3f4f6; padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.9em; }
    .meta { color: #6b7280; font-size: 0.875rem; }
  </style>
</head>
<body>
  <h1>TomaPedidos Print Agent</h1>
  <p class="meta">` + version.String() + `</p>
  <p>El panel completo llega en M5. Por ahora, los endpoints disponibles son:</p>
  <ul>
    <li><code>GET /health</code></li>
    <li><code>GET /printers</code></li>
    <li><code>POST /print</code></li>
    <li><code>POST /print/batch</code></li>
    <li><code>POST /cash-drawer/kick</code></li>
    <li><code>GET /jobs</code></li>
  </ul>
</body>
</html>`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
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
