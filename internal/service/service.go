// Package service abstracts OS-level daemon management so the CLI
// can offer the same install / uninstall / status workflow across
// macOS (launchd), Linux (systemd user) and Windows (SCM).
package service

// Manager exposes the lifecycle operations for a native OS service.
// Each platform implements its own concrete type with build tags.
type Manager interface {
	// Install registers the daemon so it starts on boot / login.
	// The config path is baked into the service definition so the
	// operator does not need to pass --config every time.
	Install(exePath, configPath string) error

	// Uninstall removes the service registration.
	Uninstall() error

	// Start begins execution of the installed service.
	Start() error

	// Stop halts the running service.
	Stop() error

	// Status returns a one-word description of the current state
	// (e.g. "running", "stopped", "not installed").
	Status() (string, error)
}

// Label is the system-level identifier used by launchd, systemd and SCM.
// It appears in `launchctl list`, `systemctl --user status` and the
// Services MMC snap-in so operators can identify it.
const Label = "com.tomapedidos.print-agent"
