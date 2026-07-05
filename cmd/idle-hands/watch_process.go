package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/procwatch"
)

// processPollInterval is how often the standalone watcher samples the target
// process's CPU and ticks the detector. It matches busyPollInterval so BUSY
// fires at the same resolution whether detection is driven by a wrapped
// command's output or by a process's activity.
const processPollInterval = busyPollInterval

// activityMarker is the synthetic "real output" chunk fed to the detector when
// the watched process is Active. The detector's isRealProgress treats any
// newline-terminated, non-keyword text as progress, so this snaps it back to
// IDLE exactly as fresh agent output would. Its content is never shown to the
// user; only its classification matters.
var activityMarker = []byte("process-activity\n")

// cmdWatchProcess implements `idle-hands watch --process <name>`: the standalone
// watcher mode from issue #10. Rather than wrapping and tee-ing a command, it
// resolves a currently-running process by name and samples its CPU activity,
// feeding that into the *same* BUSY/IDLE detector, card renderer, and stats
// store the wrapped mode uses. When the process goes quiet (blocked, waiting on
// I/O, "thinking") for the busy threshold, one card fires; when it starts
// burning CPU again the card clears and the reclaimed window is recorded. This
// is what lets idle-hands support GUI agents and IDE sidebars that can't be run
// as a wrapped child.
//
// Platform note: CPU sampling is implemented on Linux (via /proc). On macOS and
// Windows the sampler reports unsupported and we exit with a clear message
// (tracked as a follow-up on issue #10); the detector/card wiring is portable,
// so only the OS sampler is missing there.
func cmdWatchProcess(name string, cfg config.Config, detCfg detect.Config) (int, error) {
	sampler, err := procwatch.NewNameSampler(name)
	if err != nil {
		if errors.Is(err, procwatch.ErrNotFound) {
			return 1, fmt.Errorf("watch: no running process named %q (start it first, or check the name)", name)
		}
		return 1, fmt.Errorf("watch: %w", err)
	}

	poller, err := procwatch.NewPoller(sampler, procwatch.Options{Interval: processPollInterval})
	if err != nil {
		return 1, fmt.Errorf("watch: %w", err)
	}

	det := detect.New(detCfg)
	env := newWatchEnv(cfg)

	// Announce what we're watching so the user knows detection is live and on
	// which process (helpers/multiple matches are resolved to one pid).
	fmt.Fprintf(os.Stderr, "idle-hands 🙌 watching %s — one card when it goes quiet for %s. Ctrl-C to stop.\n",
		poller.Describe(), det.Threshold().Round(time.Second))

	// Stop cleanly on Ctrl-C / SIGTERM so the terminal is left tidy (a shown
	// card is cleared on the way out).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	return runProcessLoop(poller, det, env, processPollInterval, stop)
}

// runProcessLoop is the testable core of the standalone watcher. It ticks on
// `interval`, and on each tick: takes one poll; feeds an Active reading to the
// detector as real progress (which can transition BUSY→IDLE) and always ticks
// the detector's clock (which can transition IDLE→BUSY once the quiet threshold
// is reached); and stops on process exit or a stop signal. Every detector
// transition is dispatched through the shared handleState so cards, quiet
// hours, and stats behave identically to wrapped mode.
//
// The clock and poll cadence are parameters so tests can drive it with a fake
// clock/sampler and a closeable stop channel instead of real time. It returns
// exit code 0 on a clean stop (process exit or signal).
func runProcessLoop(poller procLoopPoller, det *detect.Detector, env *watchEnv, interval time.Duration, stop <-chan os.Signal) (int, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// A transient sampler error shouldn't kill the watch; but a persistent one
	// (e.g. permissions) would spin silently, so warn once and keep going.
	warnedErr := false

	for {
		select {
		case <-stop:
			clearOnExit(env)
			return 0, nil
		case <-ticker.C:
			reading, err := poller.Poll()
			if err != nil {
				if !warnedErr {
					fmt.Fprintf(os.Stderr, "idle-hands: sampling hiccup (%v); still watching\n", err)
					warnedErr = true
				}
				continue
			}
			warnedErr = false

			switch reading.Kind {
			case procwatch.Exited:
				clearOnExit(env)
				fmt.Fprintln(os.Stderr, "idle-hands: watched process exited — done.")
				return 0, nil
			case procwatch.Active:
				// Real work: feed the detector so a BUSY window ends and gets
				// recorded, then still tick the clock for consistency.
				if ev, changed := det.Feed(activityMarker); changed {
					handleState(ev, env)
				}
			case procwatch.Quiet:
				// Nothing to feed; the tick below is what eventually fires BUSY.
			}

			if ev, changed := det.Tick(time.Time{}); changed {
				handleState(ev, env)
			}
		}
	}
}

// procLoopPoller is the minimal poller surface runProcessLoop needs, so tests
// can supply a scripted poller without a real process or sampler.
type procLoopPoller interface {
	Poll() (procwatch.Reading, error)
}

// clearOnExit clears any on-screen card when the watch stops, so a card shown
// during a final BUSY window doesn't linger after we return control to the
// shell. It mirrors what an IDLE transition would do visually, without
// recording a (non-existent) reclaimed window.
func clearOnExit(env *watchEnv) {
	if env != nil && env.renderer != nil && env.renderer.Shown() {
		env.renderer.OnIdle(0)
	}
}
