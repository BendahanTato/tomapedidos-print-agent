//go:build !windows
// +build !windows

package printer

import (
	"context"
	"errors"
)

// GDIPrinter is a stub for non-Windows systems.
type GDIPrinter struct {
	id string
}

// NewGDI returns an error on non-Windows platforms.
func NewGDI(id, systemName string) (*GDIPrinter, error) {
	return nil, errors.New("usb-gdi (GDI printing) is only supported on Windows")
}

func (p *GDIPrinter) ID() string {
	return p.id
}

func (p *GDIPrinter) Status() (Status, string) {
	return StatusOffline, "not supported on this OS"
}

func (p *GDIPrinter) Ping(_ context.Context) error {
	return errors.New("usb-gdi is only supported on Windows")
}

func (p *GDIPrinter) Open(_ context.Context) error {
	return errors.New("usb-gdi is only supported on Windows")
}

func (p *GDIPrinter) Close() error {
	return nil
}

func (p *GDIPrinter) Write(_ context.Context, b []byte) error {
	return errors.New("usb-gdi is only supported on Windows")
}
