package server

import (
	"bytes"
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
	"github.com/tomapedidos/print-agent/internal/queue"
)

func setupTestServer(t *testing.T) (*httptest.Server, *config.Store, *queue.Queue, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "printers.json")

	cfg := config.Default()
	cfg.Printers[0].Host = "127.0.0.1"
	cfg.Printers[0].Port = 1
	cfg.Printers[0].TimeoutMs = 50
	cfg.Queue.PersistPath = ""

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	store, err := config.Load(cfgPath, false)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reg := printer.NewRegistry()
	for _, p := range store.Get().Printers {
		pr, info, _ := printer.NewFromConfig(ctx, p)
		if pr != nil {
			reg.Add(pr, info)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := queue.New(1, time.Minute, "", log, nil)
	if err != nil {
		cancel()
		t.Fatalf("queue.New: %v", err)
	}

	pool := queue.NewPool(q, reg, store.Get().Queue, log)
	pool.Start(ctx)
	go pool.Run(ctx)

	ts := httptest.NewServer(New(Deps{
		Config:    store,
		Registry:  reg,
		Queue:     q,
		Pool:      pool,
		Log:       log,
		StartedAt: time.Now(),
	}))
	t.Cleanup(func() {
		ts.Close()
		cancel()
		_ = q.Close()
	})
	return ts, store, q, cancel
}

func waitJobPrinted(t *testing.T, q *queue.Queue, jobID string, outPath string) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(outPath)
		if err == nil && len(data) > 0 {
			return data
		}
		if j, ok := q.Get(jobID); ok && j.Status == queue.StatusFailed {
			t.Fatalf("job %s failed unexpectedly: %s", jobID, j.LastError)
		}
		time.Sleep(25 * time.Millisecond)
	}
	j, _ := q.Get(jobID)
	t.Fatalf("timed out waiting for job %s to print (current job=%+v)", jobID, j)
	return nil
}

// TestPutConfigAddsPrinterAndPrintsWithoutRestart verifies that workers spawned
// during PUT /config are NOT bound to the HTTP request context (which cancels
// immediately after PUT /config returns 200 OK).
func TestPutConfigAddsPrinterAndPrintsWithoutRestart(t *testing.T) {
	ts, store, q, _ := setupTestServer(t)

	outPath := filepath.Join(t.TempDir(), "caja-out.bin")
	cfg := store.Get()
	cfg.Printers = append(cfg.Printers, config.Printer{
		ID:           "caja",
		Name:         "Caja USB",
		Type:         "file",
		FilePath:     outPath,
		CodePage:     "cp850",
		CharsPerLine: 42,
		Cut:          "partial",
	})

	rawCfg, _ := json.Marshal(cfg)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/config", bytes.NewReader(rawCfg))
	req.AddCookie(&http.Cookie{Name: "tpd_agent_session", Value: "0000"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /config: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /config status = %d, want 200", resp.StatusCode)
	}

	// Give Go's net/http server a moment to finalize and cancel r.Context().
	time.Sleep(50 * time.Millisecond)

	printBody := `{"job_id":"job-new-printer","printer_id":"caja","header":{"order_number":101,"customer_name":"Ana"},"items":[{"qty":1,"name":"Empanada"}]}`
	pResp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewBufferString(printBody))
	if err != nil {
		t.Fatalf("POST /print: %v", err)
	}
	defer pResp.Body.Close()
	if pResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /print status = %d, want 202", pResp.StatusCode)
	}

	written := waitJobPrinted(t, q, "job-new-printer", outPath)
	if !bytes.Contains(written, []byte("Empanada")) {
		t.Errorf("printed payload missing item name: %q", written)
	}
}

// TestPutConfigUpdatesExistingPrinterInPlace verifies that editing the starter
// "cocina" printer (keeping the same ID) replaces its backend immediately.
func TestPutConfigUpdatesExistingPrinterInPlace(t *testing.T) {
	ts, store, q, _ := setupTestServer(t)

	outPath := filepath.Join(t.TempDir(), "cocina-updated.bin")
	cfg := store.Get()
	cfg.Printers[0] = config.Printer{
		ID:           "cocina",
		Name:         "Cocina Local",
		Type:         "file",
		FilePath:     outPath,
		CodePage:     "cp850",
		CharsPerLine: 42,
		Cut:          "partial",
	}

	rawCfg, _ := json.Marshal(cfg)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/config", bytes.NewReader(rawCfg))
	req.AddCookie(&http.Cookie{Name: "tpd_agent_session", Value: "0000"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /config: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /config status = %d, want 200", resp.StatusCode)
	}

	printBody := `{"job_id":"job-edited-printer","printer_id":"cocina","header":{"order_number":202},"items":[{"qty":2,"name":"Milanesa"}]}`
	pResp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewBufferString(printBody))
	if err != nil {
		t.Fatalf("POST /print: %v", err)
	}
	defer pResp.Body.Close()
	if pResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /print status = %d, want 202", pResp.StatusCode)
	}

	written := waitJobPrinted(t, q, "job-edited-printer", outPath)
	if !bytes.Contains(written, []byte("Milanesa")) {
		t.Errorf("printed payload missing item name: %q", written)
	}
}

// TestCORSPrivateNetworkAccessPreflight verifies Chrome/Edge PNA headers when
// an HTTPS SaaS origin calls http://127.0.0.1:4510.
func TestCORSPrivateNetworkAccessPreflight(t *testing.T) {
	ts, _, _, _ := setupTestServer(t)

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/print", nil)
	req.Header.Set("Origin", "https://app.tomapedidos.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /print: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.tomapedidos.com" {
		t.Errorf("Allow-Origin = %q, want https://app.tomapedidos.com", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("Allow-Private-Network = %q, want true", got)
	}
}

func TestPairEndpointConfiguresTelemetryAndBlocksUntrustedHost(t *testing.T) {
	ts, store, _, _ := setupTestServer(t)

	// 1. Untrusted domain must be blocked with 403.
	badBody := `{"tenant_id":"t1","branch_id":"b1","endpoint_url":"https://evil.example.com/steal"}`
	badResp, err := http.Post(ts.URL+"/pair", "application/json", bytes.NewBufferString(badBody))
	if err != nil {
		t.Fatalf("POST /pair untrusted: %v", err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted /pair status = %d, want 403", badResp.StatusCode)
	}

	// 2. Valid pair with omitted endpoint_url uses DefaultTelemetryEndpoint.
	goodBody := `{"tenant_id":"pizzeria_1","branch_id":"sucursal_sur","token":"tok_abc"}`
	goodResp, err := http.Post(ts.URL+"/pair", "application/json", bytes.NewBufferString(goodBody))
	if err != nil {
		t.Fatalf("POST /pair valid: %v", err)
	}
	_ = goodResp.Body.Close()
	if goodResp.StatusCode != http.StatusOK {
		t.Fatalf("valid /pair status = %d, want 200", goodResp.StatusCode)
	}

	cfg := store.Get()
	if !cfg.Telemetry.Enabled {
		t.Errorf("expected Telemetry.Enabled=true after /pair")
	}
	if cfg.Telemetry.EndpointURL != config.DefaultTelemetryEndpoint {
		t.Errorf("EndpointURL = %q, want %q", cfg.Telemetry.EndpointURL, config.DefaultTelemetryEndpoint)
	}
	if cfg.Tenant.ID != "pizzeria_1" || cfg.Tenant.BranchID != "sucursal_sur" || cfg.Telemetry.Token != "tok_abc" {
		t.Errorf("unexpected paired config: tenant=%+v token=%q", cfg.Tenant, cfg.Telemetry.Token)
	}
}

