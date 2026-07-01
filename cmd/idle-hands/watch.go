package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rwrife/idle-hands/internal/card"
	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/store"
	"github.com/rwrife/idle-hands/internal/wrap"
)

// busyPollInterval is how often the watch loop ticks the detector so a BUSY
// window can be noticed even while the child emits nothing at all. It is much
// finer than the busy threshold so BUSY fires promptly once the gap is reached.
const busyPollInterval = 250 * time.Millisecond

// watchEnv bundles everything the transition handler needs: the card renderer
// (nil when unavailable), the stats store (nil when stats can't be opened), the
// quiet-hours window, and a clock for quiet-hours checks. It keeps handleState's
// signature small as the watch loop grows.
//
// suppressed tracks whether the in-flight BUSY window had its card withheld by
// quiet hours, so the matching IDLE transition stays equally silent (no "agent's
// back" line for a card that was never shown) while still recording the window.
type watchEnv struct {
	renderer   *card.Renderer
	store      *store.Store
	quiet      config.QuietHours
	now        func() time.Time
	suppressed bool
}

// cmdWatch runs the wrapped command under idle-hands. The child is spawned via
// internal/wrap (a PTY on Unix so interactive agent TUIs render identically to
// running them directly; a stdio passthrough on Windows). A copy of the child's
// output is tapped and fed to the M3 BUSY/IDLE detector: the detector flips to
// BUSY when output goes quiet (ignoring spinner/"thinking" noise) for the
// threshold, and back to IDLE on the next real output.
//
// M5 wires in config and stats. The busy threshold and deck now come from
// ~/.idle-hands/config.toml (falling back to the built-in defaults when there
// is no config). On each completed BUSY window the reclaimed span is recorded
// to ~/.idle-hands/state.json so `idle-hands stats` can report it. During
// configured quiet hours the card is suppressed (the agent is still wrapped and
// the window is still recorded; only the on-screen card is withheld).
//
// On each BUSY transition the M4 card engine renders exactly one card from the
// chosen deck to stderr; on IDLE it clears that card and prints "👋 agent's
// back — reclaimed Ns". The child's own stdout/stderr still flow through
// untouched, so the card never corrupts the agent's stream.
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

	// Load config; a missing file yields defaults. A malformed file is a real
	// error the user should fix, so surface it rather than guessing.
	cfg, err := config.Load()
	if err != nil {
		return 1, fmt.Errorf("config: %w", err)
	}

	det := detect.New(detect.Config{BusyThreshold: cfg.BusyThreshold})

	// Build the card renderer over the configured deck. A failure to load the
	// deck degrades gracefully: fall back to the plain one-line notices rather
	// than refusing to wrap the agent.
	renderer := newCardRenderer(cfg.Deck)

	// Open the stats store. If it can't be opened we still wrap the agent —
	// losing a scoreboard is no reason to fail the user's command — but we warn
	// once so a broken stats dir is visible.
	st, err := store.New(store.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-hands: stats unavailable (%v); not recording\n", err)
		st = nil
	}

	env := &watchEnv{
		renderer: renderer,
		store:    st,
		quiet:    cfg.Quiet,
		now:      time.Now,
	}

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
					handleState(ev, env)
				}
			case <-ticker.C:
				if ev, changed := det.Tick(time.Time{}); changed {
					handleState(ev, env)
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

// newCardRenderer loads the named deck and returns a card.Renderer writing to
// stderr. It resolves a user deck under ~/.idle-hands/decks over a built-in of
// the same name (matching `deck` preview), so a user's own deck actually drives
// the cards. If the deck can't be loaded it returns nil, and handleState falls
// back to plain one-line notices so watch still works.
func newCardRenderer(name string) *card.Renderer {
	userDir, err := config.DecksDir()
	if err != nil {
		// Can't locate the home dir; user decks are unavailable but built-ins
		// still are. Resolve against an empty dir (built-ins only).
		userDir = ""
	}
	d, _, err := deck.Resolve(name, userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-hands: deck %q unavailable (%v); using plain notices\n", name, err)
		return nil
	}
	return card.NewRenderer(os.Stderr, card.Options{Deck: d})
}

// newBufRenderer builds a card.Renderer for deck d writing to w. It exists so
// the watch loop's behavior can be exercised in tests against an in-memory
// buffer instead of a real terminal.
func newBufRenderer(w io.Writer, d deck.Deck) *card.Renderer {
	return card.NewRenderer(w, card.Options{Deck: d})
}

// handleState reacts to a detector transition. On BUSY it shows a card (unless
// quiet hours suppress it, or no renderer is available — then it prints the
// plain one-liner, or nothing during quiet hours). On IDLE it clears any card
// and records the reclaimed window to the stats store. Either way it writes
// only to stderr, never the child's stdout.
func handleState(ev detect.Event, env *watchEnv) {
	switch ev.State {
	case detect.StateBusy:
		if env.quiet.Contains(env.now()) {
			env.suppressed = true // remember so IDLE stays silent too
			return                // quiet hours: suppress the card entirely
		}
		env.suppressed = false
		if env.renderer == nil {
			reportBusyPlain(ev)
			return
		}
		env.renderer.OnBusy(ev.IdleFor)
	case detect.StateIdle:
		// Record the just-ended window regardless of whether a card was shown,
		// so quiet-hours windows still count toward reclaimed time.
		if env.store != nil {
			if err := env.store.Record(ev.IdleFor); err != nil {
				fmt.Fprintf(os.Stderr, "idle-hands: could not record stats (%v)\n", err)
			}
		}
		// If this window's card was suppressed by quiet hours, the screen never
		// changed, so say nothing now either.
		if env.suppressed {
			env.suppressed = false
			return
		}
		if env.renderer == nil {
			reportIdlePlain(ev)
			return
		}
		env.renderer.OnIdle(ev.IdleFor)
	}
}

// reportBusyPlain prints the plain one-line BUSY notice. It is the fallback
// used only when the card deck could not be loaded.
func reportBusyPlain(ev detect.Event) {
	fmt.Fprintf(os.Stderr, "\nidle-hands: 🤖 agent is thinking — your move (idle for %s)\n", ev.IdleFor.Round(time.Second))
}

// reportIdlePlain prints the plain one-line IDLE notice (deck-load fallback).
func reportIdlePlain(ev detect.Event) {
	fmt.Fprintf(os.Stderr, "\nidle-hands: 👋 agent's back — reclaimed %s\n", ev.IdleFor.Round(time.Second))
}
