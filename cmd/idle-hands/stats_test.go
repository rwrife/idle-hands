package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/store"
)

// newTestStore builds a Store over a temp file pinned to a fixed day.
func newTestStore(t *testing.T, day time.Time) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.New(store.Options{Path: path, Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunStatsEmpty(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local)
	st := newTestStore(t, day)
	var buf bytes.Buffer
	code, err := runStats(&buf, st, func() time.Time { return day })
	if err != nil {
		t.Fatalf("runStats: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "no reclaimed idle windows yet today") {
		t.Errorf("empty output missing hint, got:\n%s", buf.String())
	}
}

func TestRunStatsTodayOnly(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local)
	st := newTestStore(t, day)
	// 3 windows totaling 372s ≈ 6 min.
	for _, d := range []time.Duration{200 * time.Second, 100 * time.Second, 72 * time.Second} {
		if err := st.Record(d); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := runStats(&buf, st, func() time.Time { return day }); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "6 min") {
		t.Errorf("output missing '6 min', got:\n%s", out)
	}
	if !strings.Contains(out, "3 waits") {
		t.Errorf("output missing '3 waits', got:\n%s", out)
	}
	// Only one day of data → no all-time line.
	if strings.Contains(out, "All-time") {
		t.Errorf("unexpected All-time line for single-day data:\n%s", out)
	}
}

func TestRunStatsWithHistoryShowsAllTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	yesterday := time.Date(2026, 6, 28, 12, 0, 0, 0, time.Local)
	today := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local)

	stYesterday, _ := store.New(store.Options{Path: path, Now: func() time.Time { return yesterday }})
	if err := stYesterday.Record(300 * time.Second); err != nil {
		t.Fatal(err)
	}
	stToday, _ := store.New(store.Options{Path: path, Now: func() time.Time { return today }})
	if err := stToday.Record(120 * time.Second); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := runStats(&buf, stToday, func() time.Time { return today }); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 min") {
		t.Errorf("today line should show 2 min, got:\n%s", out)
	}
	if !strings.Contains(out, "All-time") {
		t.Errorf("expected All-time line with history, got:\n%s", out)
	}
	// All-time is 420s = 7 min.
	if !strings.Contains(out, "7 min") {
		t.Errorf("All-time should show 7 min, got:\n%s", out)
	}
}

func TestRunStatsSingularWait(t *testing.T) {
	day := time.Date(2026, 6, 29, 12, 0, 0, 0, time.Local)
	st := newTestStore(t, day)
	if err := st.Record(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := runStats(&buf, st, func() time.Time { return day }); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 wait") || strings.Contains(out, "1 waits") {
		t.Errorf("expected singular '1 wait', got:\n%s", out)
	}
	if !strings.Contains(out, "30s") {
		t.Errorf("expected '30s' for sub-minute total, got:\n%s", out)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1 min"},
		{6 * time.Minute, "6 min"},
		{90 * time.Minute, "1 h 30 min"},
		{2 * time.Hour, "2 h"},
		{125 * time.Minute, "2 h 5 min"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Errorf("humanDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestCountNoun(t *testing.T) {
	if got := countNoun(1, "wait", "waits"); got != "1 wait" {
		t.Errorf("countNoun(1) = %q, want '1 wait'", got)
	}
	if got := countNoun(3, "wait", "waits"); got != "3 waits" {
		t.Errorf("countNoun(3) = %q, want '3 waits'", got)
	}
	if got := countNoun(0, "wait", "waits"); got != "0 waits" {
		t.Errorf("countNoun(0) = %q, want '0 waits'", got)
	}
}
