// Package signal implements "plugin signals" (issue #23): a tiny, local-only
// endpoint that lets any tool — an editor extension, a web UI, a CI runner, a
// wrapper script — POST authoritative busy/idle events and drive the exact same
// card/stats pipeline that the wrapped-command and standalone-process watchers
// use.
//
// The transport is a user-owned Unix domain socket at ~/.idle-hands/signal.sock
// (0600), or a localhost-TCP fallback on platforms without Unix sockets (see
// listen_*.go). The wire protocol is one JSON object per line:
//
//	{"state":"busy","source":"vscode"}
//	{"state":"idle","source":"vscode"}
//
// Unlike the quiet-timeout detector, an external signal is *authoritative*:
// "busy" opens a BUSY window immediately (subject to the caller's quiet-hours
// handling) and "idle" closes it, recording the reclaimed span. This package
// owns only the pure, portable pieces — event parsing and the idempotent
// busy/idle coordinator that turns a stream of signals into detect.Events. The
// socket wiring and the CLI live alongside it so this core stays trivially
// testable without any I/O.
package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rwrife/idle-hands/internal/detect"
)

// SocketName is the fixed filename of the plugin-signal socket inside the
// idle-hands state directory (~/.idle-hands/signal.sock).
const SocketName = "signal.sock"

// State is the authoritative agent state a client asserts.
type State string

const (
	// Busy means the agent started thinking: open a BUSY window if one isn't
	// already open.
	Busy State = "busy"
	// Idle means the agent finished: close the open BUSY window and record the
	// reclaimed span.
	Idle State = "idle"
)

// Event is one decoded signal from a client. Source is optional, free-form, and
// used only for logging (so a leftover "busy" from a crashed client can be
// traced); it never affects the coordinator.
type Event struct {
	State  State  `json:"state"`
	Source string `json:"source,omitempty"`
}

// ErrEmpty is returned by ParseEvent for a blank line so callers can skip
// keep-alive newlines without treating them as protocol errors.
var ErrEmpty = errors.New("signal: empty line")

// ParseEvent decodes one line of the wire protocol. It accepts a JSON object
// ({"state":"busy","source":"x"}) or, as a convenience for shell clients, a
// bare word ("busy" / "idle"). The state is validated so a typo is a clear
// error rather than a silently-dropped event. A blank line yields ErrEmpty.
func ParseEvent(line []byte) (Event, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return Event{}, ErrEmpty
	}

	var ev Event
	if trimmed[0] == '{' {
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
			return Event{}, fmt.Errorf("signal: bad JSON %q: %w", trimmed, err)
		}
	} else {
		// Bare-word convenience form for `idle-hands signal busy|idle` scripts.
		ev.State = State(strings.ToLower(trimmed))
	}

	switch ev.State {
	case Busy, Idle:
		return ev, nil
	case "":
		return Event{}, fmt.Errorf("signal: missing state (want \"busy\" or \"idle\")")
	default:
		return Event{}, fmt.Errorf("signal: unknown state %q (want \"busy\" or \"idle\")", ev.State)
	}
}

// Encode renders an event as one wire line (JSON + newline), which the CLI
// client writes to the socket. Keeping encode/decode together guarantees the
// client and server never drift.
func Encode(ev Event) []byte {
	b, _ := json.Marshal(ev) // Event has no un-marshalable fields
	return append(b, '\n')
}

// Coordinator turns an idempotent stream of busy/idle signals into detector
// transition events, so the signal listener can reuse the shared card/stats
// handler unchanged. It is the "state layer" the issue asks external events to
// feed: a busy while already busy (or an idle while already idle) is a no-op,
// which is what makes duplicate events safe (no double-count, no card flicker).
//
// It is not safe for concurrent use; drive it from the single goroutine that
// reads the socket.
type Coordinator struct {
	now   func() time.Time
	busy  bool
	since time.Time // when the current BUSY window opened
}

// NewCoordinator builds a coordinator starting in the IDLE state. now supplies
// the clock (nil selects time.Now) so tests can assert exact reclaimed spans.
func NewCoordinator(now func() time.Time) *Coordinator {
	if now == nil {
		now = time.Now
	}
	return &Coordinator{now: now}
}

// Busy reports whether a BUSY window is currently open.
func (c *Coordinator) Busy() bool { return c.busy }

// Apply folds one event into the coordinator. It returns a detect.Event and
// true only when the state actually changed (idle→busy or busy→idle); duplicate
// or same-state events return (zero, false) so the caller does nothing. The
// emitted event mirrors what the quiet-timeout detector produces: a Busy
// transition carries IdleFor == 0 (the window just opened, authoritatively),
// and an Idle transition carries IdleFor == the reclaimed span, so handleState
// records stats and renders "reclaimed Ns" identically to the other modes.
func (c *Coordinator) Apply(ev Event) (detect.Event, bool) {
	now := c.now()
	switch ev.State {
	case Busy:
		if c.busy {
			return detect.Event{}, false // idempotent: already busy
		}
		c.busy = true
		c.since = now
		return detect.Event{State: detect.StateBusy, At: now}, true
	case Idle:
		if !c.busy {
			return detect.Event{}, false // idempotent: already idle
		}
		c.busy = false
		reclaimed := now.Sub(c.since)
		return detect.Event{State: detect.StateIdle, At: now, IdleFor: reclaimed}, true
	default:
		return detect.Event{}, false
	}
}
