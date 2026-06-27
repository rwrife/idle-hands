// Package detect implements the BUSY/IDLE state machine that decides when the
// wrapped agent is "thinking" (a window you can reclaim) versus actively
// streaming work back to you.
//
// The brain is deliberately simple and, above all, *non-flappy*. It is fed two
// things:
//
//   - chunks of the child's output (via Feed), as tapped by internal/wrap, and
//   - the passage of time (via Tick), so it can notice a sustained quiet gap.
//
// Core rule (per PLAN.md / M3):
//
//	fresh real output → IDLE (you're up; the agent is producing results)
//	quiet for ≥ threshold → BUSY (the agent is thinking; here's your window)
//
// The subtlety is "fresh *real* output". Many agents (and our own
// scripts/fake-agent.sh) keep redrawing a spinner — "thinking |", a braille
// frame, a bare carriage-return repaint — *while* they think. Those bytes are
// not progress; if they reset the idle timer the detector would never enter
// BUSY and the whole tool would be pointless. So Feed classifies each chunk:
// carriage-return-only repaints, lone spinner frames, and known "thinking"
// keywords are treated as quiet (they keep, or even hasten, BUSY); anything
// else counts as real activity and snaps the state back to IDLE.
//
// The Detector takes its time source as a function so tests can drive it with
// a synthetic clock and exact timelines instead of real sleeps.
package detect

import (
	"strings"
	"time"
)

// State is the detector's view of the wrapped agent.
type State int

const (
	// StateIdle means the agent is producing real output (or has only just
	// started). From the human's point of view: *you're up* — either nothing
	// to wait on, or the results are streaming in.
	StateIdle State = iota
	// StateBusy means output has been quiet (ignoring spinner/thinking noise)
	// for at least the busy threshold: the agent is thinking and this is an
	// idle window worth reclaiming.
	StateBusy
)

// String renders the state for logs and tests.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateBusy:
		return "BUSY"
	default:
		return "UNKNOWN"
	}
}

// DefaultBusyThreshold is how long output must stay quiet before the detector
// declares BUSY. PLAN.md specifies a 20s default.
const DefaultBusyThreshold = 20 * time.Second

// Event is emitted on every state transition (and never otherwise — short
// pauses below the threshold produce no events, which is what keeps the UI
// from flapping).
type Event struct {
	// State is the state just entered.
	State State
	// At is the time of the transition, taken from the detector's clock.
	At time.Time
	// IdleFor is, for a BUSY transition, how long output had been quiet when
	// BUSY fired (i.e. the busy threshold). For an IDLE transition it is how
	// long the just-ended BUSY window lasted. It is a convenience for callers
	// rendering "reclaimed Ns" without tracking timestamps themselves.
	IdleFor time.Duration
}

// Config tunes the detector.
type Config struct {
	// BusyThreshold is the quiet duration that triggers BUSY. Zero or negative
	// selects DefaultBusyThreshold.
	BusyThreshold time.Duration

	// Now returns the current time. Zero value selects time.Now. Tests inject
	// a controllable clock here.
	Now func() time.Time

	// Keywords are lowercase substrings that mark a chunk as "thinking" noise
	// rather than real progress (e.g. "thinking", "working"). A nil slice
	// selects DefaultKeywords. Matching is case-insensitive.
	Keywords []string

	// DisableSpinnerHeuristic turns off the built-in detection of spinner
	// frames / carriage-return repaints as noise. When true, *any* non-empty
	// output counts as real activity (pure quiet-timeout behavior). Off by
	// default because most agent TUIs animate while they think.
	DisableSpinnerHeuristic bool
}

// DefaultKeywords are substrings commonly emitted by agents while they think.
// They are treated as noise so a chatty "thinking…" spinner cannot keep the
// detector pinned to IDLE forever.
var DefaultKeywords = []string{
	"thinking",
	"working",
	"loading",
	"generating",
	"reasoning",
	"processing",
	"please wait",
}

// spinnerRunes are single glyphs frequently used as spinner frames: the ASCII
// "|/-\" set and the common braille spinner block. A chunk made up solely of
// these (plus whitespace and control repaint characters) is treated as noise.
var spinnerRunes = map[rune]bool{
	'|': true, '/': true, '-': true, '\\': true,
	'⠋': true, '⠙': true, '⠹': true, '⠸': true,
	'⠼': true, '⠴': true, '⠦': true, '⠧': true,
	'⠇': true, '⠏': true,
	'◐': true, '◓': true, '◑': true, '◒': true,
	'•': true, '·': true, '*': true, '✶': true, '✦': true,
}

// Detector is the BUSY/IDLE state machine. It is not safe for concurrent use;
// drive it from a single goroutine (typically the one draining the wrap tap,
// plus a ticker on the same select loop).
type Detector struct {
	threshold time.Duration
	now       func() time.Time
	keywords  []string
	useSpin   bool

	state    State
	lastReal time.Time // time of the last real-progress output
	changed  time.Time // time of the last state transition
}

