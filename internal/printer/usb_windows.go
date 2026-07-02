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
	winspool             = windows.NewLazySystemDLL("winspool.drv")
	procOpenPrinter      = winspool.NewProc("OpenPrinterW")
	procClosePrinter     = winspool.NewProc("ClosePrinter")
	procWritePrinter     = winspool.NewProc("WritePrinter")
	procGetPrinterW      = winspool.NewProc("GetPrinterW")
	procStartDocPrinter  = winspool.NewProc("StartDocPrinterW")
	procStartPagePrinter = winspool.NewProc("StartPagePrinter")
	procEndPagePrinter   = winspool.NewProc("EndPagePrinter")
	procEndDocPrinter    = winspool.NewProc("EndDocPrinter")
)

type docInfo1 struct {
	docName    *uint16
	outputFile *uint16
	dataType   *uint16
}

type printerInfo2 struct {
	pServerName         *uint16
	pPrinterName        *uint16
	pShareName          *uint16
	pPortName           *uint16
	pDriverName         *uint16
	pComment            *uint16
	pLocation           *uint16
	pDevMode            uintptr
	pSepFile            *uint16
	pPrintProcessor     *uint16
	pDatatype           *uint16
	pParameters         *uint16
	pSecurityDescriptor uintptr
	Attributes          uint32
	Priority            uint32
	DefaultPriority     uint32
	StartTime           uint32
	UntilTime           uint32
	Status              uint32
	cJobs               uint32
	AveragePPM          uint32
}

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
	if len(payload) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	errc := make(chan error, 1)
	go func() {
		errc <- p.doWrite(payload)
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("printer %q write timeout: %w", p.systemName, ctx.Err())
	case err := <-errc:
		return err
	}
}

func (p *USBPrinter) doWrite(payload []byte) error {
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

	docNamePtr, _ := windows.UTF16PtrFromString("Print Agent Job")
	dataTypePtr, _ := windows.UTF16PtrFromString("RAW")
	di := docInfo1{
		docName:    docNamePtr,
		outputFile: nil,
		dataType:   dataTypePtr,
	}

	ret, _, callErr = procStartDocPrinter.Call(
		uintptr(handle),
		1,
		uintptr(unsafe.Pointer(&di)),
	)
	if ret == 0 {
		return fmt.Errorf("StartDocPrinter(%s): %w", p.systemName, callErr)
	}
	defer procEndDocPrinter.Call(uintptr(handle))

	ret, _, callErr = procStartPagePrinter.Call(uintptr(handle))
	if ret == 0 {
		return fmt.Errorf("StartPagePrinter(%s): %w", p.systemName, callErr)
	}
	defer procEndPagePrinter.Call(uintptr(handle))

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

	return nil
}

// Close is a no-op — handle is closed per-Write.
func (p *USBPrinter) Close() error { return nil }

// MakeAndModel queries the Windows spooler for the printer's driver name
// using GetPrinterW (PRINTER_INFO_2). The driver name contains the make
// and model (e.g., "EPSON L3250 Series", "Brother HL-L2360D series").
func (p *USBPrinter) MakeAndModel(ctx context.Context) string {
	name, err := windows.UTF16PtrFromString(p.systemName)
	if err != nil {
		return ""
	}

	var handle windows.Handle
	ret, _, _ := procOpenPrinter.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)
	if ret == 0 {
		return ""
	}
	defer procClosePrinter.Call(uintptr(handle))

	// First call: get required buffer size.
	var needed uint32
	procGetPrinterW.Call(
		uintptr(handle),
		2, // PRINTER_INFO_2 level
		0,
		0,
		uintptr(unsafe.Pointer(&needed)),
	)

	if needed == 0 {
		return ""
	}

	// Second call: get the actual data.
	buf := make([]byte, needed)
	ret, _, _ = procGetPrinterW.Call(
		uintptr(handle),
		2, // PRINTER_INFO_2 level
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(needed),
		uintptr(unsafe.Pointer(&needed)),
	)
	if ret == 0 {
		return ""
	}

	if len(buf) < int(unsafe.Sizeof(printerInfo2{})) {
		return ""
	}
	pi2 := (*printerInfo2)(unsafe.Pointer(&buf[0]))
	if pi2.pDriverName == nil {
		return ""
	}
	return windows.UTF16PtrToString(pi2.pDriverName)
}

// Ping checks whether the printer is registered in the Windows spooler.
func (p *USBPrinter) Ping(ctx context.Context) error {
	return p.checkExists(ctx)
}

func (p *USBPrinter) checkExists(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	errc := make(chan error, 1)
	go func() {
		errc <- p.doCheckExists()
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("printer %q ping timeout: %w", p.systemName, ctx.Err())
	case err := <-errc:
		return err
	}
}

func (p *USBPrinter) doCheckExists() error {
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
