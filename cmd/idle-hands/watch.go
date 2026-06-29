package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rwrife/idle-hands/internal/card"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/wrap"
)

// busyPollInterval is how often the watch loop ticks the detector so a BUSY
// window can be noticed even while the child emits nothing at all. It is much
// finer than the busy threshold so BUSY fires promptly once the gap is reached.
const busyPollInterval = 250 * time.Millisecond

// defaultDeck is the deck shown when the agent goes BUSY. Choosing the deck via
// config lands in M5; for M4 we ship the body-reset deck as the sane default.
const defaultDeck = "move"

// cmdWatch runs the wrapped command under idle-hands. The child is spawned via
// internal/wrap (a PTY on Unix so interactive agent TUIs render identically to
// running them directly; a stdio passthrough on Windows). A copy of the child's
// output is tapped and fed to the M3 BUSY/IDLE detector: the detector flips to
// BUSY when output goes quiet (ignoring spinner/"thinking" noise) for the
// threshold, and back to IDLE on the next real output.
//
// On each BUSY transition the M4 card engine renders exactly one card from the
// chosen deck (default "move") to stderr; on IDLE it clears that card and
// prints "👋 agent's back — reclaimed Ns". The child's own stdout/stderr still
// flow through untouched, so the card never corrupts the agent's stream.
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

	// Build the card renderer over the default deck. A failure to load the
	// embedded deck is a build-time bug, but we degrade gracefully: fall back
	// to the plain one-line notices rather than refusing to wrap the agent.
	renderer := newCardRenderer(defaultDeck)

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
					handleState(ev, renderer)
				}
			case <-ticker.C:
				if ev, changed := det.Tick(time.Time{}); changed {
					handleState(ev, renderer)
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

// newCardRenderer loads the named built-in deck and returns a card.Renderer
// writing to stderr. If the deck can't be loaded it returns nil, and
// handleState falls back to plain one-line notices so watch still works.
func newCardRenderer(name string) *card.Renderer {
	d, err := deck.Builtin(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-hands: deck %q unavailable (%v); using plain notices\n", name, err)
		return nil
	}
	return card.NewRenderer(os.Stderr, card.Options{Deck: d})
}

// handleState reacts to a detector transition. With a renderer it shows/clears
// a card; without one (deck load failed) it prints the plain M3 one-liners.
// Either way it writes only to stderr, never the child's stdout.
func handleState(ev detect.Event, renderer *card.Renderer) {
	if renderer == nil {
		reportStatePlain(ev)
		return
	}
	switch ev.State {
	case detect.StateBusy:
		renderer.OnBusy(ev.IdleFor)
	case detect.StateIdle:
		renderer.OnIdle(ev.IdleFor)
	}
}

// reportStatePlain prints the plain one-line notice for a transition. It is the
// fallback used only when the card deck could not be loaded.
func reportStatePlain(ev detect.Event) {
	switch ev.State {
	case detect.StateBusy:
		fmt.Fprintf(os.Stderr, "\nidle-hands: 🤖 agent is thinking — your move (idle for %s)\n", ev.IdleFor.Round(time.Second))
	case detect.StateIdle:
		fmt.Fprintf(os.Stderr, "\nidle-hands: 👋 agent's back — reclaimed %s\n", ev.IdleFor.Round(time.Second))
	}
}
