package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tomapedidos/print-agent/internal/reporter"
	"github.com/tomapedidos/print-agent/internal/version"
)

// DiagnosticsResponse is returned by GET /diagnostics so the SaaS or support
// tools can inspect installation status, step-by-step installer reports,
// printer health, and recent log lines without SSH/RDP access.
type DiagnosticsResponse struct {
	OK             bool             `json:"ok"`
	Version        string           `json:"version"`
	Commit         string           `json:"commit"`
	BuildTime      string           `json:"build_time"`
	UptimeSec      int64            `json:"uptime_sec"`
	Platform       string           `json:"platform"`
	Tenant         TenantInfo       `json:"tenant"`
	ConfigPath     string           `json:"config_path"`
	LogsDir        string           `json:"logs_dir"`
	Printers       []PrinterSummary `json:"printers"`
	InstallReport  json.RawMessage  `json:"install_report,omitempty"`
	RecentLogs     []string         `json:"recent_logs,omitempty"`
	InstallLogTail []string         `json:"install_log_tail,omitempty"`
}

func diagnosticsHandler(d Deps) http.HandlerFunc {
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

		logsDir := resolveLogsDir(cfg.LogFile)
		var installReport json.RawMessage
		if raw, err := os.ReadFile(filepath.Join(logsDir, "install-report.json")); err == nil {
			trimmed := strings.TrimSpace(string(raw))
			if json.Valid([]byte(trimmed)) {
				installReport = json.RawMessage(trimmed)
			}
		}

		agentLogPath := cfg.LogFile
		if agentLogPath == "" {
			agentLogPath = filepath.Join(logsDir, "print-agent.log")
		}

		resp := DiagnosticsResponse{
			OK:        true,
			Version:   version.Version,
			Commit:    version.Commit,
			BuildTime: version.BuildTime,
			UptimeSec: int64(time.Since(d.StartedAt).Seconds()),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			Tenant: TenantInfo{
				ID:       cfg.Tenant.ID,
				BranchID: cfg.Tenant.BranchID,
			},
			ConfigPath:     d.Config.Path(),
			LogsDir:        logsDir,
			Printers:       summaries,
			InstallReport:  installReport,
			RecentLogs:     tailFileLines(agentLogPath, 50),
			InstallLogTail: tailFileLines(filepath.Join(logsDir, "install.log"), 50),
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func resolveLogsDir(logFile string) string {
	if logFile != "" {
		return filepath.Dir(logFile)
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		return filepath.Join(programData, "Tomapedidos", "logs")
	}
	if programData := os.Getenv("PROGRAMDATA"); programData != "" {
		return filepath.Join(programData, "Tomapedidos", "logs")
	}
	return filepath.Join(".", "logs")
}

func tailFileLines(path string, maxLines int) []string {
	if path == "" || maxLines <= 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

type setupFeedbackRequest struct {
	Status    string `json:"status"`
	PrinterID string `json:"printer_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func setupFeedbackHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setupFeedbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.Status == "" {
			req.Status = "ok"
		}
		d.Log.Info("setup feedback",
			slog.String("status", req.Status),
			slog.String("printer_id", req.PrinterID),
			slog.String("detail", req.Detail),
		)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			reporter.SendNow(ctx, d.Config, d.Registry, d.StartedAt, "setup_"+req.Status, req.Detail, d.Log)
		}()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
