package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
	"github.com/tomapedidos/print-agent/internal/updater"
)

type stubPrinter struct{ id string }

func (p stubPrinter) ID() string                          { return p.id }
func (p stubPrinter) Open(context.Context) error          { return nil }
func (p stubPrinter) Write(context.Context, []byte) error { return nil }
func (p stubPrinter) Close() error                        { return nil }
func (p stubPrinter) Ping(context.Context) error          { return nil }

func TestSendOncePostsHeartbeatAndTriggersTargetVersion(t *testing.T) {
	var receivedAuth string
	var received HeartbeatPayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode heartbeat: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"target_version":"v0.4.2"}`))
	}))
	defer ts.Close()

	cfgPath := filepath.Join(t.TempDir(), "printers.json")
	store, err := config.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg := store.Get()
	cfg.Tenant = config.Tenant{ID: "tenant_resto", BranchID: "sucursal_centro"}
	cfg.Telemetry = config.Telemetry{
		Enabled:     true,
		EndpointURL: ts.URL,
		Token:       "tok_secret",
		IntervalSec: 30,
	}
	cfg.AutoUpdate.Enabled = true
	if err := store.Replace(cfg); err != nil {
		t.Fatalf("store.Replace: %v", err)
	}

	reg := printer.NewRegistry()
	reg.Add(stubPrinter{id: "cocina"}, printer.Info{
		ID:     "cocina",
		Name:   "Cocina USB",
		Type:   "usb",
		Status: printer.StatusOnline,
	})

	q, err := queue.New(0, time.Minute, "", nil, nil)
	if err != nil {
		t.Fatalf("queue.New: %v", err)
	}
	defer q.Close()
	_, _ = q.Submit("cocina", "job-fail-1", []byte("x"), "")
	popped := q.Pop("cocina")
	q.MarkFailed(popped, "OpenPrinter(POS-58): El sistema no puede encontrar el archivo")

	up := updater.New(updater.Options{
		CurrentVersion: "v0.4.0",
		ConfigFn:       func() config.AutoUpdate { return store.Get().AutoUpdate },
	})

	rep := New(Deps{
		Config:    store,
		Registry:  reg,
		Queue:     q,
		Updater:   up,
		StartedAt: time.Now().Add(-2 * time.Minute),
	})

	if err := rep.SendOnce(context.Background()); err != nil {
		t.Fatalf("SendOnce: %v", err)
	}

	if receivedAuth != "Bearer tok_secret" {
		t.Errorf("Authorization = %q, want Bearer tok_secret", receivedAuth)
	}
	if received.Tenant.ID != "tenant_resto" || received.Tenant.BranchID != "sucursal_centro" {
		t.Errorf("Tenant = %+v, want tenant_resto/sucursal_centro", received.Tenant)
	}
	if len(received.Printers) != 1 || received.Printers[0].Status != "online" {
		t.Errorf("Printers = %+v, want 1 online printer", received.Printers)
	}
	if received.Jobs.Failed != 1 || len(received.RecentFailedJobs) != 1 {
		t.Fatalf("Jobs = %+v, RecentFailedJobs = %+v", received.Jobs, received.RecentFailedJobs)
	}
	if received.RecentFailedJobs[0].LastError == "" {
		t.Errorf("expected RecentFailedJobs[0].LastError to be populated")
	}

	st := rep.GetStatus()
	if st.LastStatusCode != http.StatusOK || st.LastError != "" {
		t.Errorf("Reporter status = %+v, want 200 without error", st)
	}
	if got := up.GetStatus().TargetVersion; got != "v0.4.2" {
		t.Errorf("Updater TargetVersion = %q, want v0.4.2", got)
	}
}
