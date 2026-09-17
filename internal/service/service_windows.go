//go:build windows

package service

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type scmManager struct {
	label string
}

func New() Manager {
	return &scmManager{label: Label}
}

// Install creates a Windows service via SCM using the Win32 API.
func (m *scmManager) Install(exePath, configPath string) error {
	sm, closer, err := connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer closer.Close()

	s, err := sm.OpenService(m.label)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", m.label)
	}

	cfg := mgr.Config{
		DisplayName: "TomaPedidos Print Agent",
		Description: "Servicio local de impresion para TomaPedidos",
		StartType:   mgr.StartAutomatic,
	}

	s, err = sm.CreateService(m.label, exePath, cfg, "start", "--config", configPath)
	if err != nil {
		return fmt.Errorf("create service %q: %w", m.label, err)
	}
	defer s.Close()
	return nil
}

// Uninstall stops and deletes the service.
func (m *scmManager) Uninstall() error {
	sm, closer, err := connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer closer.Close()

	s, err := sm.OpenService(m.label)
	if err != nil {
		return nil // Not installed
	}
	defer s.Close()

	// Stop if running
	_, _ = s.Control(svc.Stop)

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", m.label, err)
	}
	return nil
}

// Start invokes SCM StartService.
func (m *scmManager) Start() error {
	sm, closer, err := connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer closer.Close()

	s, err := sm.OpenService(m.label)
	if err != nil {
		return fmt.Errorf("open service %q: %w", m.label, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", m.label, err)
	}
	return nil
}

// Stop invokes SCM Stop.
func (m *scmManager) Stop() error {
	sm, closer, err := connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer closer.Close()

	s, err := sm.OpenService(m.label)
	if err != nil {
		return fmt.Errorf("open service %q: %w", m.label, err)
	}
	defer s.Close()

	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service %q: %w", m.label, err)
	}
	return nil
}

// Status queries the service state via SCM.
func (s *scmManager) Status() (string, error) {
	sm, closer, err := connect()
	if err != nil {
		return "not installed", err
	}
	defer closer.Close()
	serv, err := sm.OpenService(s.label)
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
	sm, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	disc := sm.Disconnect
	return sm, (*closerFunc)(&disc), nil
}

type closerFunc func() error

func (c *closerFunc) Close() error {
	if c != nil {
		return (*c)()
	}
	return nil
}

// IsWindowsService reports whether the process is executing as a Windows service.
func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

type agentService struct {
	runFunc func(ctx context.Context) error
}

func (s *agentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.runFunc(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case err := <-errCh:
			changes <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-errCh:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// RunAsService executes the service handler if running under Windows SCM.
func RunAsService(name string, runFunc func(ctx context.Context) error) error {
	return svc.Run(name, &agentService{runFunc: runFunc})
}
