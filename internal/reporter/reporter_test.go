package reporter

import (
	"context"
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

func TestSendReportPostsPayloadWhenConfigured(t *testing.T) {
	var got payload
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "printers.json")
	cfg := config.Default()
	cfg.ReportURL = srv.URL
	cfg.Tenant = config.Tenant{ID: "pizzeria_don_juan", BranchID: "sucursal_1"}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, err := config.Load(cfgPath, false)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	reg := printer.NewRegistry()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sendReport(context.Background(), srv.Client(), store, reg, time.Now().Add(-10*time.Second), "startup", "ok", log)

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if got.Event != "startup" || got.TenantID != "pizzeria_don_juan" || got.BranchID != "sucursal_1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}
