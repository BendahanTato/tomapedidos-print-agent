package printer

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
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

func TestNetworkPrinterWriteFailureIsAmbiguous(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			// Send a reset so the client observes a write failure instead
			// of accepting buffered data after the peer has gone away.
			_ = tcp.SetLinger(0)
		}
		_ = conn.Close()
		close(closed)
	}()

	addr := listener.Addr().(*net.TCPAddr)
	np := NewNetwork("test", "127.0.0.1", addr.Port)
	np.SetTimeout(2 * time.Second)
	defer np.Close()
	if err := np.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server did not close the accepted connection")
	}

	// Wait until the peer's reset is visible on the client socket before
	// writing, avoiding a race where a small payload fits in its send buffer.
	_ = np.conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := np.conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected the closed peer to be visible on the connection")
	}

	err = np.Write(context.Background(), make([]byte, 1<<20))
	if err == nil {
		t.Fatal("Write succeeded after the peer closed the connection")
	}
	if !errors.Is(err, ErrAmbiguousWrite) {
		t.Fatalf("Write error %v does not wrap ErrAmbiguousWrite", err)
	}
}

func TestNewFromConfigPropagatesNetworkTimeout(t *testing.T) {
	printerConfig := config.Default().Printers[0]
	printerConfig.Host = "127.0.0.1"
	printerConfig.Port = 1 // No live printer is needed to construct the backend.
	printerConfig.TimeoutMs = 23

	pr, _, err := NewFromConfig(context.Background(), printerConfig)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	np, ok := pr.(*NetworkPrinter)
	if !ok {
		t.Fatalf("NewFromConfig returned %T, want *NetworkPrinter", pr)
	}
	defer np.Close()
	if got, want := np.timeout, 23*time.Millisecond; got != want {
		t.Errorf("network timeout = %v, want %v", got, want)
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
