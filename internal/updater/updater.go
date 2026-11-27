// Package updater implements pull-based self-updates from GitHub Releases.
//
// Design (KISS):
//  1. Resolves the latest tag via the 302 redirect on
//     https://github.com/<repo>/releases/latest (no GitHub API rate limits).
//  2. Downloads print-agent-<os>-<arch>[.exe] and SHA256SUMS.
//  3. Verifies SHA256 and runs `<new-binary> version` before touching the live exe.
//  4. Renames current exe -> .old (allowed on Windows while running), moves .new -> exe,
//     and triggers a clean shutdown with non-zero exit so SCM/systemd/launchd restarts
//     into the new binary.
package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
)

// ErrRestartRequired signals main to exit with a non-zero code so the OS
// service supervisor (Windows SCM / systemd / launchd) restarts the agent.
var ErrRestartRequired = errors.New("restart required to activate updated binary")

// Status is the runtime state exposed in /health, /update/status, doctor, and telemetry.
type Status struct {
	Enabled          bool      `json:"enabled"`
	CurrentVersion   string    `json:"current_version"`
	AvailableVersion string    `json:"available_version,omitempty"`
	TargetVersion    string    `json:"target_version,omitempty"`
	Updating         bool      `json:"updating"`
	PendingRestart   bool      `json:"pending_restart,omitempty"`
	LastCheckAt      time.Time `json:"last_check_at,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
}

// Options configures a new Updater.
type Options struct {
	CurrentVersion string
	ExePath        string
	OS             string
	Arch           string
	ConfigFn       func() config.AutoUpdate
	IsIdle         func() bool
	OnRestart      func()
	Log            *slog.Logger
	BaseGitHubURL  string // default "https://github.com"; overridden in tests
	HTTPClient     *http.Client
	SmokeCheck     func(ctx context.Context, binPath string) error
}

// Updater manages periodic version checks and binary replacement.
type Updater struct {
	opts      Options
	triggerCh chan string

	mu     sync.Mutex
	status Status
	busy   bool
}

// New creates an Updater and cleans up any leftover .old/.new files from a previous update.
func New(opts Options) *Updater {
	if opts.OS == "" {
		opts.OS = runtime.GOOS
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.BaseGitHubURL == "" {
		opts.BaseGitHubURL = "https://github.com"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if opts.SmokeCheck == nil {
		opts.SmokeCheck = defaultSmokeCheck
	}
	if opts.ExePath != "" {
		CleanupOldBinary(opts.ExePath)
	}

	enabled := false
	if opts.ConfigFn != nil {
		enabled = opts.ConfigFn().Enabled
	}
	return &Updater{
		opts:      opts,
		triggerCh: make(chan string, 1),
		status: Status{
			Enabled:        enabled,
			CurrentVersion: opts.CurrentVersion,
		},
	}
}

// CleanupOldBinary removes stale .old and .new sidecars left by a prior self-update.
func CleanupOldBinary(exePath string) {
	_ = os.Remove(exePath + ".old")
	_ = os.Remove(exePath + ".new")
}

// BinaryAssetName returns the release asset filename for goos/goarch.
func BinaryAssetName(goos, goarch string) string {
	name := fmt.Sprintf("print-agent-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// IsDevVersion reports whether v is an untagged local/dev build.
func IsDevVersion(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || v == "dev" || v == "unknown" || strings.HasSuffix(v, "-dev")
}

// GetStatus returns a thread-safe snapshot of the updater state.
func (u *Updater) GetStatus() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.status
	if u.opts.ConfigFn != nil {
		st.Enabled = u.opts.ConfigFn().Enabled
	}
	return st
}

// TriggerTargetVersion schedules an asynchronous update (or rollback) to tag.
// Safe to call from the telemetry heartbeat goroutine.
func (u *Updater) TriggerTargetVersion(tag string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return
	}
	if NormalizeTag(tag) == NormalizeTag(u.opts.CurrentVersion) {
		return
	}
	u.mu.Lock()
	u.status.TargetVersion = tag
	u.mu.Unlock()

	select {
	case u.triggerCh <- tag:
	default:
	}
}

// Run executes the background auto-update loop until ctx is cancelled.
func (u *Updater) Run(ctx context.Context) {
	// Initial delayed check 45s after startup so boot is unaffected.
	initTimer := time.NewTimer(45 * time.Second)
	defer initTimer.Stop()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastPeriodicCheck time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case target := <-u.triggerCh:
			cfg := u.currentConfig()
			if !cfg.Enabled {
				continue
			}
			if _, err := u.Apply(ctx, target, true); err != nil && u.opts.Log != nil {
				u.opts.Log.Warn("remote target update failed", slog.String("target", target), slog.String("error", err.Error()))
			}
		case <-initTimer.C:
			lastPeriodicCheck = u.maybePeriodicUpdate(ctx, lastPeriodicCheck, true)
		case <-ticker.C:
			lastPeriodicCheck = u.maybePeriodicUpdate(ctx, lastPeriodicCheck, false)
		}
	}
}

func (u *Updater) maybePeriodicUpdate(ctx context.Context, lastCheck time.Time, forceDue bool) time.Time {
	cfg := u.currentConfig()
	if !cfg.Enabled || IsDevVersion(u.opts.CurrentVersion) {
		return lastCheck
	}
	intervalMin := cfg.CheckIntervalMin
	if intervalMin <= 0 {
		return lastCheck
	}
	if !forceDue && time.Since(lastCheck) < time.Duration(intervalMin)*time.Minute {
		return lastCheck
	}
	now := time.Now()
	if _, err := u.Apply(ctx, "latest", false); err != nil && u.opts.Log != nil {
		u.opts.Log.Warn("periodic auto-update check failed", slog.String("error", err.Error()))
	}
	return now
}

// CheckLatest resolves the latest release tag on GitHub without applying it.
func (u *Updater) CheckLatest(ctx context.Context) (string, error) {
	repo := u.repo()
	noRedirect := *u.opts.HTTPClient
	noRedirect.Timeout = 15 * time.Second
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	latestURL := strings.TrimRight(u.opts.BaseGitHubURL, "/") + "/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tomapedidos-print-agent/"+u.opts.CurrentVersion)

	resp, err := noRedirect.Do(req)
	if err != nil {
		u.recordCheckError(err)
		return "", fmt.Errorf("query latest release: %w", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	const marker = "/releases/tag/"
	idx := strings.Index(loc, marker)
	if idx < 0 {
		err := fmt.Errorf("unexpected response %d resolving latest release", resp.StatusCode)
		u.recordCheckError(err)
		return "", err
	}
	tag := strings.Trim(loc[idx+len(marker):], "/")
	if q := strings.IndexAny(tag, "?#"); q >= 0 {
		tag = tag[:q]
	}
	if tag == "" {
		err := errors.New("empty release tag in redirect")
		u.recordCheckError(err)
		return "", err
	}

	u.mu.Lock()
	u.status.LastCheckAt = time.Now()
	u.status.LastError = ""
	if cmp, ok := CompareSemver(tag, u.opts.CurrentVersion); ok && cmp > 0 {
		u.status.AvailableVersion = tag
	} else if NormalizeTag(tag) == NormalizeTag(u.opts.CurrentVersion) {
		u.status.AvailableVersion = ""
	}
	u.mu.Unlock()
	return tag, nil
}

// Apply downloads, verifies SHA256, smoke-tests, and swaps the agent binary.
// When forceExact is false, only versions strictly newer than CurrentVersion
// are installed. When forceExact is true (explicit tag or cloud rollback), any
// tag different from CurrentVersion is installed.
func (u *Updater) Apply(ctx context.Context, requestedTag string, forceExact bool) (bool, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return false, errors.New("update already in progress")
	}
	u.busy = true
	u.status.Updating = true
	u.mu.Unlock()

	defer func() {
		u.mu.Lock()
		u.busy = false
		u.status.Updating = false
		u.mu.Unlock()
	}()

	tag := strings.TrimSpace(requestedTag)
	if tag == "" || tag == "latest" {
		resolved, err := u.CheckLatest(ctx)
		if err != nil {
			return false, err
		}
		tag = resolved
	} else if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	if NormalizeTag(tag) == NormalizeTag(u.opts.CurrentVersion) {
		return false, nil
	}
	if !forceExact {
		cmp, ok := CompareSemver(tag, u.opts.CurrentVersion)
		if !ok || cmp <= 0 {
			return false, nil
		}
	}

	if u.opts.ExePath == "" {
		err := errors.New("executable path unknown")
		u.recordCheckError(err)
		return false, err
	}

	assetName := BinaryAssetName(u.opts.OS, u.opts.Arch)
	dlBase := fmt.Sprintf("%s/%s/releases/download/%s", strings.TrimRight(u.opts.BaseGitHubURL, "/"), u.repo(), tag)
	binURL := dlBase + "/" + assetName
	sumsURL := dlBase + "/SHA256SUMS"

	expectedHash, err := u.fetchExpectedSHA256(ctx, sumsURL, assetName)
	if err != nil {
		u.recordCheckError(err)
		return false, err
	}

	newPath := u.opts.ExePath + ".new"
	_ = os.Remove(newPath)
	actualHash, err := u.downloadAndHash(ctx, binURL, newPath)
	if err != nil {
		_ = os.Remove(newPath)
		u.recordCheckError(err)
		return false, err
	}
	if !strings.EqualFold(actualHash, expectedHash) {
		_ = os.Remove(newPath)
		err := fmt.Errorf("SHA256 mismatch for %s: expected %s, got %s", assetName, expectedHash, actualHash)
		u.recordCheckError(err)
		return false, err
	}

	smokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = u.opts.SmokeCheck(smokeCtx, newPath)
	cancel()
	if err != nil {
		_ = os.Remove(newPath)
		err = fmt.Errorf("new binary failed smoke test: %w", err)
		u.recordCheckError(err)
		return false, err
	}

	// Wait up to 30s for active print jobs to finish before swapping & restarting.
	if u.opts.IsIdle != nil {
		waitDeadline := time.Now().Add(30 * time.Second)
		for !u.opts.IsIdle() && time.Now().Before(waitDeadline) {
			select {
			case <-ctx.Done():
				_ = os.Remove(newPath)
				return false, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}

	oldPath := u.opts.ExePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(u.opts.ExePath, oldPath); err != nil {
		_ = os.Remove(newPath)
		err = fmt.Errorf("backup running executable: %w", err)
		u.recordCheckError(err)
		return false, err
	}
	if err := os.Rename(newPath, u.opts.ExePath); err != nil {
		_ = os.Rename(oldPath, u.opts.ExePath)
		_ = os.Remove(newPath)
		err = fmt.Errorf("activate new executable: %w", err)
		u.recordCheckError(err)
		return false, err
	}

	u.mu.Lock()
	u.status.AvailableVersion = ""
	u.status.LastError = ""
	u.status.PendingRestart = true
	u.mu.Unlock()

	if u.opts.Log != nil {
		u.opts.Log.Info("binary updated; requesting service restart",
			slog.String("from", u.opts.CurrentVersion),
			slog.String("to", tag),
		)
	}
	if u.opts.OnRestart != nil {
		u.opts.OnRestart()
	}
	return true, nil
}

func (u *Updater) fetchExpectedSHA256(ctx context.Context, sumsURL, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tomapedidos-print-agent/"+u.opts.CurrentVersion)
	resp, err := u.opts.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download SHA256SUMS: HTTP %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 64*1024))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		hash := fields[0]
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName && len(hash) == 64 {
			return hash, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read SHA256SUMS: %w", err)
	}
	return "", fmt.Errorf("SHA256SUMS missing entry for %s", assetName)
}

func (u *Updater) downloadAndHash(ctx context.Context, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tomapedidos-print-agent/"+u.opts.CurrentVersion)
	resp, err := u.opts.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("create staging binary %s: %w", destPath, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write staging binary: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("sync staging binary: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (u *Updater) currentConfig() config.AutoUpdate {
	if u.opts.ConfigFn != nil {
		return u.opts.ConfigFn()
	}
	return config.Default().AutoUpdate
}

func (u *Updater) repo() string {
	r := strings.TrimSpace(u.currentConfig().Repo)
	if r == "" {
		return config.DefaultUpdateRepo
	}
	return r
}

func (u *Updater) recordCheckError(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status.LastCheckAt = time.Now()
	if err != nil {
		u.status.LastError = err.Error()
	}
}

func defaultSmokeCheck(ctx context.Context, binPath string) error {
	abs, err := filepath.Abs(binPath)
	if err == nil {
		binPath = abs
	}
	cmd := exec.CommandContext(ctx, binPath, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NormalizeTag strips a leading "v" and surrounding whitespace.
func NormalizeTag(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// CompareSemver compares two MAJOR.MINOR.PATCH version strings (optional leading 'v').
// Returns (1, true) if a > b, (-1, true) if a < b, (0, true) if equal, or (0, false) if invalid.
func CompareSemver(a, b string) (int, bool) {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1, true
		}
		if pa[i] < pb[i] {
			return -1, true
		}
	}
	return 0, true
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = NormalizeTag(v)
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		// Pre-release/dev suffixes are not valid release targets for semver upgrade.
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
