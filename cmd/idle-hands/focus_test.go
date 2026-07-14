package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/focus"
)

func newFocusStore(t *testing.T, now func() time.Time) *focus.Store {
	t.Helper()
	st, err := focus.New(focus.Options{Path: filepath.Join(t.TempDir(), "focus.json"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunFocusSetStatusAndOff(t *testing.T) {
	day := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	now := func() time.Time { return day }
	st := newFocusStore(t, now)

	// Set a 25m block.
	var buf bytes.Buffer
	code, err := runFocus(&buf, st, now, []string{"25m"})
	if err != nil || code != 0 {
		t.Fatalf("set: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "focus-safe mode on") {
		t.Errorf("set output missing confirmation: %q", buf.String())
	}
	got, _ := st.Get()
	if !got.Active(day) {
		t.Error("focus should be active after set")
	}

	// Status with no args, 10m later: ~15m left.
	buf.Reset()
	later := day.Add(10 * time.Minute)
	code, err = runFocus(&buf, st, func() time.Time { return later }, nil)
	if err != nil || code != 0 {
		t.Fatalf("status: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "15 min left") {
		t.Errorf("status output = %q, want 15 min left", buf.String())
	}

	// Off clears it.
	buf.Reset()
	code, err = runFocus(&buf, st, now, []string{"off"})
	if err != nil || code != 0 {
		t.Fatalf("off: code=%d err=%v", code, err)
	}
	if got, _ := st.Get(); got.Active(day) {
		t.Error("focus should be inactive after off")
	}
}

func TestRunFocusStatusWhenOff(t *testing.T) {
	day := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	now := func() time.Time { return day }
	st := newFocusStore(t, now)

	var buf bytes.Buffer
	code, _ := runFocus(&buf, st, now, nil)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "off") {
		t.Errorf("off status = %q, want mention of off", buf.String())
	}
}

func TestRunFocusRejectsBadDuration(t *testing.T) {
	now := time.Now
	st := newFocusStore(t, now)

	var buf bytes.Buffer
	if code, err := runFocus(&buf, st, now, []string{"banana"}); err == nil || code != 2 {
		t.Errorf("bad duration: code=%d err=%v, want code 2 + error", code, err)
	}
	if code, err := runFocus(&buf, st, now, []string{"0s"}); err == nil || code != 2 {
		t.Errorf("zero duration: code=%d err=%v, want code 2 + error", code, err)
	}
	if code, err := runFocus(&buf, st, now, []string{"a", "b"}); err == nil || code != 2 {
		t.Errorf("too many args: code=%d err=%v, want code 2 + error", code, err)
	}
}

func TestFocusCommandRouting(t *testing.T) {
	// `idle-hands focus` with no args against the real default store should not
	// panic and should return 0 (off status). It uses the real home dir but
	// only reads, so it is safe.
	if code := run([]string{"focus"}); code != 0 {
		t.Errorf("run(focus) = %d, want 0", code)
	}
}
