package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rwrife/idle-hands/internal/card"
	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/duckdiff"
	"github.com/rwrife/idle-hands/internal/events"
	"github.com/rwrife/idle-hands/internal/focus"
	"github.com/rwrife/idle-hands/internal/hook"
	"github.com/rwrife/idle-hands/internal/preset"
	"github.com/rwrife/idle-hands/internal/srs"
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

	// focus is the focus-safe-mode state store (nil when unavailable). When a
	// focus block is active, cards are suppressed even outside quiet hours.
	focus *focus.Store
	// focusSuppressStats mirrors config focus_safe.suppress_stats: when true, a
	// window suppressed by an active focus block is not recorded to stats.
	focusSuppressStats bool

	// events is the optional ndjson event emitter (nil-safe: a nil *Emitter is a
	// no-op, so handleState calls it unconditionally). It is fed BUSY/IDLE state
	// transitions; card show/dismiss events come from the renderer's hooks.
	events *events.Emitter
	// focusActive records whether the in-flight BUSY window was suppressed by an
	// active focus block, so the matching IDLE transition can honor
	// focusSuppressStats without re-checking the clock (which may have advanced
	// past the focus deadline mid-window).
	focusActive bool
}

// newWatchEnv builds the shared runtime environment both watch modes use: the
// card renderer over the configured deck (nil on load failure, in which case
// handleState falls back to plain notices), the stats store (nil, with a single
// warning, if it can't be opened), the configured quiet-hours window, and the
// real clock. Centralizing it keeps the wrapped-command and standalone-process
// paths behaving identically around cards, stats, and quiet hours.
func newWatchEnv(cfg config.Config) *watchEnv {
	emitter := newEventEmitter(cfg)
	renderer := newCardRenderer(cfg, emitter)
	st, err := store.New(store.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-hands: stats unavailable (%v); not recording\n", err)
		st = nil
	}
	foc, err := focus.New(focus.Options{})
	if err != nil {
		// Focus-safe mode is a nicety; if its store can't be opened, warn once
		// and carry on wrapping the agent without focus suppression.
		fmt.Fprintf(os.Stderr, "idle-hands: focus-safe mode unavailable (%v); cards will show normally\n", err)
		foc = nil
	}
	return &watchEnv{
		renderer:           renderer,
		store:              st,
		quiet:              cfg.Quiet,
		now:                time.Now,
		focus:              foc,
		focusSuppressStats: cfg.FocusSafe.SuppressStats,
		events:             emitter,
	}
}

