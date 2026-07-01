// Package version exposes build-time metadata for the print agent.
//
// Version, Commit and BuildTime are populated via -ldflags by the build
// script (scripts/build.sh) so the running binary can self-identify in
// /health, /metrics and the log stream.
package version

// Variables overridden at build time with -ldflags.
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns a human-readable identifier combining the three fields.
func String() string {
	return Version + " (" + Commit + ") built " + BuildTime
}
