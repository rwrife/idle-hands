package main

import (
	"github.com/rwrife/idle-hands/internal/wrap"
)

// cmdWatch runs the wrapped command under idle-hands. As of M2 it spawns the
// child through internal/wrap, which uses a PTY on Unix (so interactive agent
// TUIs render identically to running them directly) and a stdio passthrough on
// Windows. A copy of the child's output is tapped for the BUSY/IDLE detector
// that arrives in M3; for now the tap is simply drained.
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

	// Drain the output tap until wrap closes it. M3 replaces this consumer
	// with the detector state machine.
	tap := make(chan []byte, 64)
	done := make(chan struct{})
	go func() {
		for range tap {
			// Discard for now; the bytes have already been written to the
			// real terminal by the wrapper.
		}
		close(done)
	}()

	res, err := wrap.Run(wrap.Config{
		Name: args[0],
		Args: args[1:],
		Tap:  tap,
	})
	<-done

	if err != nil {
		// Failure to start the child (e.g. command not found).
		return 1, err
	}
	return res.ExitCode, nil
}
