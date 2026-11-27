// Package server wires the HTTP/WWSS API the cashier browser (and the
// local web panel) will call. All endpoints bind to 127.0.0.1 only;
// the agent is never reachable from the LAN or the public internet.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/eventbus"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
	"github.com/tomapedidos/print-agent/internal/telemetry"
	"github.com/tomapedidos/print-agent/internal/updater"
)

// webFS embeds the static panel assets (index.html, js/, css/).
//
//go:embed all:web
var webFS embed.FS

// Deps is the bundle of objects the HTTP handlers need.
type Deps struct {
	Config    *config.Store
	Registry  *printer.Registry
	Queue     *queue.Queue
	Pool      *queue.Pool
	Telemetry *telemetry.Reporter
	Updater   *updater.Updater
	EventBus  *eventbus.Bus // nil = events disabled
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

// corsMiddleware allows any origin. The agent binds exclusively to
// 127.0.0.1 (loopback), so there is no exposure to the LAN or internet.
// Any browser tab running on the same machine can call the agent,
// including the tomaPedidos SaaS loaded from vercel.app.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// New returns a chi router configured with every endpoint the agent
// exposes. The caller is responsible for http.ListenAndServe.
func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)
	r.Use(requestLogger(d.Log))

	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}

	// Auth endpoints (always accessible).
	r.Post("/auth/login", authLoginHandler(d))
	r.Post("/auth/logout", authLogoutHandler())

	// Public endpoints (called by the SaaS browser, no PIN required).
	r.Get("/health", healthHandler(d))
	r.Post("/pair", pairHandler(d))
	r.Get("/printers", listPrintersHandler(d))
	r.Get("/printers/{id}", getPrinterHandler(d))
	r.Get("/events", eventsHandler(d))

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
		pr.Post("/telemetry/ping", telemetryPingHandler(d))
		pr.Post("/update/check", updateCheckHandler(d))
		pr.Post("/update/apply", updateApplyHandler(d))
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
		// Reconcile the registry and worker pool using a detached context so
		// workers outlive the HTTP request and edited printers are reopened.
		syncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		printer.SyncFromConfig(syncCtx, d.Registry, d.Config.Get(), d.Log)
		if d.Pool != nil {
			d.Pool.Sync()
		}
		d.Log.Info("config replaced via panel")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// DetectSystemPrinters scans the OS spooler for installed printers.
func DetectSystemPrinters(ctx context.Context) ([]DetectedPrinter, error) {
	return detectSystemPrinters(ctx)
}

// pairHandler lets the TomaPedidos web app link this local PC in 1 click by
// posting tenant_id, branch_id, token, and optional endpoint_url. Because it
// requires no local PIN, endpoint_url is restricted to trusted TomaPedidos
// domains (or localhost in dev/tests) to prevent cross-site exfiltration.
func pairHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TenantID    string `json:"tenant_id"`
			BranchID    string `json:"branch_id"`
			Token       string `json:"token"`
			EndpointURL string `json:"endpoint_url"`
		}
		if !decodeJSON(w, r, d.Log, &req) {
			return
		}
		req.TenantID = strings.TrimSpace(req.TenantID)
		req.BranchID = strings.TrimSpace(req.BranchID)
		req.Token = strings.TrimSpace(req.Token)
		req.EndpointURL = strings.TrimSpace(req.EndpointURL)

		if req.TenantID == "" {
			writeError(w, http.StatusBadRequest, "invalid_payload", "tenant_id is required")
			return
		}
		endpoint := req.EndpointURL
		if endpoint == "" {
			endpoint = config.DefaultTelemetryEndpoint
		}
		if !isTrustedPairEndpoint(endpoint) {
			writeError(w, http.StatusForbidden, "untrusted_endpoint", "endpoint_url host is not an allowed TomaPedidos domain")
			return
		}

		cfg := d.Config.Get()
		cfg.Tenant.ID = req.TenantID
		if req.BranchID != "" {
			cfg.Tenant.BranchID = req.BranchID
		}
		cfg.Telemetry.Enabled = true
		cfg.Telemetry.EndpointURL = endpoint
		if req.Token != "" {
			cfg.Telemetry.Token = req.Token
		}
		if cfg.Telemetry.IntervalSec <= 0 {
			cfg.Telemetry.IntervalSec = 60
		}
		if err := d.Config.Replace(cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}

		if d.Telemetry != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = d.Telemetry.SendOnce(ctx)
			}()
		}

		d.Log.Info("agent paired with TomaPedidos cloud",
			slog.String("tenant_id", cfg.Tenant.ID),
			slog.String("branch_id", cfg.Tenant.BranchID),
			slog.String("endpoint", endpoint),
		)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"tenant_id":    cfg.Tenant.ID,
			"branch_id":    cfg.Tenant.BranchID,
			"endpoint_url": endpoint,
		})
	}
}

func isTrustedPairEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" {
		return true
	}
	if u.Scheme != "https" {
		return false
	}
	return host == "tomapedidos.online" ||
		strings.HasSuffix(host, ".tomapedidos.online") ||
		strings.HasSuffix(host, ".vercel.app")
}

func telemetryPingHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Telemetry == nil {
			writeError(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry reporter not initialized")
			return
		}
		if err := d.Telemetry.SendOnce(r.Context()); err != nil {
			writeError(w, http.StatusBadGateway, "telemetry_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"status": d.Telemetry.GetStatus(),
		})
	}
}

func updateCheckHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Updater == nil {
			writeError(w, http.StatusServiceUnavailable, "updater_unavailable", "updater not initialized")
			return
		}
		latest, err := d.Updater.CheckLatest(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "update_check_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"latest": latest,
			"status": d.Updater.GetStatus(),
		})
	}
}

func updateApplyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Updater == nil {
			writeError(w, http.StatusServiceUnavailable, "updater_unavailable", "updater not initialized")
			return
		}
		var req struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		target := req.Version
		forceExact := target != "" && target != "latest"

		applyCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		updated, err := d.Updater.Apply(applyCtx, target, forceExact)
		if err != nil {
			writeError(w, http.StatusBadGateway, "update_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"updated": updated,
			"status":  d.Updater.GetStatus(),
		})
	}
}

// detectPrintersHandler scans the OS for installed printers and
// returns info including suggested type based on hardware detection.
func detectPrintersHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detected, err := detectSystemPrinters(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "detect_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"printers": detected})
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
