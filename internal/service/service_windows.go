//go:build windows

package service

import (
	"context"
	"fmt"
	"syscall"
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

// virtualAccountName returns the least-privilege virtual service account SCM
// associates with this service ("NT SERVICE\<label>").
//
// Virtual accounts need NO password: x/sys encodes an empty Password string as
// NULL (see toPtr in mgr), which is exactly the documented contract for
// virtual/computer accounts. Previously the service ran as LocalSystem with the
// executable in a user-writable directory (%LOCALAPPDATA), which let any
// user-session process replace the exe and gain SYSTEM on the next start, and
// is also a standard antivirus heuristic trigger.
func (m *scmManager) virtualAccountName() string {
	return `NT SERVICE\` + m.label
}

// binaryPath builds the SCM command line, quoting both paths so that
// directories containing spaces keep working.
func binaryPath(exePath, configPath string) string {
	return syscall.EscapeArg(exePath) + " start --config " + syscall.EscapeArg(configPath)
}

// recoveryActions tells SCM to restart the agent after crashes. Without them a
// panic leaves the service dead until the machine reboots.
func recoveryActions() []mgr.RecoveryAction {
	return []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
}

// reconfigure updates an already-installed service in place: new binary path,
// least-privilege account and recovery actions.
func (m *scmManager) reconfigure(s *mgr.Service, exePath, configPath string) error {
	// Round-trip through Service.Config() instead of building a partial Config:
	// UpdateConfig passes ServiceType, StartType and ErrorControl verbatim to
	// ChangeServiceConfig, and zero is NOT SERVICE_NO_CHANGE (that is
	// 0xFFFFFFFF) — StartType 0 would silently mean SERVICE_BOOT_START.
	cur, err := s.Config()
	if err != nil {
		return fmt.Errorf("read service %q config: %w", m.label, err)
	}
	cur.BinaryPathName = binaryPath(exePath, configPath)
	cur.ServiceStartName = m.virtualAccountName()
	cur.Password = "" // NULL — both source and target accounts are passwordless
	if err := s.UpdateConfig(cur); err != nil {
		return fmt.Errorf("update service %q config: %w", m.label, err)
	}
	if err := s.SetRecoveryActions(recoveryActions(), 86400); err != nil {
		return fmt.Errorf("set recovery actions for service %q: %w", m.label, err)
	}
	return nil
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
		defer s.Close()
		// Already installed: reconfigure in place. Deleting and recreating
		// would leave a window with no registration and lose local SCM state.
		return m.reconfigure(s, exePath, configPath)
	}

	cfg := mgr.Config{
		DisplayName:      "TomaPedidos Print Agent",
		Description:      "Servicio local de impresion para TomaPedidos",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: m.virtualAccountName(),
		Password:         "", // NULL required for virtual accounts; no password.
	}

	s, err = sm.CreateService(m.label, exePath, cfg, "start", "--config", configPath)
	if err != nil {
		return fmt.Errorf("create service %q: %w", m.label, err)
	}
	defer s.Close()

	if err := s.SetRecoveryActions(recoveryActions(), 86400); err != nil {
		return fmt.Errorf("set recovery actions for service %q: %w", m.label, err)
	}
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

	// Stop if running. Control() returns as soon as the request is delivered, so
	// wait until the service is actually stopped: Delete() on a running service
	// only marks it for deletion and leaves the executable locked until reboot.
	if _, err := s.Control(svc.Stop); err == nil {
		deadline := time.Now().Add(30 * time.Second)
		for {
			status, qerr := s.Query()
			if qerr != nil || status.State == svc.Stopped {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("service %q did not stop within 30s", m.label)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

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
