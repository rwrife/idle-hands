//go:build !windows

package wrap

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// Run executes the command described by cfg under a pseudo-terminal, passing
// I/O through transparently and mirroring output to cfg.Tap. It blocks until
// the child exits and returns how it terminated.
//
// If allocating a PTY fails (rare, e.g. no /dev/ptmx in a constrained
// sandbox), Run transparently falls back to direct stdio passthrough so the
// wrapper still works.
func Run(cfg Config) (Result, error) {
	stdin, stdout, _ := cfg.streams()

	child := exec.Command(cfg.Name, cfg.Args...)
	if cfg.Env != nil {
		child.Env = cfg.Env
	} else {
		child.Env = os.Environ()
	}

	ptmx, err := pty.Start(child)
	if err != nil {
		// Could not get a PTY (e.g. exec failure or no pty device). If the
		// command itself doesn't exist, surface that as a start error; any
		// other PTY-allocation problem degrades to the stdio fallback.
		if errors.Is(err, exec.ErrNotFound) || isNoSuchFile(err) {
			closeTap(cfg.Tap)
			return Result{}, err
		}
		return runFallback(cfg)
	}
	defer func() { _ = ptmx.Close() }()

	// If our stdin is a real terminal, flip it to raw mode so keystrokes reach
	// the child untranslated, and forward window-size changes. When stdin is a
	// pipe/file (tests, CI, `< file`), skip both — there's no TTY to mirror.
	var restore func()
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if oldState, err := term.MakeRaw(int(f.Fd())); err == nil {
			restore = func() { _ = term.Restore(int(f.Fd()), oldState) }
		}
		stopResize := forwardResize(ptmx, f)
		defer stopResize()
	}
	if restore != nil {
		defer restore()
	}

	// Pump stdin → child. This goroutine outlives the copy below; it ends when
	// stdin hits EOF or the PTY closes. We don't wait on it (a blocked read on
	// the real terminal would otherwise hang exit).
	go func() { _, _ = io.Copy(ptmx, stdin) }()

	// Pump child → stdout, mirroring to the tap. Read to EOF (PTY closes when
	// the child exits). On Linux, reading a PTY master after the child exits
	// yields EIO rather than EOF; treat that as a clean end.
	out := newTapWriter(stdout, cfg.Tap)
	_, copyErr := io.Copy(out, ptmx)
	out.Close()

	waitErr := child.Wait()
	if restore != nil {
		restore()
		restore = nil
	}

	res := Result{ExitCode: exitCode(waitErr), PTY: true}
	if copyErr != nil && !isEIO(copyErr) {
		// Surface a genuine I/O error only when the child itself succeeded;
		// otherwise the exit code is the more useful signal.
		if res.ExitCode == 0 {
			return res, copyErr
		}
	}
	return res, nil
}

// forwardResize sends the current terminal size to the PTY immediately and on
// every SIGWINCH, until the returned stop function is called.
func forwardResize(ptmx *os.File, tty *os.File) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	// Prime once so the child starts at the right size.
	_ = pty.InheritSize(tty, ptmx)
	go func() {
		for range ch {
			_ = pty.InheritSize(tty, ptmx)
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}

// exitCode extracts a process exit status from the error returned by Wait.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		// Terminated by signal: mirror the shell 128+signal convention.
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return 1
	}
	return 1
}

func isEIO(err error) bool        { return errors.Is(err, syscall.EIO) }
func isNoSuchFile(err error) bool { return errors.Is(err, syscall.ENOENT) }
