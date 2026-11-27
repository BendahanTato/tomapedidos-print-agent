// Package telemetry pushes periodic outbound HTTPS heartbeats to TomaPedidos
// so operators can monitor remote agents, printer statuses, and failed jobs
// without inbound ports, VPNs, or agents.
//
// If the server responds with {"target_version": "vX.Y.Z"}, the reporter
// notifies the updater to perform a remote upgrade or rollback.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
	"github.com/tomapedidos/print-agent/internal/updater"
	"github.com/tomapedidos/print-agent/internal/version"
)

// Status summarizes the state of outbound remote monitoring for /health, doctor, and the UI.
type Status struct {
	Enabled        bool      `json:"enabled"`
	EndpointURL    string    `json:"endpoint_url,omitempty"`
	LastSentAt     time.Time `json:"last_sent_at,omitempty"`
	LastStatusCode int       `json:"last_status_code,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

// HeartbeatPayload is the JSON body POSTed to Telemetry.EndpointURL.
type HeartbeatPayload struct {
	Hostname         string           `json:"hostname"`
	OS               string           `json:"os"`
	Arch             string           `json:"arch"`
	Version          string           `json:"version"`
	Commit           string           `json:"commit"`
	BuildTime        string           `json:"build_time"`
	UptimeSec        int64            `json:"uptime_sec"`
	SentAt           time.Time        `json:"sent_at"`
	Tenant           config.Tenant    `json:"tenant"`
	Printers         []PrinterState   `json:"printers"`
	Jobs             JobsSummary      `json:"jobs"`
	RecentFailedJobs []FailedJobBrief `json:"recent_failed_jobs,omitempty"`
	Updater          updater.Status   `json:"updater"`
}

// PrinterState describes a single registered printer in the heartbeat.
type PrinterState struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	QueueDepth  int        `json:"queue_depth"`
	LastPrintAt *time.Time `json:"last_print_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

// JobsSummary aggregates recent job counts by state.
type JobsSummary struct {
	Queued   int `json:"queued"`
	Printing int `json:"printing"`
	Failed   int `json:"failed"`
}

// FailedJobBrief surfaces recent print failures so support immediately sees root causes.
type FailedJobBrief struct {
	ID         string    `json:"id"`
	PrinterID  string    `json:"printer_id"`
	LastError  string    `json:"last_error"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// HeartbeatResponse is the optional JSON response returned by the cloud endpoint.
type HeartbeatResponse struct {
	OK            bool   `json:"ok"`
	TargetVersion string `json:"target_version,omitempty"`
}

// Deps holds the runtime dependencies read by the Reporter.
type Deps struct {
	Config     *config.Store
	Registry   *printer.Registry
	Queue      *queue.Queue
	Updater    *updater.Updater
	StartedAt  time.Time
	Log        *slog.Logger
	HTTPClient *http.Client
}

// Reporter periodically pushes HeartbeatPayload over HTTPS.
type Reporter struct {
	deps     Deps
	hostname string

	mu     sync.Mutex
	status Status
}

// New creates a Reporter.
func New(d Deps) *Reporter {
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}
	host, _ := os.Hostname()
	return &Reporter{
		deps:     d,
		hostname: host,
	}
}

// GetStatus returns a thread-safe snapshot of the last heartbeat result.
func (r *Reporter) GetStatus() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	if r.deps.Config != nil {
		tcfg := r.deps.Config.Get().Telemetry
		st.Enabled = tcfg.Enabled
		st.EndpointURL = tcfg.EndpointURL
	}
	return st
}

// Run loops until ctx is cancelled, pushing heartbeats whenever telemetry is enabled.
func (r *Reporter) Run(ctx context.Context) {
	// Initial push shortly after boot so newly started agents appear online quickly.
	initTimer := time.NewTimer(3 * time.Second)
	defer initTimer.Stop()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastSent time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-initTimer.C:
			if r.isEnabled() {
				_ = r.SendOnce(ctx)
				lastSent = time.Now()
			}
		case <-ticker.C:
			if !r.isEnabled() {
				continue
			}
			interval := r.interval()
			if time.Since(lastSent) < interval {
				continue
			}
			_ = r.SendOnce(ctx)
			lastSent = time.Now()
		}
	}
}

// SendOnce builds and sends a single heartbeat immediately. Returns an error
// if telemetry is not configured or the remote server rejects the request.
func (r *Reporter) SendOnce(ctx context.Context) error {
	if r.deps.Config == nil {
		return fmt.Errorf("config store unavailable")
	}
	cfg := r.deps.Config.Get()
	endpoint := strings.TrimSpace(cfg.Telemetry.EndpointURL)
	if endpoint == "" {
		return fmt.Errorf("telemetry.endpoint_url is empty")
	}

	payload := r.buildPayload(cfg)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		r.recordResult(0, err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tomapedidos-print-agent/"+version.Version)
	if tok := strings.TrimSpace(cfg.Telemetry.Token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := r.deps.HTTPClient.Do(req)
	if err != nil {
		r.recordResult(0, err)
		if r.deps.Log != nil {
			r.deps.Log.Warn("telemetry heartbeat failed", slog.String("error", err.Error()))
		}
		return err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
		r.recordResult(resp.StatusCode, err)
		if r.deps.Log != nil {
			r.deps.Log.Warn("telemetry heartbeat rejected", slog.Int("status", resp.StatusCode))
		}
		return err
	}

	r.recordResult(resp.StatusCode, nil)

	var hbResp HeartbeatResponse
	if len(respBytes) > 0 && json.Unmarshal(respBytes, &hbResp) == nil {
		if hbResp.TargetVersion != "" && cfg.AutoUpdate.Enabled && r.deps.Updater != nil {
			r.deps.Updater.TriggerTargetVersion(hbResp.TargetVersion)
		}
	}
	return nil
}

func (r *Reporter) buildPayload(cfg config.Config) HeartbeatPayload {
	var printers []PrinterState
	if r.deps.Registry != nil {
		all := r.deps.Registry.All()
		printers = make([]PrinterState, 0, len(all))
		for _, p := range all {
			ps := PrinterState{
				ID:         p.ID,
				Name:       p.Name,
				Type:       p.Type,
				Status:     string(p.Status),
				QueueDepth: p.QueueDepth,
				LastError:  p.LastError,
			}
			if !p.LastPrintAt.IsZero() {
				t := p.LastPrintAt.UTC()
				ps.LastPrintAt = &t
			}
			printers = append(printers, ps)
		}
	}

	var jobsSummary JobsSummary
	var recentFailed []FailedJobBrief
	if r.deps.Queue != nil {
		recent := r.deps.Queue.List(100, "")
		for _, j := range recent {
			switch j.Status {
			case queue.StatusQueued:
				jobsSummary.Queued++
			case queue.StatusPrinting:
				jobsSummary.Printing++
			case queue.StatusFailed:
				jobsSummary.Failed++
				if len(recentFailed) < 5 {
					recentFailed = append(recentFailed, FailedJobBrief{
						ID:         j.ID,
						PrinterID:  j.PrinterID,
						LastError:  j.LastError,
						FinishedAt: j.FinishedAt.UTC(),
					})
				}
			}
		}
	}

	var upStatus updater.Status
	if r.deps.Updater != nil {
		upStatus = r.deps.Updater.GetStatus()
	}

	return HeartbeatPayload{
		Hostname:         r.hostname,
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		Version:          version.Version,
		Commit:           version.Commit,
		BuildTime:        version.BuildTime,
		UptimeSec:        int64(time.Since(r.deps.StartedAt).Seconds()),
		SentAt:           time.Now().UTC(),
		Tenant:           cfg.Tenant,
		Printers:         printers,
		Jobs:             jobsSummary,
		RecentFailedJobs: recentFailed,
		Updater:          upStatus,
	}
}

func (r *Reporter) isEnabled() bool {
	if r.deps.Config == nil {
		return false
	}
	t := r.deps.Config.Get().Telemetry
	return t.Enabled && strings.TrimSpace(t.EndpointURL) != ""
}

func (r *Reporter) interval() time.Duration {
	sec := 60
	if r.deps.Config != nil && r.deps.Config.Get().Telemetry.IntervalSec > 0 {
		sec = r.deps.Config.Get().Telemetry.IntervalSec
	}
	if sec < 10 {
		sec = 10
	}
	return time.Duration(sec) * time.Second
}

func (r *Reporter) recordResult(code int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.LastSentAt = time.Now().UTC()
	r.status.LastStatusCode = code
	if err != nil {
		r.status.LastError = err.Error()
	} else {
		r.status.LastError = ""
	}
}
