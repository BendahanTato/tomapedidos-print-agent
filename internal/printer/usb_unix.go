//go:build !windows

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// USBPrinter sends commands to a printer managed by the OS spooler
// (CUPS on macOS / Linux). It does NOT attempt to talk to the USB
// port directly; the printer must be installed system-wide.
type USBPrinter struct {
	id         string
	systemName string
	timeout    time.Duration
}

// NewUSB returns a USBPrinter configured for the given systemName.
//
// The systemName MUST match the output of `lpstat -p` (Unix) or the
// `Name` field from `Get-Printer` (Windows). CUPS will be invoked
// with the `-o raw` flag so ESC/POS bytes are forwarded without any
// preprocessing.
func NewUSB(id, systemName string) *USBPrinter {
	return &USBPrinter{
		id:         id,
		systemName: systemName,
		timeout:    30 * time.Second,
	}
}

// ID returns the printer's logical identifier.
func (p *USBPrinter) ID() string { return p.id }

// SetTimeout adjusts the per-call deadline. Intended for tests.
func (p *USBPrinter) SetTimeout(d time.Duration) { p.timeout = d }

// Open checks that the printer exists in the CUPS spooler. The
// underlying `lp` command also does this implicitly on Write, but
// Open runs early so the agent can mark the printer as offline at
// boot instead of failing the first print.
func (p *USBPrinter) Open(ctx context.Context) error {
	return p.lpstat(ctx)
}

// Write sends payload to the OS spooler.
//
// The CUPS command is:
//
//	lp -d <system_name> -o raw -
//
// The `-o raw` flag is essential for thermal receipt printers: without
// it CUPS may try to interpret the bytes as PostScript or PCL and
// produce a blank page.
func (p *USBPrinter) Write(ctx context.Context, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lp",
		"-d", p.systemName,
		"-o", "raw",
		"-",
	)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lp -d %s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}

// Close is a no-op for USBPrinter. The spooler session is per-Write.
func (p *USBPrinter) Close() error { return nil }

// Ping checks whether the print queue is reachable by asking CUPS.
// It does not query the hardware status of the printer itself.
func (p *USBPrinter) Ping(ctx context.Context) error {
	return p.lpstat(ctx)
}

func (p *USBPrinter) lpstat(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lpstat", "-p", p.systemName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lpstat -p %s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}

func formatStderr(out []byte) string {
	if len(out) == 0 {
		return ""
	}
	return ": " + string(bytes.TrimSpace(out))
}
