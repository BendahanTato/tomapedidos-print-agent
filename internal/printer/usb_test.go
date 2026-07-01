package printer

import (
	"context"
	"testing"
	"time"
)

func TestUSBPrinterOpenFailsOnMissingPrinter(t *testing.T) {
	up := NewUSB("test", "ZZZ_NO_SUCH_PRINTER_999_ZXCVBNM")
	up.SetTimeout(2 * time.Second)
	if err := up.Open(context.Background()); err == nil {
		t.Fatalf("expected Open to fail on a printer that does not exist")
	}
}

func TestUSBPrinterPingFailsOnMissingPrinter(t *testing.T) {
	up := NewUSB("test", "ZZZ_NO_SUCH_PRINTER_999_ZXCVBNM")
	up.SetTimeout(2 * time.Second)
	if err := up.Ping(context.Background()); err == nil {
		t.Fatalf("expected Ping to fail on a printer that does not exist")
	}
}

func TestUSBPrinterID(t *testing.T) {
	up := NewUSB("printer-a", "SomeQueue")
	if up.ID() != "printer-a" {
		t.Errorf("ID = %q, want printer-a", up.ID())
	}
}

func TestUSBPrinterCloseIsNoop(t *testing.T) {
	up := NewUSB("x", "SomeQueue")
	if err := up.Close(); err != nil {
		t.Errorf("Close should be a no-op, got %v", err)
	}
}
