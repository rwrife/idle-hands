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
	"github.com/rwrife/idle-hands/internal/procwatch"
	"github.com/rwrife/idle-hands/internal/store"
)

// TestParseWatchFlagsProcess verifies --process is parsed (both spaced and
// =forms), that it's mutually understood alongside no trailing command, and
// that a missing/empty value errors.
func TestParseWatchFlagsProcess(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{"spaced", []string{"--process", "code"}, "code", false},
		{"equals", []string{"--process=code"}, "code", false},
		{"single-dash", []string{"-process", "claude"}, "claude", false},
		{"missing value", []string{"--process"}, "", true},
		{"empty value", []string{"--process="}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, _, err := parseWatchFlags(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseWatchFlags(%v) = nil error, want error", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWatchFlags(%v) = %v", tc.args, err)
			}
			if flags.process != tc.want {
				t.Errorf("process = %q, want %q", flags.process, tc.want)
			}
		})
	}
}

// TestCmdWatchProcessRejectsCommand verifies --process and a wrapped command
// can't be combined (they're different modes).
func TestCmdWatchProcessRejectsCommand(t *testing.T) {
	code, err := cmdWatch([]string{"--process", "code", "--", "echo", "hi"})
	if err == nil || code != 2 {
		t.Fatalf("cmdWatch(--process + cmd) = (%d, %v), want (2, error)", code, err)
	}
}

// scriptedPoller returns a fixed sequence of readings, then blocks forever
// (returns a Quiet with a sentinel so the loop keeps ticking until stopped). It
// implements procLoopPoller for runProcessLoop tests without a real process.
type scriptedPoller struct {
	readings []procwatch.Reading
	i        int
}

func (s *scriptedPoller) Poll() (procwatch.Reading, error) {
	if s.i < len(s.readings) {
		r := s.readings[s.i]
		s.i++
		return r, nil
	}
	return procwatch.Reading{Kind: procwatch.Quiet}, nil
}

// procTestEnv builds a watchEnv with a buffer-backed renderer and a temp store
// on a fixed non-quiet day, mirroring watch_test.go's testEnv.
func procTestEnv(t *testing.T) (*watchEnv, *bytes.Buffer, *store.Store) {
	t.Helper()
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
	return env, &buf, st
}

// TestRunProcessLoopExitStops verifies the loop returns cleanly when the poller
// reports the process Exited.
func TestRunProcessLoopExitStops(t *testing.T) {
	env, _, _ := procTestEnv(t)
	poller := &scriptedPoller{readings: []procwatch.Reading{
		{Kind: procwatch.Quiet},
		{Kind: procwatch.Exited},
	}}
	// Detector with a tiny threshold; we don't assert a card here, only that
	// Exited terminates the loop promptly.
	det := detect.New(detect.Config{BusyThreshold: 10 * time.Millisecond})

	code := runLoopWithTimeout(t, poller, det, env)
	if code != 0 {
		t.Fatalf("runProcessLoop exit code = %d, want 0", code)
	}
}

// TestRunProcessLoopBusyThenIdleRecords drives a quiet stretch (fires BUSY →
// card) followed by Active (clears → records a window), then Exited. It proves
// the standalone loop reuses the same card + stats pipeline as wrapped mode.
func TestRunProcessLoopBusyThenIdleRecords(t *testing.T) {
	env, buf, st := procTestEnv(t)
	// Threshold below the poll interval so a couple of Quiet ticks trip BUSY.
	det := detect.New(detect.Config{BusyThreshold: 1 * time.Millisecond})

	poller := &scriptedPoller{readings: []procwatch.Reading{
		{Kind: procwatch.Quiet}, // ticks accumulate quiet → BUSY fires here-ish
		{Kind: procwatch.Quiet},
		{Kind: procwatch.Active}, // real work → IDLE, record window
		{Kind: procwatch.Exited},
	}}

	code := runLoopWithTimeout(t, poller, det, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if buf.Len() == 0 {
		t.Error("expected a card to be rendered during the BUSY window")
	}
	today, err := st.Today()
	if err != nil {
		t.Fatal(err)
	}
	if today.Windows < 1 {
		t.Errorf("Windows = %d, want >= 1 (BUSY→IDLE should record)", today.Windows)
	}
}

// runLoopWithTimeout runs runProcessLoop on a fast tick with a small real-time
// budget and a stop channel that fires if the loop overruns, so a logic bug
// can't hang the suite. It uses a short interval so the scripted readings and
// detector ticks are consumed quickly.
func runLoopWithTimeout(t *testing.T, poller procLoopPoller, det *detect.Detector, env *watchEnv) int {
	t.Helper()
	stop := make(chan os.Signal, 1)
	type result struct{ code int }
	ch := make(chan result, 1)
	go func() {
		code, _ := runProcessLoop(poller, det, env, 2*time.Millisecond, stop)
		ch <- result{code}
	}()
	select {
	case r := <-ch:
		return r.code
	case <-time.After(3 * time.Second):
		close(stop) // ask it to stop
		select {
		case r := <-ch:
			return r.code
		case <-time.After(1 * time.Second):
			t.Fatal("runProcessLoop did not stop in time")
			return -1
		}
	}
}
