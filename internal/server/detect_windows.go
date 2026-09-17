//go:build windows

package server

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/tomapedidos/print-agent/internal/printer"
	"golang.org/x/sys/windows"
)

var (
	winspool          = windows.NewLazySystemDLL("winspool.drv")
	procEnumPrintersW = winspool.NewProc("EnumPrintersW")
)

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

func detectSystemPrinters(ctx context.Context) ([]DetectedPrinter, error) {
	const (
		printerEnumLocal       = 0x00000002
		printerEnumConnections = 0x00000004
	)
	flags := uint32(printerEnumLocal | printerEnumConnections)
	var needed, returned uint32

	// First call to determine buffer size needed.
	procEnumPrintersW.Call(
		uintptr(flags),
		0,
		2,
		0,
		0,
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if needed == 0 {
		return nil, nil
	}

	buf := make([]byte, needed)
	ret, _, err := procEnumPrintersW.Call(
		uintptr(flags),
		0,
		2,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(needed),
		uintptr(unsafe.Pointer(&needed)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("EnumPrintersW: %w", err)
	}

	if returned == 0 {
		return nil, nil
	}

	infos := unsafe.Slice((*printerInfo2)(unsafe.Pointer(&buf[0])), returned)
	var printers []DetectedPrinter
	for _, info := range infos {
		name := ""
		if info.pPrinterName != nil {
			name = windows.UTF16PtrToString(info.pPrinterName)
		}
		if name == "" {
			continue
		}
		driver := ""
		if info.pDriverName != nil {
			driver = windows.UTF16PtrToString(info.pDriverName)
		}

		mam := driver
		if mam == "" {
			mam = name
		}
		suggested := printer.DetectType(name + " " + driver)

		printers = append(printers, DetectedPrinter{
			Name:          name,
			MakeAndModel:  driver,
			SuggestedType: suggested,
		})
	}
	return printers, nil
}

