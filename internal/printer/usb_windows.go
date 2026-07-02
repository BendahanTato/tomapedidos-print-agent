//go:build windows

package printer

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winspool         = windows.NewLazySystemDLL("winspool.drv")
	procOpenPrinter  = winspool.NewProc("OpenPrinterW")
	procClosePrinter = winspool.NewProc("ClosePrinter")
	procWritePrinter = winspool.NewProc("WritePrinter")
)

// USBPrinter for Windows uses the native winspool.drv API to write
// raw bytes directly to the printer spooler. This works for both
// thermal/ESC/POS and office/plain-text printers — the rendering
// layer (RenderKitchen vs RenderKitchenPlainText) already produces
// the correct format.
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

// SetType is a no-op on Windows — winspool.drv handles raw bytes
// regardless of printer type.
func (p *USBPrinter) SetType(string) {}

// Open checks that the printer exists in the Windows spooler.
func (p *USBPrinter) Open(ctx context.Context) error {
	return p.checkExists(ctx)
}

// Write sends the payload directly to the printer via winspool.drv.
// The bytes are written as-is — no encoding or transformation.
func (p *USBPrinter) Write(ctx context.Context, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	name, err := windows.UTF16PtrFromString(p.systemName)
	if err != nil {
		return fmt.Errorf("invalid printer name %q: %w", p.systemName, err)
	}

	var handle windows.Handle
	ret, _, callErr := procOpenPrinter.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)
	if ret == 0 {
		return fmt.Errorf("OpenPrinter(%s): %w", p.systemName, callErr)
	}
	defer procClosePrinter.Call(uintptr(handle))

	var written uint32
	ret, _, callErr = procWritePrinter.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)),
		uintptr(unsafe.Pointer(&written)),
	)
	if ret == 0 {
		return fmt.Errorf("WritePrinter(%s): %w", p.systemName, callErr)
	}

	// Wait for spooler to finish processing
	windows.FlushFileBuffers(handle)

	return nil
}

// Close is a no-op — handle is closed per-Write.
func (p *USBPrinter) Close() error { return nil }

// MakeAndModel returns empty on Windows.
func (p *USBPrinter) MakeAndModel(ctx context.Context) string { return "" }

// Ping checks whether the printer is registered in the Windows spooler.
func (p *USBPrinter) Ping(ctx context.Context) error {
	return p.checkExists(ctx)
}

func (p *USBPrinter) checkExists(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	name, err := windows.UTF16PtrFromString(p.systemName)
	if err != nil {
		return fmt.Errorf("invalid printer name: %w", err)
	}

	var handle windows.Handle
	ret, _, callErr := procOpenPrinter.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)
	if ret == 0 {
		return fmt.Errorf("printer %q not found: %w", p.systemName, callErr)
	}
	procClosePrinter.Call(uintptr(handle))
	return nil
}
