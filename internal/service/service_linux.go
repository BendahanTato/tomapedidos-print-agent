//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type systemdUserManager struct {
	label       string
	servicePath string
}

func New() Manager {
	// systemd user units go under $HOME/.config/systemd/user/
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return &systemdUserManager{
		label:       Label,
		servicePath: filepath.Join(home, ".config", "systemd", "user", Label+".service"),
	}
}

func (m *systemdUserManager) Install(exePath, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(m.servicePath), 0o755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	unit := fmt.Sprintf(`[Unit]
Description=TomaPedidos Print Agent
After=network.target

[Service]
Type=simple
ExecStart=%s start --config %s
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, exePath, configPath)
	if err := os.WriteFile(m.servicePath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return run("systemctl", "--user", "enable", m.label)
}

func (m *systemdUserManager) Uninstall() error {
	_ = run("systemctl", "--user", "stop", m.label)
	_ = run("systemctl", "--user", "disable", m.label)
	_ = os.Remove(m.servicePath)
	_ = run("systemctl", "--user", "daemon-reload")
	return nil
}

func (m *systemdUserManager) Start() error {
	return run("systemctl", "--user", "start", m.label)
}

func (m *systemdUserManager) Stop() error {
	return run("systemctl", "--user", "stop", m.label)
}

func (m *systemdUserManager) Status() (string, error) {
	cmd := exec.Command("systemctl", "--user", "is-active", m.label)
	out, _ := cmd.CombinedOutput()
	s := trim(string(out))
	if s == "active" {
		return "running", nil
	}
	return "stopped", nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return nil
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
