// Package version exposes the build version of idle-hands.
//
// The values here are overridden at build time via -ldflags, e.g.:
//
//	go build -ldflags "-X github.com/rwrife/idle-hands/internal/version.Version=v0.1.0"
//
// When built without ldflags (e.g. `go run`), it falls back to the module
// version recorded in the binary's build info, then to "dev".
package version

import "runtime/debug"

// These are intended to be overridden at build time with -ldflags -X.
var (
	// Version is the semantic version of the build (e.g. "v0.1.0").
	Version = ""
	// Commit is the short git commit the binary was built from.
	Commit = ""
	// Date is the build timestamp (RFC3339).
	Date = ""
)

// String returns a human-friendly version string. It prefers an explicitly
// injected Version, then the module version from build info, then "dev".
func String() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// Detail returns version plus commit/date when available, suitable for the
// `version` subcommand. Falls back to just String() when no build metadata
// was injected.
func Detail() string {
	out := String()
	if Commit != "" {
		out += " (" + Commit
		if Date != "" {
			out += ", " + Date
		}
		out += ")"
	} else if Date != "" {
		out += " (" + Date + ")"
	}
	return out
}
