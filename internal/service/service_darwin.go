//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type launchdManager struct {
	label     string
	plistPath string
}

func New() Manager {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return &launchdManager{
		label:     Label,
		plistPath: filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
	}
}

func (m *launchdManager) Install(exePath, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(m.plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	logPath := filepath.Join(filepath.Dir(configPath), "agent.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>start</string>
		<string>--config</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, m.label, exePath, configPath, logPath, logPath)
	if err := os.WriteFile(m.plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	return run("launchctl", "load", m.plistPath)
}

func (m *launchdManager) Uninstall() error {
	_ = run("launchctl", "unload", m.plistPath)
	_ = os.Remove(m.plistPath)
	return nil
}

func (m *launchdManager) Start() error  { return run("launchctl", "load", m.plistPath) }
func (m *launchdManager) Stop() error   { return run("launchctl", "unload", m.plistPath) }

func (m *launchdManager) Status() (string, error) {
	cmd := exec.Command("launchctl", "list", m.label)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "stopped", nil
	}
	first := strings.Fields(strings.TrimSpace(string(out)))
	if len(first) == 0 || first[0] == "-" {
		return "stopped", nil
	}
	return "running", nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return nil
}
