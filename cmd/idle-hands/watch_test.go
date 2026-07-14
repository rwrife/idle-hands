package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/focus"
	"github.com/rwrife/idle-hands/internal/store"
)

// osGetwd/osChdir are thin aliases so the cwd-swapping test reads clearly and
// the os import has a single obvious use here.
var (
	osGetwd = os.Getwd
	osChdir = os.Chdir
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

// TestHandleStateFocusSuppressesButRecords verifies an active focus block hushes
// the card (like quiet hours) while still recording the reclaimed window by
// default (focus_safe.suppress_stats unset).
func TestHandleStateFocusSuppressesButRecords(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local) // midday, no quiet
	now := func() time.Time { return day }
	env, buf, st := testEnv(t, config.QuietHours{}, now)

	// Attach an active focus block (ends an hour from now).
	fpath := filepath.Join(t.TempDir(), "focus.json")
	fs, err := focus.New(focus.Options{Path: fpath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Set(time.Hour); err != nil {
		t.Fatal(err)
	}
	env.focus = fs

	handleState(busyEvent(20*time.Second), env)
	handleState(idleEvent(50*time.Second), env)

	if buf.Len() != 0 {
		t.Errorf("expected no card output during focus block, got:\n%q", buf.String())
	}
	today, err := st.Today()
	if err != nil {
		t.Fatal(err)
	}
	if today.Windows != 1 || today.Seconds != 50 {
		t.Errorf("focus window not recorded: %+v, want {1 50}", today)
	}
}

// TestHandleStateFocusSuppressStatsExcludesWindow verifies that with
// focus_safe.suppress_stats set, a focus-block window is neither shown nor
// counted, while an expired focus block leaves normal behavior intact.
func TestHandleStateFocusSuppressStatsExcludesWindow(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local)
	now := func() time.Time { return day }
	env, buf, st := testEnv(t, config.QuietHours{}, now)
	env.focusSuppressStats = true

	fpath := filepath.Join(t.TempDir(), "focus.json")
	fs, _ := focus.New(focus.Options{Path: fpath, Now: now})
	if _, err := fs.Set(30 * time.Minute); err != nil {
		t.Fatal(err)
	}
	env.focus = fs

	handleState(busyEvent(20*time.Second), env)
	handleState(idleEvent(50*time.Second), env)

	if buf.Len() != 0 {
		t.Errorf("expected no card output during focus block, got:\n%q", buf.String())
	}
	today, _ := st.Today()
	if today.Windows != 0 || today.Seconds != 0 {
		t.Errorf("suppress_stats focus window should not record: %+v, want {0 0}", today)
	}

	// Clear focus: a subsequent window renders and records normally.
	if err := fs.Clear(); err != nil {
		t.Fatal(err)
	}
	handleState(busyEvent(20*time.Second), env)
	handleState(idleEvent(40*time.Second), env)
	if buf.Len() == 0 {
		t.Error("expected card output after focus cleared, got none")
	}
	today, _ = st.Today()
	if today.Windows != 1 || today.Seconds != 40 {
		t.Errorf("post-focus window record = %+v, want {1 40}", today)
	}
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

// TestNewCardRendererDuckDiffFallsBack verifies the duckdiff deck wiring: run
// from a non-git temp dir so LoadDeck hits the "not a git repo" path, falls
// back to the static duck deck, and still returns a usable renderer (never nil).
// This exercises the watch integration without needing a repo or a live Ollama.
func TestNewCardRendererDuckDiffFallsBack(t *testing.T) {
	dir := t.TempDir()
	old, err := osGetwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := osChdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = osChdir(old) })

	cfg := config.Default()
	cfg.Deck = "duckdiff"
	// A short timeout keeps the test fast even if a stray Ollama is reachable;
	// the non-git dir means we shouldn't reach the model at all.
	cfg.DuckDiff.Timeout = 200 * time.Millisecond

	r := newCardRenderer(cfg, nil)
	if r == nil {
		t.Fatal("newCardRenderer(duckdiff) = nil, want a fallback renderer")
	}
}
