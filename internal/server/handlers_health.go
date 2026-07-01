package server

import (
	"net/http"
	"time"

	"github.com/tomapedidos/print-agent/internal/version"
)

// HealthResponse is the body of GET /health. It tells the cashier browser
// (and the local panel) whether the agent is alive, what version is
// running and the current state of every registered printer.
type HealthResponse struct {
	OK        bool             `json:"ok"`
	Version   string           `json:"version"`
	Commit    string           `json:"commit"`
	BuildTime string           `json:"build_time"`
	UptimeSec int64            `json:"uptime_sec"`
	Tenant    TenantInfo       `json:"tenant"`
	Printers  []PrinterSummary `json:"printers"`
}

// TenantInfo identifies which tenant/branch the agent is configured to
// serve. Helps the operator confirm they pointed the agent at the right
// place after install.
type TenantInfo struct {
	ID       string `json:"id"`
	BranchID string `json:"branch_id"`
}

// PrinterSummary is the per-printer slice of HealthResponse.
type PrinterSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	QueueDepth  int       `json:"queue_depth"`
	LastPrintAt time.Time `json:"last_print_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

func healthHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := d.Config.Get()
		printers := d.Registry.All()
		summaries := make([]PrinterSummary, 0, len(printers))
		for _, p := range printers {
			summaries = append(summaries, PrinterSummary{
				ID:          p.ID,
				Name:        p.Name,
				Type:        p.Type,
				Status:      string(p.Status),
				QueueDepth:  p.QueueDepth,
				LastPrintAt: p.LastPrintAt,
				LastError:   p.LastError,
			})
		}
		resp := HealthResponse{
			OK:        true,
			Version:   version.Version,
			Commit:    version.Commit,
			BuildTime: version.BuildTime,
			UptimeSec: int64(time.Since(d.StartedAt).Seconds()),
			Tenant: TenantInfo{
				ID:       cfg.Tenant.ID,
				BranchID: cfg.Tenant.BranchID,
			},
			Printers: summaries,
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
