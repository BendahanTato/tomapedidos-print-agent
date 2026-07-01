//go:build windows

package printer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// USBPrinter for Windows uses the OS spooler. Because Windows lacks a
// `lp` equivalent that can accept raw bytes via stdin, we write the
// payload to a temporary file and invoke the `print` command. For this
// to work the printer must be installed with a generic/text driver
// (or the vendor's OPOS/EPSON driver that accepts raw port data).
type USBPrinter struct {
	id         string
	systemName string
	timeout    time.Duration
}

// NewUSB returns a USBPrinter configured for the given systemName.
func NewUSB(id, systemName string) *USBPrinter {
	return &USBPrinter{
		id:         id,
		systemName: systemName,
		timeout:    30 * time.Second,
	}
}

// ID returns the printer's logical identifier.
func (p *USBPrinter) ID() string { return p.id }

// SetTimeout adjusts the per-call deadline.
func (p *USBPrinter) SetTimeout(d time.Duration) { p.timeout = d }

// Open checks that the printer exists in the Windows spooler.
func (p *USBPrinter) Open(ctx context.Context) error {
	return p.checkExists(ctx)
}

// Write sends the payload to the printer via a temp file + `print /d:…`.
func (p *USBPrinter) Write(ctx context.Context, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("tpd-agent-%s-%d.bin", p.systemName, time.Now().UnixNano()))
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	defer os.Remove(tmp)

	cmd := exec.CommandContext(ctx, "print", "/d:"+p.systemName, tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("print /d:%s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}

// Close is a no-op for USBPrinter.
func (p *USBPrinter) Close() error { return nil }

// Ping checks whether the printer is registered in the Windows spooler.
func (p *USBPrinter) Ping(ctx context.Context) error {
	return p.checkExists(ctx)
}

func (p *USBPrinter) checkExists(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-Printer -Name '"+p.systemName+"' -ErrorAction Stop).Name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Get-Printer %s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}
