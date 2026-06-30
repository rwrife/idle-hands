package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/store"
)

// testEnv builds a watchEnv whose renderer writes to a buffer, with a temp-file
// store on a fixed day and the supplied quiet hours and clock. It returns the
// env and the renderer's output buffer.
func testEnv(t *testing.T, quiet config.QuietHours, now func() time.Time) (*watchEnv, *bytes.Buffer, *store.Store) {
	t.Helper()
	d, err := deck.Builtin("move")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	// Build a renderer over the buffer using the same package the watch path
	// uses; importing internal/card directly keeps this honest.
	r := newBufRenderer(&buf, d)

	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.New(store.Options{Path: path, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return &watchEnv{renderer: r, store: st, quiet: quiet, now: now}, &buf, st
}

func busyEvent(idle time.Duration) detect.Event {
	return detect.Event{State: detect.StateBusy, IdleFor: idle}
}
func idleEvent(span time.Duration) detect.Event {
	return detect.Event{State: detect.StateIdle, IdleFor: span}
}

// TestHandleStateRecordsWindow verifies a normal BUSY→IDLE cycle renders a card
// and records exactly one window with the reclaimed seconds.
func TestHandleStateRecordsWindow(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local) // midday, no quiet
	env, buf, st := testEnv(t, config.QuietHours{}, func() time.Time { return day })

	handleState(busyEvent(20*time.Second), env)
	handleState(idleEvent(75*time.Second), env)

	if buf.Len() == 0 {
		t.Error("expected card output during non-quiet window, got none")
	}
	today, err := st.Today()
	if err != nil {
		t.Fatal(err)
	}
	if today.Windows != 1 {
		t.Errorf("Windows = %d, want 1", today.Windows)
	}
	if today.Seconds != 75 {
		t.Errorf("Seconds = %d, want 75", today.Seconds)
	}
}

// TestHandleStateQuietHoursSuppressesButRecords is the core M5 behavior: during
// quiet hours no card (and no "agent's back" line) is written, yet the reclaimed
// window is still counted.
func TestHandleStateQuietHoursSuppressesButRecords(t *testing.T) {
	// Quiet 22:00→07:00; pin the clock to 02:00 (inside the window).
	q := config.QuietHours{}
	start, _ := config.ParseClock("22:00")
	end, _ := config.ParseClock("07:00")
	q.Start, q.End = start, end
	day := time.Date(2026, 6, 29, 2, 0, 0, 0, time.Local)
	env, buf, st := testEnv(t, q, func() time.Time { return day })

	handleState(busyEvent(20*time.Second), env)
	handleState(idleEvent(40*time.Second), env)

	if buf.Len() != 0 {
		t.Errorf("expected no output during quiet hours, got:\n%q", buf.String())
	}
	today, err := st.Today()
	if err != nil {
		t.Fatal(err)
	}
	if today.Windows != 1 || today.Seconds != 40 {
		t.Errorf("quiet-hours window not recorded: %+v, want {1 40}", today)
	}
}

func TestRunStatsCommandRouting(t *testing.T) {
	// `run(["stats"])` must exit 0 even with no recorded history (it reports
	// the empty-state hint). Capture stdout so the test stays quiet.
	old := stdout
	var buf bytes.Buffer
	stdout = &buf
	defer func() { stdout = old }()

	if code := run([]string{"stats"}); code != 0 {
		t.Fatalf("run(stats) = %d, want 0", code)
	}
}
