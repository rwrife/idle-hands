// Package wrap runs a child command transparently while tapping a copy of its
// output for downstream consumers (the BUSY/IDLE detector, arriving in M3).
//
// On Unix-like systems the child is spawned under a pseudo-terminal (PTY) so
// interactive agent TUIs render exactly as they would when run directly: the
// program sees a real terminal, colors and cursor control work, and terminal
// resizes (SIGWINCH) are forwarded. A copy of everything the child writes is
// mirrored to an optional output tap.
//
// On platforms without PTY support (Windows), Run falls back to a direct
// stdio passthrough that still mirrors output to the tap. The fallback cannot
// be byte-for-byte identical to a real TTY, but it keeps the wrapper usable
// everywhere and preserves the exit code.
package wrap

import (
	"io"
	"os"
)

// Config controls how Run executes the child command.
type Config struct {
	// Name is the child executable (looked up via PATH).
	Name string
	// Args are the child's arguments (not including Name).
	Args []string
	// Env is the child environment. When nil, the current process env is used.
	Env []string

	// Stdin/Stdout/Stderr are the streams the wrapper attaches to. When nil
	// they default to the corresponding os.Std* stream. They are exposed
	// mainly so tests can drive the wrapper without touching the real
	// terminal.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Tap, when non-nil, receives a copy of every chunk the child writes to
	// its output. Chunks are freshly allocated, so consumers may retain them.
	// The channel is closed when the child's output stream reaches EOF. If a
	// send would block, the chunk is dropped rather than stalling the child's
	// I/O — taps are advisory signal, never backpressure.
	Tap chan<- []byte
}

// Result reports how the child terminated.
type Result struct {
	// ExitCode is the child's exit status. For a child terminated by a signal
	// it follows the conventional 128+signal value where the platform allows
	// it; otherwise it is a generic non-zero code.
	ExitCode int
	// PTY reports whether the child actually ran under a pseudo-terminal.
	// False means the platform fallback (direct stdio) was used.
	PTY bool
}

// stream returns the configured reader/writers, defaulting any nil field to
// the matching real standard stream.
func (c Config) streams() (stdin io.Reader, stdout, stderr io.Writer) {
	stdin = c.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout = c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr = c.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdin, stdout, stderr
}

// tapWriter mirrors everything written to it into w, and also forwards a copy
// to tap (best-effort, never blocking). A nil tap makes it a transparent
// pass-through to w. Close closes the tap channel exactly once.
type tapWriter struct {
	w      io.Writer
	tap    chan<- []byte
	closed bool
}

func newTapWriter(w io.Writer, tap chan<- []byte) *tapWriter {
	return &tapWriter{w: w, tap: tap}
}

func (t *tapWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if t.tap != nil && n > 0 {
		// Copy the bytes actually written so consumers can safely retain them.
		chunk := make([]byte, n)
		copy(chunk, p[:n])
		select {
		case t.tap <- chunk:
		default:
			// Drop rather than block the child's output path.
		}
	}
	return n, err
}

// Close closes the underlying tap channel (once). It does not close t.w, which
// is owned by the caller (typically os.Stdout).
func (t *tapWriter) Close() {
	if t.tap != nil && !t.closed {
		close(t.tap)
		t.closed = true
	}
}

// closeTap closes a tap channel if it is non-nil. It is used on early-return
// paths (e.g. the child failed to start) where no tapWriter was created, so
// that Run always honors its contract of closing the tap before returning.
func closeTap(tap chan<- []byte) {
	if tap != nil {
		close(tap)
	}
}
