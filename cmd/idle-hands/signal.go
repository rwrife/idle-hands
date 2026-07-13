package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	sig "github.com/rwrife/idle-hands/internal/signal"
)

// cmdSignal implements the `signal` subcommand (issue #23): plugin signals.
//
// Two modes:
//
//   - `idle-hands signal` (no args): server mode. Bind the local, user-owned
//     socket and translate incoming busy/idle events into the same card/stats
//     pipeline the watchers use. Runs until Ctrl-C.
//   - `idle-hands signal busy` / `idle-hands signal idle`: client mode. Connect
//     to the running server and deliver one authoritative event, then exit.
//     This is the trivial helper scripts and IDE extensions call.
func cmdSignal(args []string) (int, error) {
	if len(args) == 0 {
		return signalServe()
	}

	switch args[0] {
	case "busy", "idle":
		if len(args) > 1 {
			return 2, fmt.Errorf("signal: %q takes no extra arguments", args[0])
		}
		return signalClient(sig.State(args[0]))
	default:
		return 2, fmt.Errorf("signal: unknown argument %q (use `idle-hands signal` to listen, or `signal busy|idle` to send)", args[0])
	}
}

// signalServe runs the listener until interrupted. External events are
// authoritative, so instead of the quiet-timeout detector we drive a
// signal.Coordinator (which makes duplicate busy/idle events idempotent) and
// dispatch each real transition through the shared handleState — giving plugin
// signals identical cards, quiet-hours suppression, and reclaimed-time stats.
func signalServe() (int, error) {
	cfg, err := config.Load()
	if err != nil {
		return 1, fmt.Errorf("config: %w", err)
	}

	ln, err := sig.Listen()
	if err != nil {
		return 1, err
	}
	defer ln.Close()

	env := newWatchEnv(cfg)
	coord := sig.NewCoordinator(time.Now)

	fmt.Fprintf(os.Stderr, "idle-hands 🙌 listening for plugin signals at %s — POST busy/idle to drive a card. Ctrl-C to stop.\n", ln.Addr())

	// Serve runs in its own goroutine; a stop signal closes the listener, which
	// unblocks Accept and lets Serve return cleanly.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sig.Serve(ln,
			func(ev sig.Event) {
				if det, changed := coord.Apply(ev); changed {
					handleState(det, env)
				}
			},
			func(perr error) {
				fmt.Fprintf(os.Stderr, "idle-hands: %v\n", perr)
			},
		)
	}()

	<-stop
	clearOnExit(env) // don't leave a card on screen after Ctrl-C
	_ = ln.Close()   // triggers a clean Serve return and removes the socket file
	<-done
	fmt.Fprintln(os.Stderr, "\nidle-hands: signal listener stopped.")
	return 0, nil
}

// signalClient connects to a running listener and sends exactly one event.
func signalClient(state sig.State) (int, error) {
	conn, err := sig.Dial()
	if err != nil {
		return 1, err
	}
	defer conn.Close()

	if _, err := conn.Write(sig.Encode(sig.Event{State: state, Source: "cli"})); err != nil {
		return 1, fmt.Errorf("signal: send %s: %w", state, err)
	}
	return 0, nil
}