// New builds a Detector from cfg and starts it in IDLE, anchored at the current
// time so the first quiet window is measured from construction.
func New(cfg Config) *Detector {
	threshold := cfg.BusyThreshold
	if threshold <= 0 {
		threshold = DefaultBusyThreshold
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	kw := cfg.Keywords
	if kw == nil {
		kw = DefaultKeywords
	}
	start := now()
	return &Detector{
		threshold: threshold,
		now:       now,
		keywords:  kw,
		useSpin:   !cfg.DisableSpinnerHeuristic,
		state:     StateIdle,
		lastReal:  start,
		changed:   start,
	}
}

// State returns the detector's current state.
func (d *Detector) State() State { return d.state }

// Threshold returns the configured busy threshold (after defaulting).
func (d *Detector) Threshold() time.Duration { return d.threshold }

// Feed processes a chunk of child output. If the chunk represents real
// progress it (re)marks activity and, if currently BUSY, returns an IDLE
// transition event. Spinner/thinking-noise chunks are ignored for the purpose
// of resetting the idle timer, so they never drag the detector out of BUSY.
//
// Feed returns (Event, true) when a transition occurred, else (zero, false).
func (d *Detector) Feed(chunk []byte) (Event, bool) {
	if !d.isRealProgress(chunk) {
		return Event{}, false
	}
	now := d.now()
	d.lastReal = now
	if d.state == StateBusy {
		return d.transition(StateIdle, now), true
	}
	return Event{}, false
}

// Tick advances time-based logic to the supplied moment (or, if t is the zero
// value, to the detector's clock). It returns a BUSY transition event when the
// quiet gap has reached the threshold. Call it periodically — e.g. from a
// time.Ticker on the same select loop that drains the tap — so BUSY can fire
// even while the child produces nothing at all.
func (d *Detector) Tick(t time.Time) (Event, bool) {
	if t.IsZero() {
		t = d.now()
	}
	if d.state == StateIdle && t.Sub(d.lastReal) >= d.threshold {
		return d.transition(StateBusy, t), true
	}
	return Event{}, false
}

// transition flips state and builds the event, recording the moment so the
// next transition can report how long the window lasted.
func (d *Detector) transition(to State, at time.Time) Event {
	var idleFor time.Duration
	switch to {
	case StateBusy:
		// We've been quiet for at least the threshold; report that span.
		idleFor = at.Sub(d.lastReal)
	case StateIdle:
		// Report how long the just-ended BUSY window lasted.
		idleFor = at.Sub(d.changed)
	}
	d.state = to
	d.changed = at
	return Event{State: to, At: at, IdleFor: idleFor}
}

// isRealProgress decides whether a chunk counts as the agent producing results
// (true) or merely animating while it thinks (false). Empty chunks are never
// progress. With the spinner heuristic enabled, a chunk is noise when, after
// stripping carriage-return repaints, it is empty, made up solely of spinner
// glyphs/whitespace, or contains a known thinking keyword and no other
// substantive text.
func (d *Detector) isRealProgress(chunk []byte) bool {
	if len(chunk) == 0 {
		return false
	}
	if !d.useSpin {
		// Pure quiet-timeout mode: any byte is activity.
		return true
	}

	s := string(chunk)

	// A spinner repaint is classically "\r  thinking | " with no newline: the
	// line is overwritten in place. Output that advances the screen almost
	// always contains a newline, so treat the presence of a newline as a
	// strong signal of real progress (the agent printed a line of results).
	if strings.ContainsAny(s, "\n") {
		// ...unless the only thing on those lines is a thinking keyword (some
		// agents print "thinking...\n" repeatedly). Fall through to the keyword
		// check using the de-noised text.
		if !d.isKeywordOnly(s) {
			return true
		}
		return false
	}

	// No newline: collapse carriage returns and surrounding whitespace and see
	// what's left.
	trimmed := strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if trimmed == "" {
		return false // bare cursor repaint
	}

	// Keyword-only "thinking" line → noise.
	if d.isKeywordOnly(trimmed) {
		return false
	}

	// Made up entirely of spinner glyphs / separators → noise.
	if isSpinnerOnly(trimmed) {
		return false
	}

	return true
}

// isKeywordOnly reports whether, after removing known thinking keywords and
// spinner/separator glyphs, the (already newline-aware) text has no substantive
// content left. It lets "⠋ thinking…" and "working...\n" be classified as noise
// while "thinking: I will edit main.go" (real, substantive) is not.
func (d *Detector) isKeywordOnly(s string) bool {
	low := strings.ToLower(s)
	hasKeyword := false
	for _, kw := range d.keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(low, kw) {
			hasKeyword = true
			low = strings.ReplaceAll(low, kw, " ")
		}
	}
	if !hasKeyword {
		return false
	}
	// Strip spinner glyphs, common ellipsis/punctuation and whitespace; if
	// nothing meaningful remains, it was just a thinking banner.
	low = strings.Map(func(r rune) rune {
		if spinnerRunes[r] || r == '.' || r == '…' || r == ':' || r == '\r' ||
			r == '(' || r == ')' || r == '%' || (r >= '0' && r <= '9') {
			return -1
		}
		return r
	}, low)
	return strings.TrimSpace(low) == ""
}

// isSpinnerOnly reports whether every rune in s is a spinner glyph or
// whitespace, i.e. the chunk is a bare animated frame with no text.
func isSpinnerOnly(s string) bool {
	sawGlyph := false
	for _, r := range s {
		switch {
		case spinnerRunes[r]:
			sawGlyph = true
		case r == ' ' || r == '\t' || r == '\r':
			// allowed padding
		default:
			return false
		}
	}
	return sawGlyph
}
