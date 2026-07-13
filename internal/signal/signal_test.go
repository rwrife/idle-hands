package signal

import (
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/detect"
)

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    Event
		wantErr bool
		empty   bool
	}{
		{name: "json busy", line: `{"state":"busy","source":"vscode"}`, want: Event{State: Busy, Source: "vscode"}},
		{name: "json idle", line: `{"state":"idle"}`, want: Event{State: Idle}},
		{name: "bare busy", line: "busy", want: Event{State: Busy}},
		{name: "bare idle upper", line: "IDLE", want: Event{State: Idle}},
		{name: "whitespace padded", line: "  busy  ", want: Event{State: Busy}},
		{name: "blank", line: "   ", empty: true},
		{name: "unknown state", line: `{"state":"sleepy"}`, wantErr: true},
		{name: "bare unknown", line: "wat", wantErr: true},
		{name: "missing state", line: `{"source":"x"}`, wantErr: true},
		{name: "bad json", line: `{"state":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEvent([]byte(tt.line))
			if tt.empty {
				if err != ErrEmpty {
					t.Fatalf("want ErrEmpty, got %v", err)
				}
				return
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	in := Event{State: Busy, Source: "cli"}
	out, err := ParseEvent(Encode(in))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
	// Encode must be newline-terminated so the server's line scanner frames it.
	enc := Encode(in)
	if enc[len(enc)-1] != '\n' {
		t.Fatalf("Encode not newline-terminated: %q", enc)
	}
}

// fakeClock returns times from a scripted list, advancing on each call.
type fakeClock struct {
	times []time.Time
	i     int
}

func (f *fakeClock) now() time.Time {
	t := f.times[f.i]
	if f.i < len(f.times)-1 {
		f.i++
	}
	return t
}

func TestCoordinatorBusyThenIdle(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{times: []time.Time{base, base.Add(90 * time.Second)}}
	c := NewCoordinator(clk.now)

	// busy → StateBusy transition, window just opened (IdleFor 0).
	ev, changed := c.Apply(Event{State: Busy})
	if !changed {
		t.Fatal("busy should change state")
	}
	if ev.State != detect.StateBusy {
		t.Fatalf("want StateBusy, got %v", ev.State)
	}
	if ev.IdleFor != 0 {
		t.Fatalf("busy IdleFor want 0, got %v", ev.IdleFor)
	}
	if !c.Busy() {
		t.Fatal("coordinator should be busy")
	}

	// idle → StateIdle transition carrying the reclaimed span.
	ev, changed = c.Apply(Event{State: Idle})
	if !changed {
		t.Fatal("idle should change state")
	}
	if ev.State != detect.StateIdle {
		t.Fatalf("want StateIdle, got %v", ev.State)
	}
	if ev.IdleFor != 90*time.Second {
		t.Fatalf("reclaimed want 90s, got %v", ev.IdleFor)
	}
	if c.Busy() {
		t.Fatal("coordinator should be idle again")
	}
}

func TestCoordinatorIdempotentDuplicates(t *testing.T) {
	c := NewCoordinator(time.Now)

	if _, changed := c.Apply(Event{State: Busy}); !changed {
		t.Fatal("first busy should change")
	}
	// Duplicate busy: no transition, no flicker.
	if _, changed := c.Apply(Event{State: Busy}); changed {
		t.Fatal("duplicate busy must be a no-op")
	}
	if _, changed := c.Apply(Event{State: Idle}); !changed {
		t.Fatal("first idle should change")
	}
	// Duplicate idle: no transition, no double-count.
	if _, changed := c.Apply(Event{State: Idle}); changed {
		t.Fatal("duplicate idle must be a no-op")
	}
}
