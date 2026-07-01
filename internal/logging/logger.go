// Package logging configures the structured JSON logger used by every
// package in the print agent. Output goes to stdout (suitable for systemd,
// launchd and Windows SCM to capture).
package logging

import (
	"log/slog"
	"os"
	"strings"
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
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(
		slog.String("service", service),
		slog.String("version", version),
	)
}
