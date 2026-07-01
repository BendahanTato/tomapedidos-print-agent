package printer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// FilePrinter writes the generated ESC/POS bytes to a file on disk. It is
// intended for development, integration tests and operators who want to
// inspect what the agent would send to a real printer. Each Write appends
// to the file (so a copy/paste into an emulator works) and rotates to a
// timestamped file when the file exceeds maxBytes.
type FilePrinter struct {
	id       string
	path     string
	maxBytes int64

	mu      sync.Mutex
	curSize int64
}

// NewFile returns a FilePrinter writing to path. maxBytes=0 means no rotation.
func NewFile(id, path string, maxBytes int64) *FilePrinter {
	return &FilePrinter{id: id, path: path, maxBytes: maxBytes}
}

// ID returns the printer's logical identifier.
func (p *FilePrinter) ID() string { return p.id }

// Open makes sure the destination directory exists.
func (p *FilePrinter) Open(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	// Truncate on open so a fresh agent starts a clean log.
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	p.curSize = 0
	return nil
}

// Write appends the payload to the backing file, rotating if needed.
func (p *FilePrinter) Write(_ context.Context, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.maxBytes > 0 && p.curSize+int64(len(payload)) > p.maxBytes {
		if err := p.rotateLocked(); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open for append: %w", err)
	}
	defer f.Close()
	n, err := f.Write(payload)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	p.curSize += int64(n)
	return nil
}

// Close is a no-op for FilePrinter (the file handle is opened per Write).
func (p *FilePrinter) Close() error { return nil }

// Ping is a no-op for FilePrinter; it is always considered online.
func (p *FilePrinter) Ping(_ context.Context) error { return nil }

// rotateLocked renames the current file with a timestamp suffix and starts
// a fresh one. Caller must hold p.mu.
func (p *FilePrinter) rotateLocked() error {
	if _, err := os.Stat(p.path); err != nil {
		// nothing to rotate
		p.curSize = 0
		return nil
	}
	rotated := fmt.Sprintf("%s.%s", p.path, strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Rename(p.path, rotated); err != nil {
		return fmt.Errorf("rotate file: %w", err)
	}
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open new file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close new file: %w", err)
	}
	p.curSize = 0
	return nil
}
