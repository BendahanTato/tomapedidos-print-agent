// Package logging configures the structured JSON logger used by every
// package in the print agent.
package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Level parses a log level name (debug, info, warn, error) into a slog.Level.
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a JSON slog logger writing to stdout at the given level.
// The logger includes the service name and version so logs can be
// filtered downstream without losing context.
func New(level slog.Level, service, version string) *slog.Logger {
	return NewWithWriter(level, service, version, os.Stdout)
}

// NewWithWriter returns a JSON slog logger writing to w at the given level.
// The logger includes the service name and version so logs can be filtered
// downstream without losing context.
func NewWithWriter(level slog.Level, service, version string, w io.Writer) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(
		slog.String("service", service),
		slog.String("version", version),
	)
}

// NewWithRotatingFile opens a rotating file sink and returns a logger that
// writes to it. If the file cannot be opened, it logs the failure to stdout
// and returns a stdout logger instead. The returned closer is nil on fallback.
func NewWithRotatingFile(level slog.Level, service, version, path string) (*slog.Logger, io.Closer, error) {
	w, err := NewRotatingFileWriter(path, 0)
	if err != nil {
		log := New(level, service, version)
		log.Error("could not open log file; falling back to stdout",
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		return log, nil, err
	}
	return NewWithWriter(level, service, version, w), w, nil
}

const (
	// DefaultMaxFileSize is the default maximum size of the active log file.
	DefaultMaxFileSize int64 = 5 * 1024 * 1024
	rotatedFileCount         = 3
)

// RotatingFileWriter appends to a log file and keeps up to three older files.
// Each Write is serialized and synced before it returns, so the writer keeps
// no buffered log data across calls.
type RotatingFileWriter struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	file    *os.File
	size    int64
	closed  bool
}

// NewRotatingFileWriter opens path in append mode. A maxSize less than or
// equal to zero selects DefaultMaxFileSize.
func NewRotatingFileWriter(path string, maxSize int64) (*RotatingFileWriter, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxFileSize
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &RotatingFileWriter{path: path, maxSize: maxSize, file: f, size: size}, nil
}

// Write appends p, rotating the active file first when the next write would
// exceed the configured limit. Writes are kept intact, so an individual
// write larger than the limit may itself exceed it.
func (w *RotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if syncErr := w.file.Sync(); err == nil {
		err = syncErr
	}
	return n, err
}

// Close flushes and closes the active log file. It is safe to call repeatedly.
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

func (w *RotatingFileWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	for i := rotatedFileCount; i >= 1; i-- {
		oldPath := w.rotatedPath(i - 1)
		newPath := w.rotatedPath(i)
		if i == rotatedFileCount {
			if err := os.Remove(newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return w.reopenAfterRotationError(err)
			}
		}
		if i > 1 {
			if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return w.reopenAfterRotationError(err)
			}
		}
	}
	if err := os.Rename(w.path, w.rotatedPath(1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return w.reopenAfterRotationError(err)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return w.reopenAfterRotationError(err)
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *RotatingFileWriter) reopenAfterRotationError(rotationErr error) error {
	f, reopenErr := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if reopenErr != nil {
		return errors.Join(rotationErr, reopenErr)
	}
	w.file = f
	if size, err := f.Seek(0, io.SeekEnd); err == nil {
		w.size = size
	} else {
		return errors.Join(rotationErr, err)
	}
	return rotationErr
}

func (w *RotatingFileWriter) rotatedPath(index int) string {
	if index == 0 {
		return w.path
	}
	return w.path + "." + strconv.Itoa(index)
}
