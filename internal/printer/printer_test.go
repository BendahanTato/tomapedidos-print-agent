package printer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilePrinterWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	fp := NewFile("test", path, 0)
	if err := fp.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := fp.Write(context.Background(), []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := fp.Write(context.Background(), []byte(" world")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file content = %q, want %q", got, "hello world")
	}
}

func TestFilePrinterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot.bin")
	fp := NewFile("test", path, 8) // 8 bytes cap
	if err := fp.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := fp.Write(context.Background(), []byte("12345")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if err := fp.Write(context.Background(), []byte("67890")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	// After 5+5 bytes the file should have rotated at 8 bytes.
	// The new file should contain "67890" (the second write that
	// triggered rotation).
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "67890" {
		t.Errorf("after rotation content = %q, want 67890", got)
	}
}

func TestNetworkPrinterRejectsUnreachable(t *testing.T) {
	// Port 1 is reserved and should always refuse connections.
	np := NewNetwork("test", "127.0.0.1", 1)
	np.SetTimeout(200 * time.Millisecond)
	if err := np.Open(context.Background()); err == nil {
		_ = np.Close()
		t.Fatalf("expected Open to fail on 127.0.0.1:1")
	}
}

func TestDetectType(t *testing.T) {
	tests := []struct {
		makeAndModel string
		want         string
	}{
		{"NETUM 5890K-LN", "usb"},
		{"NETUM Type 5890K", "usb"},
		{"POS-58 Printer", "usb"},
		{"POS58 Series", "usb"},
		{"Epson TM-T20III", "usb"},
		{"HP LaserJet Pro M404n", "usb-office"},
		{"Brother HL-L2360D series", "usb-office"},
	}

	for _, tt := range tests {
		got := DetectType(tt.makeAndModel)
		if got != tt.want {
			t.Errorf("DetectType(%q) = %q, want %q", tt.makeAndModel, got, tt.want)
		}
	}
}

