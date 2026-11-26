package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
)

func TestDiagnosticsEndpointIncludesInstallReportAndLogs(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "print-agent.log")
	if err := os.WriteFile(logFile, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatalf("write agent log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install-report.json"), []byte(`{"status":"ok","support_code":"TP-1234"}`), 0o600); err != nil {
		t.Fatalf("write install-report: %v", err)
	}

	cfg := config.Default()
	cfg.LogFile = logFile
	cfgPath := filepath.Join(dir, "printers.json")
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, err := config.Load(cfgPath, false)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	srv := New(Deps{
		Config:    store,
		Registry:  printer.NewRegistry(),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp DiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || len(resp.RecentLogs) != 2 || len(resp.InstallReport) == 0 {
		t.Fatalf("unexpected diagnostics response: %+v", resp)
	}
}
