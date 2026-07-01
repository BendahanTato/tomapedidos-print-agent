//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type scmManager struct {
	label string
}

func New() Manager {
	return &scmManager{label: Label}
}

// Install creates a Windows service via SCM. The binary is registered
// with the start subcommand and auto-start set to demand (the user
// explicitly starts it after install, or sets auto-start later).
func (m *scmManager) Install(exePath, configPath string) error {
	// sc.exe (built into Windows) is the canonical way to create a
	// service; we use it because golang.org/x/sys/windows/svc/mgr
	// requires admin privileges for CreateService.
	args := fmt.Sprintf(
		`create %s binPath="%s start --config %s" start=demand DisplayName="TomaPedidos Print Agent"`,
		m.label, exePath, configPath,
	)
	cmd := exec.Command("sc", strings.Fields(args)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc create: %w\n%s", err, out)
	}
	return nil
}

// Uninstall stops and deletes the service.
func (m *scmManager) Uninstall() error {
	_ = m.Stop()
	_ = run("sc", "delete", m.label)
	return nil
}

// Start invokes sc start.
func (m *scmManager) Start() error {
	return run("sc", "start", m.label)
}

// Stop invokes sc stop.
func (m *scmManager) Stop() error {
	return run("sc", "stop", m.label)
}

// Status queries the service state via SCM.
func (s *scmManager) Status() (string, error) {
	mgr, closer, err := connect()
	if err != nil {
		return "not installed", err
	}
	defer closer.Close()
	serv, err := mgr.OpenService(m.label)
	if err != nil {
		return "not installed", nil
	}
	defer serv.Close()
	status, err := serv.Query()
	if err != nil {
		return "unknown", err
	}
	switch status.State {
	case svc.Running:
		return "running", nil
	case svc.Stopped:
		return "stopped", nil
	default:
		return "unknown", nil
	}
}

func connect() (*mgr.Mgr, *closerFunc, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	return m, (*closerFunc)(&m.Disconnect), nil
}

type closerFunc func() error

func (c *closerFunc) Close() error {
	if c != nil {
		return (*c)()
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return nil
}
