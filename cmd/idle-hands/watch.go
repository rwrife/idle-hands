package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/wrap"
)

// busyPollInterval is how often the watch loop ticks the detector so a BUSY
// window can be noticed even while the child emits nothing at all. It is much
// finer than the busy threshold so BUSY fires promptly once the gap is reached.
const busyPollInterval = 250 * time.Millisecond

// cmdWatch runs the wrapped command under idle-hands. The child is spawned via
// internal/wrap (a PTY on Unix so interactive agent TUIs render identically to
// running them directly; a stdio passthrough on Windows). A copy of the child's
// output is tapped and fed to the M3 BUSY/IDLE detector: the detector flips to
// BUSY when output goes quiet (ignoring spinner/"thinking" noise) for the
// threshold, and back to IDLE on the next real output.
//
// For M3 there is no card UI yet (that lands in M4); state changes are reported
// on stderr so the detector's behavior is observable end-to-end. The child's
// own stdout/stderr still flow through untouched.
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

	det := detect.New(detect.Config{}) // default 20s threshold, wall clock

	// Drain the output tap, feeding each chunk to the detector. A ticker on the
	// same loop advances the time-based BUSY check. The loop exits when wrap
	// closes the tap (child output reached EOF).
	tap := make(chan []byte, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(busyPollInterval)
		defer ticker.Stop()
		for {
			select {
			case chunk, ok := <-tap:
				if !ok {
					return // wrap finished; bytes already hit the real terminal
				}
				if ev, changed := det.Feed(chunk); changed {
					reportState(ev)
				}
			case <-ticker.C:
				if ev, changed := det.Tick(time.Time{}); changed {
					reportState(ev)
				}
			}
		}
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

// reportState prints a one-line notice for a detector transition. This is a
// placeholder surface for M3; M4 replaces it with the rendered card on BUSY and
// a "👋 agent's back" clear on IDLE. Written to stderr so it never pollutes the
// child's stdout stream.
func reportState(ev detect.Event) {
	switch ev.State {
	case detect.StateBusy:
		fmt.Fprintf(os.Stderr, "\nidle-hands: 🤖 agent is thinking — your move (idle for %s)\n", ev.IdleFor.Round(time.Second))
	case detect.StateIdle:
		fmt.Fprintf(os.Stderr, "\nidle-hands: 👋 agent's back — reclaimed %s\n", ev.IdleFor.Round(time.Second))
	}
}
