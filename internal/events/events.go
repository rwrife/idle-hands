// Package events implements the optional newline-delimited JSON (ndjson) event
// stream idle-hands can emit during a `watch` session so external tooling
// (status bars, tmux, dashboards, the plugin-signals socket consumers) can
// react to BUSY/IDLE transitions and card show/dismiss without scraping the
// TUI.
//
// The stream is opt-in: an Emitter is only constructed when the user passes
// --json (or sets the config key). When disabled the watch loop holds a nil
// *Emitter, and every method here is a safe no-op on nil, so callers never need
// to branch. Events are written to a caller-chosen io.Writer (stderr by default
// so the wrapped agent's stdout/PTY stays untouched) as one compact JSON object
// per line. Timestamps are RFC3339 in UTC.
//
// Event schema (one object per line):
//
//	{"ts":"2026-07-12T21:00:00Z","event":"state","state":"busy"}
//	{"ts":"...","event":"card_shown","deck":"move","title":"Stand up"}
//	{"ts":"...","event":"card_dismissed"}
//	{"ts":"...","event":"state","state":"idle","reclaimed_seconds":42}
package events

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Emitter serializes idle-hands events as ndjson to an underlying writer. It is
// safe for concurrent use (the card renderer fires show/dismiss from its own
// goroutines while the watch loop emits state changes). A nil *Emitter is a
// valid "disabled" emitter: every method is a no-op, so the watch loop can hold
// one unconditionally.
type Emitter struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time // injectable clock for deterministic tests
}

// event is the wire shape shared by every emitted line. Fields are omitted when
// empty so each event only carries what it needs (e.g. a card_dismissed line is
// just ts + event).
type event struct {
	TS               string `json:"ts"`
	Event            string `json:"event"`
	State            string `json:"state,omitempty"`
	ReclaimedSeconds *int64 `json:"reclaimed_seconds,omitempty"`
	Deck             string `json:"deck,omitempty"`
	Title            string `json:"title,omitempty"`
}

// New builds an Emitter writing to w. Pass nil to get a disabled emitter (all
// methods no-op); this lets the watch loop construct one unconditionally and
// only supply a writer when --json is set.
func New(w io.Writer) *Emitter {
	if w == nil {
		return nil
	}
	return &Emitter{w: w, now: time.Now}
}

// withClock overrides the emitter's clock. Used by tests to get stable
// timestamps. Returns the receiver for chaining.
func (e *Emitter) withClock(now func() time.Time) *Emitter {
	if e == nil {
		return nil
	}
	e.now = now
	return e
}

// Busy emits a {"event":"state","state":"busy"} line. No-op on a nil Emitter.
func (e *Emitter) Busy() {
	if e == nil {
		return
	}
	e.emit(event{Event: "state", State: "busy"})
}

// Idle emits a {"event":"state","state":"idle","reclaimed_seconds":N} line,
// where N is the just-ended BUSY window rounded to whole seconds. No-op on nil.
func (e *Emitter) Idle(reclaimed time.Duration) {
	if e == nil {
		return
	}
	secs := int64(reclaimed.Round(time.Second) / time.Second)
	e.emit(event{Event: "state", State: "idle", ReclaimedSeconds: &secs})
}

// CardShown emits a {"event":"card_shown","deck":...,"title":...} line. No-op
// on a nil Emitter. deck or title may be empty (omitted from the line).
func (e *Emitter) CardShown(deck, title string) {
	if e == nil {
		return
	}
	e.emit(event{Event: "card_shown", Deck: deck, Title: title})
}

// CardDismissed emits a {"event":"card_dismissed"} line. No-op on nil.
func (e *Emitter) CardDismissed() {
	if e == nil {
		return
	}
	e.emit(event{Event: "card_dismissed"})
}

// emit stamps and writes one event line under the lock. A serialization or
// write error is intentionally swallowed: the event stream is a best-effort
// side channel and must never disrupt the wrapped agent or crash the watch.
func (e *Emitter) emit(ev event) {
	ev.TS = e.now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(e.w, "%s\n", b)
}
