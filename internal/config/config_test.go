package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "printers.json")
	store, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if store.Path() != path {
		t.Errorf("Path = %q, want %q", store.Path(), path)
	}
	cfg := store.Get()
	if len(cfg.Printers) == 0 {
		t.Errorf("expected default to seed at least one printer")
	}
	if cfg.Port != 4510 {
		t.Errorf("default port = %d, want 4510", cfg.Port)
	}
}

func TestLoadMissingNoCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	if _, err := Load(path, false); err == nil {
		t.Errorf("expected error when file is absent and create=false")
	}
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	cfg := Default()
	cfg.Printers = append(cfg.Printers, cfg.Printers[0])
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected duplicate id error")
	}
}

func TestValidateNetworkRequiresHostPort(t *testing.T) {
	cfg := Default()
	cfg.Printers[0].Type = "network"
	cfg.Printers[0].Host = ""
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected missing host error")
	}
}

func TestValidateUSBRequiresSystemName(t *testing.T) {
	cfg := Default()
	cfg.Printers[0].Type = "usb"
	cfg.Printers[0].SystemName = ""
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected missing system_name error")
	}
}

func TestValidateUnknownType(t *testing.T) {
	cfg := Default()
	cfg.Printers[0].Type = "bluetooth"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected unknown type error")
	}
}

func TestReloadReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	cfg := Default()
	cfg.Printers[0].ID = "alpha"
	if err := writeJSON(path, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	store, err := Load(path, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if store.Get().Printers[0].ID != "alpha" {
		t.Fatalf("initial id = %q, want alpha", store.Get().Printers[0].ID)
	}
	cfg.Printers[0].ID = "beta"
	if err := writeJSON(path, cfg); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if store.Get().Printers[0].ID != "beta" {
		t.Errorf("after reload id = %q, want beta", store.Get().Printers[0].ID)
	}
}

func TestReplacePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	store, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := store.Get()
	cfg.Printers[0].ID = "renamed"
	if err := store.Replace(cfg); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), `"renamed"`) {
		t.Errorf("expected persisted file to contain renamed")
	}
}

func writeJSON(path string, cfg Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
