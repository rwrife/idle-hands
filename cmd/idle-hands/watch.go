package main

import (
	"os"
	"os/exec"
)

// cmdWatch implements the M1 stub of `watch`: it runs the wrapped command with
// stdio passed straight through and returns the child's exit code. BUSY/IDLE
// detection, PTY wrapping, and cards arrive in M2–M4; for now this just proves
// the wrapper execs transparently and exits cleanly.
//
// A leading "--" separator (idle-hands watch -- echo hi) is stripped so flags
// can be passed to the child without idle-hands trying to parse them.
func cmdWatch(args []string) (int, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return 2, errNoCommand
	}

	child := exec.Command(args[0], args[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = os.Environ()

	if err := child.Run(); err != nil {
		// Surface the child's real exit code when it ran but exited non-zero
		// (including termination by signal). Only treat a failure to *start*
		// the process as an idle-hands-level error.
		if ee, ok := err.(*exec.ExitError); ok {
			if code := ee.ExitCode(); code >= 0 {
				return code, nil
			}
			// ExitCode() == -1 means terminated by a signal; mirror the
			// conventional 128+signal shell convention when we can.
			if ws, ok := ee.Sys().(interface{ Signal() os.Signal }); ok {
				_ = ws // platform-specific; fall through to generic non-zero.
			}
			return 1, nil
		}
		return 1, err
	}
	return 0, nil
}