// newEventEmitter builds the ndjson event emitter for the watch session, or a
// nil (no-op) emitter when the stream is disabled. When enabled it writes to the
// configured file descriptor (default 2, stderr) so the wrapped agent's stdout
// stays untouched. An unusable fd is a warning, not a fatal error: the agent is
// still wrapped, just without the event stream.
func newEventEmitter(cfg config.Config) *events.Emitter {
	if !cfg.JSON.Enabled {
		return nil
	}
	var w io.Writer
	switch cfg.JSON.FD {
	case 1:
		// Guarded against in config, but stay defensive: never emit to stdout.
		fmt.Fprintf(os.Stderr, "idle-hands: json_fd 1 (stdout) is not allowed; using stderr\n")
		w = os.Stderr
	case 2, 0:
		w = os.Stderr
	default:
		f := os.NewFile(uintptr(cfg.JSON.FD), fmt.Sprintf("fd/%d", cfg.JSON.FD))
		if f == nil {
			fmt.Fprintf(os.Stderr, "idle-hands: json_fd %d is not open; event stream disabled\n", cfg.JSON.FD)
			return nil
		}
		w = f
	}
	return events.New(w)
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
//
// idle-hands' own flags (currently just --preset <name>) may appear before that
// separator; they select a built-in detector profile tuned for a known agent
// (Claude Code, Aider, Cursor, Codex, gh copilot). With no --preset the detector
// keeps its generic quiet-timeout behavior. An explicit busy_threshold in config
// still wins over the preset's suggested threshold.
func cmdWatch(args []string) (int, error) {
	flags, args, err := parseWatchFlags(args)
	if err != nil {
		return 2, err
	}

	// Load config; a missing file yields defaults. A malformed file is a real
	// error the user should fix, so surface it rather than guessing.
	cfg, err := config.Load()
	if err != nil {
		return 1, fmt.Errorf("config: %w", err)
	}

	detCfg, err := detectorConfig(cfg, flags.preset)
	if err != nil {
		return 2, err
	}

	// Watch flags override the config's event-stream settings so
	// `idle-hands watch --json -- <agent>` works without editing config.
	if flags.json {
		cfg.JSON.Enabled = true
	}
	if flags.jsonFD != nil {
		fd := *flags.jsonFD
		if fd == 1 {
			return 2, fmt.Errorf("watch: --json-fd 1 (stdout) is not allowed; it would corrupt the agent's output — use 2 (stderr)")
		}
		cfg.JSON.FD = fd
	}

	// Standalone watcher mode: instead of wrapping a command, watch an
	// already-running process's CPU activity through the same detector/card/
	// store pipeline. `--process` and a wrapped command are mutually exclusive.
	if flags.process != "" {
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 0 {
			return 2, fmt.Errorf("watch: --process %q cannot be combined with a wrapped command (%s)", flags.process, args[0])
		}
		return cmdWatchProcess(flags.process, cfg, detCfg)
	}

	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return 2, errNoCommand
	}

	det := detect.New(detCfg)

	env := newWatchEnv(cfg)

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

// watchFlags holds idle-hands' own parsed flags for the watch subcommand.
type watchFlags struct {
	// preset is the selected agent preset name ("" if none).
	preset string
	// process is the target process name for standalone watcher mode ("" when
	// wrapping a command instead).
	process string
	// json, when set, enables the ndjson event stream (overrides config).
	json bool
	// jsonFD, when non-nil, overrides the event-stream file descriptor.
	jsonFD *int
}

// parseWatchFlags pulls idle-hands' own flags off the front of the watch
// argument list and returns them plus the remaining args (the wrapped command
// and its arguments, including any leading "--"). Only flags before the first
// non-flag token (or an explicit "--") are consumed, so
// `idle-hands watch --preset claude -- claude --dangerously` passes
// --dangerously straight to the child.
//
// Supported forms: `--preset <name>`/`--preset=<name>` and
// `--process <name>`/`--process=<name>` (also single-dash). An unknown
// idle-hands flag is an error rather than being forwarded, so a typo like
// --presett is caught instead of silently handed to the agent.
func parseWatchFlags(args []string) (flags watchFlags, rest []string, err error) {
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" {
			return flags, args[i:], nil // leave the separator for cmdWatch
		}
		// Once we hit a token that isn't one of our flags, everything from here
		// on is the child command.
		if len(arg) < 2 || arg[0] != '-' {
			return flags, args[i:], nil
		}
		name := arg
		val := ""
		hasVal := false
		if eq := indexByte(arg, '='); eq >= 0 {
			name, val, hasVal = arg[:eq], arg[eq+1:], true
		}
		switch name {
		case "--preset", "-preset":
			if !hasVal {
				if i+1 >= len(args) {
					return watchFlags{}, nil, fmt.Errorf("watch: --preset needs a value (one of: %s)", joinNames())
				}
				val = args[i+1]
				i++
			}
			if _, ok := preset.Lookup(val); !ok {
				return watchFlags{}, nil, preset.ErrorFor(val)
			}
			flags.preset = val
		case "--process", "-process":
			if !hasVal {
				if i+1 >= len(args) {
					return watchFlags{}, nil, fmt.Errorf("watch: --process needs a value (a running process name)")
				}
				val = args[i+1]
				i++
			}
			if strings.TrimSpace(val) == "" {
				return watchFlags{}, nil, fmt.Errorf("watch: --process needs a non-empty process name")
			}
			flags.process = val
		case "--json", "-json":
			// Boolean flag: an explicit =false disables; a bare --json enables.
			// Only consume =value form; a following token is the child command.
			if hasVal {
				switch val {
				case "true", "1", "":
					flags.json = true
				case "false", "0":
					flags.json = false
				default:
					return watchFlags{}, nil, fmt.Errorf("watch: --json takes true/false, got %q", val)
				}
			} else {
				flags.json = true
			}
		case "--json-fd", "-json-fd":
			if !hasVal {
				if i+1 >= len(args) {
					return watchFlags{}, nil, fmt.Errorf("watch: --json-fd needs a value (2 for stderr)")
				}
				val = args[i+1]
				i++
			}
			fd, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return watchFlags{}, nil, fmt.Errorf("watch: --json-fd %q is not a number", val)
			}
			flags.jsonFD = &fd
			flags.json = true // supplying a fd implies enabling the stream
		default:
			return watchFlags{}, nil, fmt.Errorf("watch: unknown flag %q (did you forget \"--\" before the command?)", arg)
		}
		i++
	}
	return flags, args[i:], nil
}

