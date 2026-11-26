// Package reporter sends lightweight, low-overhead status reports from the
// running print agent to a configured TomaPedidos server endpoint.
//
// It is designed to never overload the host machine:
//   - Only runs a single goroutine with a 5-minute heartbeat ticker.
//   - Only reacts to printer.status_changed events (with a 30s cooldown),
//     ignoring individual print job events.
//   - Uses a short HTTP timeout (5s) and drops failed sends without retrying
//     or buffering in memory.
package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/eventbus"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/version"
)

const (
	heartbeatInterval = 5 * time.Minute
	eventCooldown     = 30 * time.Second
	httpTimeout       = 5 * time.Second
)

type payload struct {
	Event     string           `json:"event"`
	Detail    string           `json:"detail,omitempty"`
	Version   string           `json:"version"`
	Platform  string           `json:"platform"`
	UptimeSec int64            `json:"uptime_sec"`
	TenantID  string           `json:"tenant_id"`
	BranchID  string           `json:"branch_id"`
	SentAt    string           `json:"sent_at"`
	Printers  []printerSummary `json:"printers"`
}

type printerSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}

// Run starts the lightweight reporting loop and blocks until ctx is canceled.
func Run(ctx context.Context, cfgStore *config.Store, reg *printer.Registry, bus *eventbus.Bus, startedAt time.Time, log *slog.Logger) {
	client := &http.Client{Timeout: httpTimeout}

	// Wait briefly so initial printer pings complete before the startup report.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	sendReport(ctx, client, cfgStore, reg, startedAt, "startup", "", log)

	var subID string
	var ch <-chan eventbus.Event
	if bus != nil {
		subID, ch = bus.Subscribe(16)
		defer bus.Unsubscribe(subID)
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	lastEventSent := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendReport(ctx, client, cfgStore, reg, startedAt, "heartbeat", "", log)
		case evt, ok := <-ch:
			if !ok {
				ch = nil
				continue
			}
			if evt.Type != "printer.status_changed" {
				continue
			}
			if time.Since(lastEventSent) < eventCooldown {
				continue
			}
			lastEventSent = time.Now()
			sendReport(ctx, client, cfgStore, reg, startedAt, "printer_status", "", log)
		}
	}
}

// SendNow dispatches a single one-off status report to cfg.ReportURL if set.
func SendNow(ctx context.Context, cfgStore *config.Store, reg *printer.Registry, startedAt time.Time, event, detail string, log *slog.Logger) {
	client := &http.Client{Timeout: httpTimeout}
	sendReport(ctx, client, cfgStore, reg, startedAt, event, detail, log)
}

func sendReport(ctx context.Context, client *http.Client, cfgStore *config.Store, reg *printer.Registry, startedAt time.Time, event, detail string, log *slog.Logger) {
	cfg := cfgStore.Get()
	if cfg.ReportURL == "" {
		return
	}

	all := reg.All()
	printers := make([]printerSummary, 0, len(all))
	for _, p := range all {
		printers = append(printers, printerSummary{
			ID:        p.ID,
			Name:      p.Name,
			Type:      p.Type,
			Status:    string(p.Status),
			LastError: p.LastError,
		})
	}

	body, err := json.Marshal(payload{
		Event:     event,
		Detail:    detail,
		Version:   version.Version,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		UptimeSec: int64(time.Since(startedAt).Seconds()),
		TenantID:  cfg.Tenant.ID,
		BranchID:  cfg.Tenant.BranchID,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
		Printers:  printers,
	})
	if err != nil {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.ReportURL, bytes.NewReader(body))
	if err != nil {
		log.Warn("reporter invalid url", slog.String("error", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Debug("reporter post failed", slog.String("event", event), slog.String("error", err.Error()))
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Debug("reporter server non-2xx", slog.String("event", event), slog.Int("status", resp.StatusCode))
	}
}
