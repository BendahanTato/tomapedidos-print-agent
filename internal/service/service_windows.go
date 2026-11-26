//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
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

// Install creates or reconfigures the Windows service via SCM, prepares
// ProgramData directories, seeds/migrates config if needed, and grants
// directory ACLs using locale-independent SIDs.
func (m *scmManager) Install(exePath, configPath string) error {
	absConfig, err := filepath.Abs(configPath)
	if err == nil {
		configPath = absConfig
	}
	configDir, dataDir, logsDir, err := ensureServiceDirsAndConfig(configPath)
	if err != nil {
		return err
	}

	sm, closer, err := connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer closer.Close()

	s, err := sm.OpenService(m.label)
	if err == nil {
		defer s.Close()
		stopAndWait(s, 30*time.Second)
		if err := m.reconfigure(s, exePath, configPath); err != nil {
			return err
		}
		return m.grantDataDirPermissions(configDir, dataDir, logsDir)
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
	return m.grantDataDirPermissions(configDir, dataDir, logsDir)
}

func stopAndWait(s *mgr.Service, timeout time.Duration) {
	if _, err := s.Control(svc.Stop); err != nil {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, qerr := s.Query()
		if qerr != nil || status.State == svc.Stopped {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func ensureServiceDirsAndConfig(configPath string) (configDir, dataDir, logsDir string, err error) {
	configDir = filepath.Dir(configPath)
	dataRoot := filepath.Dir(configDir)
	if !strings.EqualFold(filepath.Base(configDir), "config") {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		dataRoot = filepath.Join(pd, "Tomapedidos")
	}
	dataDir = filepath.Join(dataRoot, "data")
	logsDir = filepath.Join(dataRoot, "logs")

	for _, dir := range []string{configDir, dataDir, logsDir} {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return "", "", "", fmt.Errorf("create directory %q: %w", dir, mkErr)
		}
	}

	jobsPath := filepath.Join(dataDir, "jobs.db")
	// Migrate legacy %APPDATA%\tomapedidos files if present and target does not exist yet.
	if appData := os.Getenv("APPDATA"); appData != "" {
		legacyDir := filepath.Join(appData, "tomapedidos")
		legacyCfg := filepath.Join(legacyDir, "printers.json")
		if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
			if raw, readErr := os.ReadFile(legacyCfg); readErr == nil {
				_ = os.WriteFile(configPath, raw, 0o644)
			}
		}
		if _, statErr := os.Stat(jobsPath); os.IsNotExist(statErr) {
			if matches, _ := filepath.Glob(filepath.Join(legacyDir, "jobs.db*")); len(matches) > 0 {
				for _, src := range matches {
					if raw, readErr := os.ReadFile(src); readErr == nil {
						_ = os.WriteFile(filepath.Join(dataDir, filepath.Base(src)), raw, 0o600)
					}
				}
			}
		}
	}

	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		starter := config.Default()
		starter.Queue.PersistPath = jobsPath
		b, mErr := json.MarshalIndent(starter, "", "  ")
		if mErr != nil {
			return "", "", "", fmt.Errorf("marshal starter config: %w", mErr)
		}
		if wErr := os.WriteFile(configPath, b, 0o644); wErr != nil {
			return "", "", "", fmt.Errorf("write starter config %q: %w", configPath, wErr)
		}
	} else if statErr == nil {
		// Ensure relative persist_path in migrated/existing configs points to ProgramData\Tomapedidos\data\jobs.db.
		if raw, rErr := os.ReadFile(configPath); rErr == nil {
			var parsed config.Config
			if jErr := json.Unmarshal(raw, &parsed); jErr == nil {
				if parsed.Queue.PersistPath == "" || parsed.Queue.PersistPath == "./data/jobs.db" || parsed.Queue.PersistPath == `.\data\jobs.db` {
					parsed.Queue.PersistPath = jobsPath
					if b, mErr := json.MarshalIndent(parsed, "", "  "); mErr == nil {
						_ = os.WriteFile(configPath, b, 0o644)
					}
				}
			}
		}
	}
	return configDir, dataDir, logsDir, nil
}

// grantDataDirPermissions grants full control to the virtual service account
// and modify access to built-in Users via well-known SID *S-1-5-32-545 so it
// works on Spanish ("Usuarios"), English ("Users"), or any Windows locale.
func (m *scmManager) grantDataDirPermissions(dirs ...string) error {
	serviceGrant := m.virtualAccountName() + ":(OI)(CI)F"
	usersSIDGrant := "*S-1-5-32-545:(OI)(CI)M"
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	icaclsPath := filepath.Join(sysRoot, "System32", "icacls.exe")
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		cmd := exec.Command(icaclsPath, dir, "/grant", serviceGrant, usersSIDGrant)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("icacls %q failed: %w (%s)", dir, err, strings.TrimSpace(string(out)))
		}
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
