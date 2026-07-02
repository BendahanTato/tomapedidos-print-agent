//go:build windows

package printer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// USBPrinter for Windows uses the OS spooler.
//   - "usb" (thermal): raw binary via `print /d:`
//   - "usb-office": plain text via PowerShell Out-Printer
type USBPrinter struct {
	id          string
	systemName  string
	printerType string // "usb" or "usb-office"
	timeout     time.Duration
}

// NewUSB returns a USBPrinter configured for the given systemName.
func NewUSB(id, systemName string) *USBPrinter {
	return &USBPrinter{
		id:         id,
		systemName: systemName,
		timeout:    30 * time.Second,
	}
}

// SetType sets the rendering type ("usb" or "usb-office").
func (p *USBPrinter) SetType(t string) { p.printerType = t }

// ID returns the printer's logical identifier.
func (p *USBPrinter) ID() string { return p.id }

// SetTimeout adjusts the per-call deadline.
func (p *USBPrinter) SetTimeout(d time.Duration) { p.timeout = d }

// Open checks that the printer exists in the Windows spooler.
func (p *USBPrinter) Open(ctx context.Context) error {
	return p.checkExists(ctx)
}

// Write sends the payload to the printer.
//   - Thermal (usb): raw binary via `print /d:` — sends bytes directly
//     to the spooler without interpretation.
//   - Office (usb-office): plain text via PowerShell Out-Printer —
//     the printer driver handles formatting.
func (p *USBPrinter) Write(ctx context.Context, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("tpd-agent-%s-%d.bin", p.systemName, time.Now().UnixNano()))
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	defer os.Remove(tmp)

	if p.printerType == "usb-office" {
		return p.writeOffice(ctx, tmp)
	}
	return p.writeRaw(ctx, tmp)
}

// writeRaw sends the file via the legacy `print /d:` command.
// Best for thermal/ESC/POS printers that expect raw binary.
func (p *USBPrinter) writeRaw(ctx context.Context, filePath string) error {
	cmd := exec.CommandContext(ctx, "print", "/d:"+p.systemName, filePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("print /d:%s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}

// writeOffice sends the file via PowerShell Out-Printer.
// Best for office/laser printers that expect plain text from the driver.
func (p *USBPrinter) writeOffice(ctx context.Context, filePath string) error {
	ps := fmt.Sprintf(
		"$content = [System.IO.File]::ReadAllText('%s', [System.Text.Encoding]::UTF8); $content | Out-Printer -Name '%s'",
		filePath, p.systemName,
	)
	cmd := exec.CommandContext(ctx, "powershell", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Out-Printer %s: %w%s", p.systemName, err, formatStderr(out))
	}
	return nil
}

// Close is a no-op for USBPrinter.
func (p *USBPrinter) Close() error { return nil }

// MakeAndModel returns empty on Windows (no CUPS equivalent).
func (p *USBPrinter) MakeAndModel(ctx context.Context) string { return "" }

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

func formatStderr(out []byte) string {
	if len(out) == 0 { return "" }
	return ": " + string(bytes.TrimSpace(out))
}
