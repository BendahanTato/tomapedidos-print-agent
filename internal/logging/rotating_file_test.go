package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFileWriterKeepsThreeBackupsAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "print-agent.log")
	w, err := NewRotatingFileWriter(path, 4)
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	for _, record := range []string{"aa", "bb", "cc", "dd", "ee", "ff", "gg", "hh", "ii"} {
		if _, err := io.WriteString(w, record); err != nil {
			t.Fatalf("Write(%q): %v", record, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening appends to the current file instead of truncating it.
	w, err = NewRotatingFileWriter(path, 4)
	if err != nil {
		t.Fatalf("reopen writer: %v", err)
	}
	if _, err := io.WriteString(w, "jj"); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close reopened writer: %v", err)
	}

	wantFiles := map[string]string{
		path:        "iijj",
		path + ".1": "gghh",
		path + ".2": "eeff",
		path + ".3": "ccdd",
	}
	for file, want := range wantFiles {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", file, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(file), got, want)
		}
	}
	if _, err := os.Stat(path + ".4"); !os.IsNotExist(err) {
		t.Errorf("unexpected fourth backup: stat error = %v", err)
	}
}

func TestNewWithRotatingFileFallsBackToStdout(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	path := filepath.Join(blocker, "print-agent.log")

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = writeEnd
	defer func() { os.Stdout = oldStdout }()

	log, closer, openErr := NewWithRotatingFile(slog.LevelInfo, "print-agent", "test", path)
	if openErr == nil {
		t.Fatal("expected file-open error")
	}
	if closer != nil {
		t.Fatal("fallback returned a file closer")
	}
	log.Info("fallback marker")
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = oldStdout

	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	_ = readEnd.Close()
	if !strings.Contains(string(output), "could not open log file; falling back to stdout") || !strings.Contains(string(output), "fallback marker") {
		t.Errorf("stdout output missing fallback details: %s", output)
	}
}
