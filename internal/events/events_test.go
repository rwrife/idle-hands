package events

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a clock that always reports ts.
func fixedClock(ts time.Time) func() time.Time {
	return func() time.Time { return ts }
}

// decodeLines parses the emitter output into a slice of generic maps, one per
// ndjson line, failing the test on any malformed line.
func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		evs = append(evs, m)
	}
	return evs
}

func TestEmitterOrderingAndPayloads(t *testing.T) {
	var buf bytes.Buffer
	ts := time.Date(2026, 7, 12, 21, 0, 0, 0, time.UTC)
	em := New(&buf).withClock(fixedClock(ts))

	// A full busy→card→idle cycle.
	em.Busy()
	em.CardShown("move", "Stand up & stretch")
	em.CardDismissed()
	em.Idle(42 * time.Second)

	evs := decodeLines(t, buf.String())
	if len(evs) != 4 {
		t.Fatalf("got %d events, want 4: %q", len(evs), buf.String())
	}

	// Every event carries an RFC3339 UTC timestamp.
	for i, ev := range evs {
		got, _ := ev["ts"].(string)
		if got != "2026-07-12T21:00:00Z" {
			t.Errorf("event %d ts = %q, want RFC3339 UTC", i, got)
		}
	}

	if evs[0]["event"] != "state" || evs[0]["state"] != "busy" {
		t.Errorf("event 0 = %v, want state/busy", evs[0])
	}
	if evs[1]["event"] != "card_shown" || evs[1]["deck"] != "move" || evs[1]["title"] != "Stand up & stretch" {
		t.Errorf("event 1 = %v, want card_shown move/title", evs[1])
	}
	if evs[2]["event"] != "card_dismissed" {
		t.Errorf("event 2 = %v, want card_dismissed", evs[2])
	}
	if _, hasDeck := evs[2]["deck"]; hasDeck {
		t.Errorf("card_dismissed should omit deck: %v", evs[2])
	}
	if evs[3]["event"] != "state" || evs[3]["state"] != "idle" {
		t.Errorf("event 3 = %v, want state/idle", evs[3])
	}
	if got := evs[3]["reclaimed_seconds"]; got != float64(42) {
		t.Errorf("event 3 reclaimed_seconds = %v, want 42", got)
	}
}

func TestIdleRoundsToSeconds(t *testing.T) {
	var buf bytes.Buffer
	em := New(&buf)
	em.Idle(2*time.Second + 600*time.Millisecond) // rounds to 3
	evs := decodeLines(t, buf.String())
	if got := evs[0]["reclaimed_seconds"]; got != float64(3) {
		t.Errorf("reclaimed_seconds = %v, want 3", got)
	}
}

func TestNilEmitterIsNoop(t *testing.T) {
	// A nil *Emitter (the "disabled" case) must be safe on every method.
	var em *Emitter
	em.Busy()
	em.Idle(time.Second)
	em.CardShown("move", "x")
	em.CardDismissed()

	// New(nil) also yields a nil, no-op emitter.
	if got := New(nil); got != nil {
		t.Errorf("New(nil) = %v, want nil (disabled)", got)
	}
}

func TestCardShownOmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	em := New(&buf)
	em.CardShown("", "")
	line := strings.TrimSpace(buf.String())
	if strings.Contains(line, "deck") || strings.Contains(line, "title") {
		t.Errorf("empty card fields should be omitted, got %q", line)
	}
}
