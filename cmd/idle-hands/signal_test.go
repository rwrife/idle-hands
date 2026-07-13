package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	sig "github.com/rwrife/idle-hands/internal/signal"
	"github.com/rwrife/idle-hands/internal/store"
)

// TestCmdSignalRejectsBadArgs covers the client-mode argument validation.
func TestCmdSignalRejectsBadArgs(t *testing.T) {
	cases := [][]string{
		{"wat"},
		{"busy", "extra"},
	}
	for _, args := range cases {
		if code, err := cmdSignal(args); err == nil || code != 2 {
			t.Fatalf("cmdSignal(%v) = (%d, %v), want (2, error)", args, code, err)
		}
	}
}

// TestSignalCoordinatorDrivesCardAndStats proves plugin signals reuse the exact
// same card + stats pipeline as the watchers: a busy event renders a card via
// handleState, and the following idle event clears it and records the reclaimed
// window in the stats store.
func TestSignalCoordinatorDrivesCardAndStats(t *testing.T) {
	d, err := deck.Builtin("move")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := newBufRenderer(&buf, d)
	day := time.Date(2026, 7, 4, 12, 0, 0, 0, time.Local)
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.New(store.Options{Path: path, Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	env := &watchEnv{renderer: r, store: st, quiet: config.QuietHours{}, now: func() time.Time { return day }}

	// A clock that advances 45s between the busy and idle reads.
	times := []time.Time{day, day.Add(45 * time.Second)}
	i := 0
	coord := sig.NewCoordinator(func() time.Time {
		t := times[i]
		if i < len(times)-1 {
			i++
		}
		return t
	})

	// busy → card rendered.
	if ev, changed := coord.Apply(sig.Event{State: sig.Busy}); changed {
		handleState(ev, env)
	}
	if buf.Len() == 0 {
		t.Fatal("expected a card on busy")
	}

	// Duplicate busy is idempotent (no dispatch).
	if _, changed := coord.Apply(sig.Event{State: sig.Busy}); changed {
		t.Fatal("duplicate busy should not change state")
	}

	// idle → window recorded.
	if ev, changed := coord.Apply(sig.Event{State: sig.Idle}); changed {
		handleState(ev, env)
	}
	today, err := st.Today()
	if err != nil {
		t.Fatal(err)
	}
	if today.Windows != 1 {
		t.Fatalf("recorded windows = %d, want 1", today.Windows)
	}
}