// detectorConfig builds the detect.Config for the watch loop from the resolved
// config and an optional preset name. Precedence for the busy threshold is:
// an explicit busy_threshold in config > the preset's suggested threshold > the
// built-in default. Keyword hints from the preset are merged on top of
// detect.DefaultKeywords so generic spinner/thinking detection still applies.
// A blank preset name yields the plain config-driven behavior (unchanged).
func detectorConfig(cfg config.Config, presetName string) (detect.Config, error) {
	dc := detect.Config{BusyThreshold: cfg.BusyThreshold}
	if presetName == "" {
		return dc, nil
	}
	p, ok := preset.Lookup(presetName)
	if !ok {
		// parseWatchFlags already validated, but stay defensive.
		return detect.Config{}, preset.ErrorFor(presetName)
	}
	// Threshold: config wins only if the user set it explicitly; otherwise the
	// preset's tuned value replaces the default.
	if !cfg.BusyThresholdSet && p.BusyThreshold > 0 {
		dc.BusyThreshold = p.BusyThreshold
	}
	// Keywords: augment the detector defaults with the agent-specific hints.
	dc.Keywords = p.MergeKeywords(detect.DefaultKeywords)
	return dc, nil
}

// indexByte returns the index of the first b in s, or -1. A tiny local helper so
// flag parsing doesn't pull in the strings package for one call.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// joinNames renders the valid preset names for a usage message. Kept here so the
// message stays consistent with preset.Names without importing strings.
func joinNames() string {
	names := preset.Names()
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// newCardRenderer builds the card.Renderer for the configured deck, writing to
// stderr. Three paths:
//
//   - deck = "srs": load the user's flashcards from cfg.SRS.Source (Markdown
//     Q/A or Anki export) and render them in reveal mode (question first, then
//     the answer after cfg.SRS.Reveal) with recently-shown cards spaced out.
//   - deck = "duckdiff": generate one review question from the staged git diff
//     via a local Ollama model, falling back to the static "duck" deck when
//     there's no repo, nothing staged, or Ollama is unavailable/slow. The model
//     call is time-boxed here at startup and never blocks the watch loop.
//   - any other deck: resolve a user deck under ~/.idle-hands/decks over a
//     built-in of the same name (matching `deck` preview), so a user's own deck
//     actually drives the cards.
//
// If the deck can't be loaded it returns nil, and handleState falls back to
// plain one-line notices so watch still works.
func newCardRenderer(cfg config.Config, em *events.Emitter) *card.Renderer {
	onShow := func(deck, title string) { em.CardShown(deck, title) }
	onDismiss := func() { em.CardDismissed() }
	if cfg.Deck == srs.DeckName {
		d, err := srs.LoadDeck(cfg.SRS.Source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "idle-hands: flashcard deck unavailable (%v); using plain notices\n", err)
			return nil
		}
		return card.NewRenderer(os.Stderr, card.Options{
			Deck:      d,
			Reveal:    cfg.SRS.Reveal,
			Spacing:   cfg.SRS.Spacing,
			OnShow:    onShow,
			OnDismiss: onDismiss,
		})
	}

	if cfg.Deck == duckdiff.DeckName {
		res, err := duckdiff.LoadDeck(duckdiff.Options{
			Model:   cfg.DuckDiff.Model,
			URL:     cfg.DuckDiff.URL,
			Timeout: cfg.DuckDiff.Timeout,
		})
		if err != nil {
			// Even the static fallback deck failed to load (a build-time bug);
			// degrade to plain notices rather than refuse to wrap the agent.
			fmt.Fprintf(os.Stderr, "idle-hands: duckdiff deck unavailable (%v); using plain notices\n", err)
			return nil
		}
		if !res.Live {
			// Not an error: no repo / nothing staged / Ollama down. Say which,
			// once, so the static-duck fallback isn't a silent surprise.
			fmt.Fprintf(os.Stderr, "idle-hands: duckdiff → %s; showing the static duck deck\n", res.Reason)
		}
		return card.NewRenderer(os.Stderr, card.Options{Deck: res.Deck, OnShow: onShow, OnDismiss: onDismiss})
	}

	if cfg.Deck == hook.DeckName {
		hd, err := hook.LoadDeck(hook.Options{
			Specs:   cfg.Hooks.Specs,
			Timeout: cfg.Hooks.Timeout,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "idle-hands: hook deck unavailable (%v); using plain notices\n", err)
			return nil
		}
		// The hook deck has no static cards; render a one-card placeholder while
		// the hook runs, then swap in the result via the async producer.
		placeholder := deck.Deck{
			Name:  hook.DeckName,
			Emoji: hd.Emoji(),
			Cards: []deck.Card{{Title: "running a hook…", Text: "doing real work while you wait"}},
		}
		return card.NewRenderer(os.Stderr, card.Options{
			Deck: placeholder,
			Async: func(ctx context.Context) (deck.Card, bool) {
				res := hd.Run(ctx)
				if res.Cancelled {
					return deck.Card{}, false
				}
				return res.Card, true
			},
			OnShow:    onShow,
			OnDismiss: onDismiss,
		})
	}

	userDir, err := config.DecksDir()
	if err != nil {
		// Can't locate the home dir; user decks are unavailable but built-ins
		// still are. Resolve against an empty dir (built-ins only).
		userDir = ""
	}
	d, _, err := deck.Resolve(cfg.Deck, userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-hands: deck %q unavailable (%v); using plain notices\n", cfg.Deck, err)
		return nil
	}
	return card.NewRenderer(os.Stderr, card.Options{Deck: d, OnShow: onShow, OnDismiss: onDismiss})
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
		// The agent went quiet: emit the state event regardless of whether the
		// on-screen card is suppressed (quiet hours / focus). External tooling
		// cares about the transition even when we hush the TUI card.
		env.events.Busy()
		// Focus-safe mode: while a focus block is active, hush the card even
		// outside quiet hours. The window is still recorded on IDLE unless
		// focus_safe.suppress_stats opts out.
		if env.focusActiveNow() {
			env.focusActive = true
			env.suppressed = true // keep IDLE equally silent on screen
			return
		}
		env.focusActive = false
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
		// The agent returned: emit the idle state event with the reclaimed span.
		env.events.Idle(ev.IdleFor)
		// Record the just-ended window regardless of whether a card was shown,
		// so quiet-hours windows still count toward reclaimed time. The one
		// exception is a focus-block window when focus_safe.suppress_stats is
		// set: that window is excluded from the tally entirely.
		record := env.store != nil
		if env.focusActive && env.focusSuppressStats {
			record = false
		}
		if record {
			if err := env.store.Record(ev.IdleFor); err != nil {
				fmt.Fprintf(os.Stderr, "idle-hands: could not record stats (%v)\n", err)
			}
		}
		env.focusActive = false
		// If this window's card was suppressed (quiet hours or focus), the
		// screen never changed, so say nothing now either.
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

// focusActiveNow reports whether a focus block is currently active. A missing
// or unreadable focus file is treated as "no focus" so a transient read error
// never wrongly hushes cards; the read happens per BUSY transition so a focus
// block set mid-session takes effect on the next window without a restart.
func (env *watchEnv) focusActiveNow() bool {
	if env.focus == nil {
		return false
	}
	state, err := env.focus.Get()
	if err != nil {
		return false
	}
	return state.Active(env.now())
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
