//go:build windows
// +build windows

package printer

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32               = syscall.NewLazyDLL("gdi32.dll")
	procCreateDCW       = gdi32.NewProc("CreateDCW")
	procDeleteDC        = gdi32.NewProc("DeleteDC")
	procStartDocW       = gdi32.NewProc("StartDocW")
	procEndDoc          = gdi32.NewProc("EndDoc")
	procStartPage       = gdi32.NewProc("StartPage")
	procEndPage         = gdi32.NewProc("EndPage")
	procCreateFontW     = gdi32.NewProc("CreateFontIndirectW")
	procSelectObject    = gdi32.NewProc("SelectObject")
	procDeleteObject    = gdi32.NewProc("DeleteObject")
	procTextOutW        = gdi32.NewProc("TextOutW")
	procGetDeviceCaps   = gdi32.NewProc("GetDeviceCaps")
)

const (
	LOGPIXELSY     = 90
	PHYSICALWIDTH  = 110
	PHYSICALHEIGHT = 111
	FW_NORMAL      = 400
	DEFAULT_CHARSET = 1
	OUT_DEFAULT_PRECIS = 0
	CLIP_DEFAULT_PRECIS = 0
	DEFAULT_QUALITY = 0
	DEFAULT_PITCH = 0
)

type DOCINFOW struct {
	CbSize       uint32
	LpszDocName  *uint16
	LpszOutput   *uint16
	LpszDatatype *uint16
	FwType       uint32
}

type LOGFONTW struct {
	LfHeight         int32
	LfWidth          int32
	LfEscapement     int32
	LfOrientation    int32
	LfWeight         int32
	LfItalic         byte
	LfUnderline      byte
	LfStrikeOut      byte
	LfCharSet        byte
	LfOutPrecision   byte
	LfClipPrecision  byte
	LfQuality        byte
	LfPitchAndFamily byte
	LfFaceName       [32]uint16
}

// GDIPrinter uses the Windows GDI (Graphics Device Interface) to draw
// text directly onto the printer's page context. This is required for
// printers that do not support RAW text printing, such as virtual PDF
// printers or cheap host-based (GDI) inkjet printers.
type GDIPrinter struct {
	id         string
	systemName string
	mu         sync.Mutex
}

func NewGDI(id, systemName string) (*GDIPrinter, error) {
	return &GDIPrinter{
		id:         id,
		systemName: systemName,
	}, nil
}

func (p *GDIPrinter) ID() string {
	return p.id
}

func (p *GDIPrinter) Status() (Status, string) {
	return StatusOnline, ""
}

func (p *GDIPrinter) Ping(_ context.Context) error {
	return nil
}

func (p *GDIPrinter) Open(_ context.Context) error {
	return nil
}

func (p *GDIPrinter) Close() error {
	return nil
}

func (p *GDIPrinter) Write(_ context.Context, b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	driver, _ := windows.UTF16PtrFromString("WINSPOOL")
	device, _ := windows.UTF16PtrFromString(p.systemName)

	hdc, _, _ := procCreateDCW.Call(
		uintptr(unsafe.Pointer(driver)),
		uintptr(unsafe.Pointer(device)),
		0,
		0,
	)
	if hdc == 0 {
		return fmt.Errorf("CreateDCW failed for printer %s", p.systemName)
	}
	defer procDeleteDC.Call(hdc)

	docName, _ := windows.UTF16PtrFromString("Print Agent Job")
	di := DOCINFOW{
		CbSize:      uint32(unsafe.Sizeof(DOCINFOW{})),
		LpszDocName: docName,
	}

	ret, _, _ := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(&di)))
	if int32(ret) <= 0 {
		return fmt.Errorf("StartDocW failed")
	}
	defer procEndDoc.Call(hdc)

	ret, _, _ = procStartPage.Call(hdc)
	if int32(ret) <= 0 {
		return fmt.Errorf("StartPage failed")
	}

	dpiY, _, _ := procGetDeviceCaps.Call(hdc, LOGPIXELSY)
	pageHeight, _, _ := procGetDeviceCaps.Call(hdc, PHYSICALHEIGHT)

	// Use a 10pt monospace font.
	pointSize := 10
	lfHeight := -int32((pointSize * int(dpiY)) / 72)
	lineSpacing := int(-lfHeight) + 4 // Add a little padding

	lf := LOGFONTW{
		LfHeight: lfHeight,
		LfWeight: FW_NORMAL,
		LfCharSet: DEFAULT_CHARSET,
		LfOutPrecision: OUT_DEFAULT_PRECIS,
		LfClipPrecision: CLIP_DEFAULT_PRECIS,
		LfQuality: DEFAULT_QUALITY,
		LfPitchAndFamily: DEFAULT_PITCH,
	}
	faceName, _ := windows.UTF16FromString("Courier New")
	copy(lf.LfFaceName[:], faceName)

	hFont, _, _ := procCreateFontW.Call(uintptr(unsafe.Pointer(&lf)))
	if hFont != 0 {
		oldFont, _, _ := procSelectObject.Call(hdc, hFont)
		defer procSelectObject.Call(hdc, oldFont)
		defer procDeleteObject.Call(hFont)
	}

	lines := bytes.Split(b, []byte("\n"))
	y := 0

	for _, line := range lines {
		// Strip carriage returns just in case.
		line = bytes.TrimRight(line, "\r")
		
		if y+lineSpacing > int(pageHeight) {
			procEndPage.Call(hdc)
			procStartPage.Call(hdc)
			y = 0
		}

		if len(line) > 0 {
			u16, err := windows.UTF16FromString(string(line))
			if err == nil && len(u16) > 0 {
				procTextOutW.Call(
					hdc,
					0, // x
					uintptr(y),
					uintptr(unsafe.Pointer(&u16[0])),
					uintptr(len(u16)-1), // length without null terminator
				)
			}
		}
		y += lineSpacing
	}

	procEndPage.Call(hdc)
	return nil
}
