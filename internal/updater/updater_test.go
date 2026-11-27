package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomapedidos/print-agent/internal/config"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.4.1", "v0.4.0", 1, true},
		{"0.4.0", "v0.4.0", 0, true},
		{"v0.3.9", "v0.4.0", -1, true},
		{"v0.10.0", "v0.9.9", 1, true},
		{"0.1.0-dev", "v0.4.0", 0, false},
		{"unknown", "v0.4.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := CompareSemver(tc.a, tc.b)
		if ok != tc.ok || got != tc.want {
			t.Errorf("CompareSemver(%q, %q) = (%d, %v), want (%d, %v)", tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestCheckLatestAndApplySuccess(t *testing.T) {
	const repo = "acme/print-agent"
	const asset = "print-agent-windows-amd64.exe"
	newBin := []byte("NEW-BINARY-V0.5.0")
	sums := fmt.Sprintf("%s  %s\n", sha256Hex(newBin), asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/"+repo+"/releases/tag/v0.5.0", http.StatusFound)
	})
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newBin)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "print-agent.exe")
	if err := os.WriteFile(exePath, []byte("OLD-BINARY-V0.4.0"), 0o755); err != nil {
		t.Fatalf("write initial exe: %v", err)
	}

	restarted := false
	up := New(Options{
		CurrentVersion: "v0.4.0",
		ExePath:        exePath,
		OS:             "windows",
		Arch:           "amd64",
		BaseGitHubURL:  ts.URL,
		ConfigFn:       func() config.AutoUpdate { return config.AutoUpdate{Enabled: true, Repo: repo} },
		IsIdle:         func() bool { return true },
		OnRestart:      func() { restarted = true },
		SmokeCheck:     func(context.Context, string) error { return nil },
	})

	updated, err := up.Apply(context.Background(), "latest", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !updated {
		t.Fatalf("expected updated=true")
	}
	if !restarted {
		t.Errorf("expected OnRestart to be invoked")
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read replaced exe: %v", err)
	}
	if string(got) != string(newBin) {
		t.Errorf("exe content = %q, want %q", got, newBin)
	}
	oldGot, err := os.ReadFile(exePath + ".old")
	if err != nil {
		t.Fatalf("expected .old backup to exist before cleanup: %v", err)
	}
	if string(oldGot) != "OLD-BINARY-V0.4.0" {
		t.Errorf(".old content = %q, want OLD-BINARY-V0.4.0", oldGot)
	}

	CleanupOldBinary(exePath)
	if _, err := os.Stat(exePath + ".old"); !os.IsNotExist(err) {
		t.Errorf("expected CleanupOldBinary to remove .old")
	}
}

func TestApplyRejectsSHA256Mismatch(t *testing.T) {
	const repo = "acme/print-agent"
	const asset = "print-agent-windows-amd64.exe"
	badSums := strings.Repeat("a", 64) + "  " + asset + "\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(badSums))
	})
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("TAMPERED-BINARY"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "print-agent.exe")
	_ = os.WriteFile(exePath, []byte("SAFE-ORIGINAL"), 0o755)

	restarted := false
	up := New(Options{
		CurrentVersion: "v0.4.0",
		ExePath:        exePath,
		OS:             "windows",
		Arch:           "amd64",
		BaseGitHubURL:  ts.URL,
		ConfigFn:       func() config.AutoUpdate { return config.AutoUpdate{Enabled: true, Repo: repo} },
		OnRestart:      func() { restarted = true },
		SmokeCheck:     func(context.Context, string) error { return nil },
	})

	updated, err := up.Apply(context.Background(), "v0.5.0", true)
	if err == nil || updated {
		t.Fatalf("expected SHA256 mismatch failure, got updated=%v err=%v", updated, err)
	}
	if restarted {
		t.Errorf("OnRestart must not run on failed verification")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "SAFE-ORIGINAL" {
		t.Errorf("live binary was modified despite hash mismatch: %q", got)
	}
	if _, err := os.Stat(exePath + ".new"); !os.IsNotExist(err) {
		t.Errorf("staging .new file was not cleaned up")
	}
}

func TestApplyRejectsFailedSmokeCheck(t *testing.T) {
	const repo = "acme/print-agent"
	const asset = "print-agent-windows-amd64.exe"
	newBin := []byte("CORRUPT-FOR-AV")
	sums := fmt.Sprintf("%s  %s\n", sha256Hex(newBin), asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+repo+"/releases/download/v0.5.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newBin)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "print-agent.exe")
	_ = os.WriteFile(exePath, []byte("SAFE-ORIGINAL"), 0o755)

	up := New(Options{
		CurrentVersion: "v0.4.0",
		ExePath:        exePath,
		OS:             "windows",
		Arch:           "amd64",
		BaseGitHubURL:  ts.URL,
		ConfigFn:       func() config.AutoUpdate { return config.AutoUpdate{Enabled: true, Repo: repo} },
		SmokeCheck:     func(context.Context, string) error { return errors.New("blocked by antivirus") },
	})

	updated, err := up.Apply(context.Background(), "v0.5.0", true)
	if err == nil || updated {
		t.Fatalf("expected smoke test failure, got updated=%v err=%v", updated, err)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "SAFE-ORIGINAL" {
		t.Errorf("live binary modified after failed smoke check: %q", got)
	}
}

func TestApplyForceExactAllowsRollback(t *testing.T) {
	const repo = "acme/print-agent"
	const asset = "print-agent-windows-amd64.exe"
	rollbackBin := []byte("ROLLBACK-V0.4.0")
	sums := fmt.Sprintf("%s  %s\n", sha256Hex(rollbackBin), asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+repo+"/releases/download/v0.4.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+repo+"/releases/download/v0.4.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rollbackBin)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "print-agent.exe")
	_ = os.WriteFile(exePath, []byte("BUGGY-V0.5.0"), 0o755)

	up := New(Options{
		CurrentVersion: "v0.5.0",
		ExePath:        exePath,
		OS:             "windows",
		Arch:           "amd64",
		BaseGitHubURL:  ts.URL,
		ConfigFn:       func() config.AutoUpdate { return config.AutoUpdate{Enabled: true, Repo: repo} },
		SmokeCheck:     func(context.Context, string) error { return nil },
	})

	// Non-forced Apply must NOT downgrade.
	updated, err := up.Apply(context.Background(), "v0.4.0", false)
	if err != nil || updated {
		t.Fatalf("expected non-forced downgrade to be skipped, got updated=%v err=%v", updated, err)
	}

	// Forced Apply (cloud target_version rollback) MUST downgrade.
	updated, err = up.Apply(context.Background(), "v0.4.0", true)
	if err != nil || !updated {
		t.Fatalf("expected forced rollback to succeed, got updated=%v err=%v", updated, err)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != string(rollbackBin) {
		t.Errorf("exe after rollback = %q, want %q", got, rollbackBin)
	}
}
