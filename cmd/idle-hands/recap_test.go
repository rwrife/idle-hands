package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/store"
)

// seedStore builds a Store over a temp file and records windows on the given
// day offsets from base (0 = base day). Each entry records `secs` seconds.
func seedStore(t *testing.T, base time.Time, offsets []int, secs int) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	for _, off := range offsets {
		day := base.AddDate(0, 0, -off)
		st, err := store.New(store.Options{Path: path, Now: func() time.Time { return day }})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Record(time.Duration(secs) * time.Second); err != nil {
			t.Fatalf("Record off=%d: %v", off, err)
		}
	}
	st, err := store.New(store.Options{Path: path, Now: func() time.Time { return base }})
	if err != nil {
		t.Fatal(err)
	}
	return st, path
}

func TestRunRecapEmpty(t *testing.T) {
	day := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.New(store.Options{Path: path, Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code, err := runRecap(&buf, st, func() time.Time { return day }, false)
	if err != nil {
		t.Fatalf("runRecap: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "no reclaimed idle windows yet this week") {
		t.Errorf("empty recap missing nudge, got:\n%s", buf.String())
	}
}

func TestRunRecapTodayWeekStreak(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	// Windows today (-0), -1, -2 → 3-day streak; -4 also has one (in-week,
	// but past the streak gap at -3). Each records 60s.
	st, _ := seedStore(t, base, []int{0, 1, 2, 4}, 60)

	var buf bytes.Buffer
	if _, err := runRecap(&buf, st, func() time.Time { return base }, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Today: 1 window, 60s → "1 min", "1 wait".
	if !strings.Contains(out, "reclaimed 1 min across 1 wait today") {
		t.Errorf("today line wrong, got:\n%s", out)
	}
	// Week: 4 windows, 240s → "4 min", "4 waits".
	if !strings.Contains(out, "This week: 4 min across 4 waits") {
		t.Errorf("week line wrong, got:\n%s", out)
	}
	// Streak of 3 (today, -1, -2; gap at -3).
	if !strings.Contains(out, "🔥 3-day streak.") {
		t.Errorf("streak line wrong, got:\n%s", out)
	}
	// No --weekly → no per-day breakdown.
	if strings.Contains(out, "Last 7 days:") {
		t.Errorf("unexpected weekly breakdown without --weekly:\n%s", out)
	}
}

func TestRunRecapWeeklyBreakdown(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	// Windows today and -2 only; -1 is a gap shown as a dash.
	st, _ := seedStore(t, base, []int{0, 2}, 90)

	var buf bytes.Buffer
	if _, err := runRecap(&buf, st, func() time.Time { return base }, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "Last 7 days:") {
		t.Errorf("expected weekly header, got:\n%s", out)
	}
	// Should list exactly 7 day rows.
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Wed ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Tue ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Mon ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Sun ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Sat ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Fri ") ||
			strings.HasPrefix(strings.TrimSpace(line), "Thu ") {
			rows++
		}
	}
	if rows != 7 {
		t.Errorf("weekly breakdown listed %d day rows, want 7:\n%s", rows, out)
	}
	// The gap day (-1) must render a dash.
	if !strings.Contains(out, "—") {
		t.Errorf("expected a dash for the gap day, got:\n%s", out)
	}
}

func TestRunRecapStreakSingularDay(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	st, _ := seedStore(t, base, []int{0}, 30)
	var buf bytes.Buffer
	if _, err := runRecap(&buf, st, func() time.Time { return base }, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "🔥 1-day streak.") {
		t.Errorf("expected '1-day streak', got:\n%s", out)
	}
}

func TestStreakLine(t *testing.T) {
	if got := streakLine(0); !strings.Contains(got, "No active streak") {
		t.Errorf("streakLine(0) = %q, want no-streak message", got)
	}
	if got := streakLine(1); got != "🔥 1-day streak." {
		t.Errorf("streakLine(1) = %q, want '🔥 1-day streak.'", got)
	}
	if got := streakLine(5); got != "🔥 5-day streak." {
		t.Errorf("streakLine(5) = %q, want '🔥 5-day streak.'", got)
	}
}

func TestParseRecapArgs(t *testing.T) {
	if weekly, err := parseRecapArgs(nil); err != nil || weekly {
		t.Errorf("parseRecapArgs(nil) = (%v, %v), want (false, nil)", weekly, err)
	}
	if weekly, err := parseRecapArgs([]string{"--weekly"}); err != nil || !weekly {
		t.Errorf("parseRecapArgs(--weekly) = (%v, %v), want (true, nil)", weekly, err)
	}
	if weekly, err := parseRecapArgs([]string{"-w"}); err != nil || !weekly {
		t.Errorf("parseRecapArgs(-w) = (%v, %v), want (true, nil)", weekly, err)
	}
	if _, err := parseRecapArgs([]string{"--bogus"}); err == nil {
		t.Errorf("parseRecapArgs(--bogus) = nil error, want usage error")
	}
}
