package wrap

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// runFallback executes the child with direct stdio passthrough (no PTY),
// mirroring stdout/stderr to the tap. It is used on platforms without PTY
// support and as a degrade path when PTY allocation fails. Output is not a
// real terminal, so some TUIs may render in a plainer mode, but I/O and the
// exit code pass through faithfully.
func runFallback(cfg Config) (Result, error) {
	stdin, stdout, stderr := cfg.streams()

	child := exec.Command(cfg.Name, cfg.Args...)
	if cfg.Env != nil {
		child.Env = cfg.Env
	} else {
		child.Env = os.Environ()
	}
	child.Stdin = stdin

	// Mirror stdout (and stderr) through a single tap so the detector sees the
	// same byte stream a user does. The tap is closed once when both writers
	// are done.
	out := newTapWriter(stdout, cfg.Tap)
	child.Stdout = out
	if stderr == stdout {
		child.Stderr = out
	} else {
		// Different sinks: still mirror stderr to the tap, but only one writer
		// owns closing it. Wrap stderr without a tap channel and rely on the
		// stdout tapWriter to close.
		child.Stderr = io.MultiWriter(stderr, tapOnly{out})
	}

	err := child.Run()
	out.Close()

	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if code := ee.ExitCode(); code >= 0 {
				return Result{ExitCode: code}, nil
			}
			return Result{ExitCode: 1}, nil
		}
		// Failure to start (e.g. command not found).
		return Result{}, err
	}
	return Result{ExitCode: 0}, nil
}

// tapOnly forwards writes to a tapWriter's tap side without re-writing to its
// underlying writer (used to mirror stderr into the same tap as stdout).
type tapOnly struct{ t *tapWriter }

func (s tapOnly) Write(p []byte) (int, error) {
	if s.t.tap != nil && len(p) > 0 {
		chunk := make([]byte, len(p))
		copy(chunk, p)
		select {
		case s.t.tap <- chunk:
		default:
		}
	}
	return len(p), nil
}
