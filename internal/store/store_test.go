package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a Now func pinned to the given time, for deterministic
// day-keying in tests.
func fixedClock(tm time.Time) func() time.Time {
	return func() time.Time { return tm }
}

func TestRecordAndTodayRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	day := time.Date(2026, 6, 29, 14, 0, 0, 0, time.Local)
	st, err := New(Options{Path: path, Now: fixedClock(day)})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.Record(90 * time.Second); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := st.Record(30 * time.Second); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := st.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if got.Windows != 2 {
		t.Errorf("Windows = %d, want 2", got.Windows)
	}
	if got.Seconds != 120 {
		t.Errorf("Seconds = %d, want 120", got.Seconds)
	}

	// A fresh Store over the same file must see the persisted data.
	st2, err := New(Options{Path: path, Now: fixedClock(day)})
	if err != nil {
		t.Fatal(err)
	}
	got2, err := st2.Today()
	if err != nil {
		t.Fatal(err)
	}
	if got2 != got {
		t.Errorf("reloaded Today = %+v, want %+v", got2, got)
	}
}

func TestRecordRoundsAndFloorsAtZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	day := time.Date(2026, 6, 29, 14, 0, 0, 0, time.Local)
	st, _ := New(Options{Path: path, Now: fixedClock(day)})

	// Sub-second window: counts as a window, contributes ~0 seconds.
	if err := st.Record(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Negative (clock skew) must not subtract from the total.
	if err := st.Record(-5 * time.Second); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Today()
	if got.Windows != 2 {
		t.Errorf("Windows = %d, want 2", got.Windows)
	}
	if got.Seconds < 0 {
		t.Errorf("Seconds = %d, want >= 0", got.Seconds)
	}
	// 0.4s rounds to 0; -5s floored to 0 → total 0.
	if got.Seconds != 0 {
		t.Errorf("Seconds = %d, want 0", got.Seconds)
	}
}

func TestSeparateDaysAndTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	d1 := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	d2 := time.Date(2026, 6, 29, 10, 0, 0, 0, time.Local)

	st1, _ := New(Options{Path: path, Now: fixedClock(d1)})
	if err := st1.Record(60 * time.Second); err != nil {
		t.Fatal(err)
	}

	st2, _ := New(Options{Path: path, Now: fixedClock(d2)})
	if err := st2.Record(120 * time.Second); err != nil {
		t.Fatal(err)
	}

	// Today (d2) sees only the second day's window.
	today, _ := st2.Today()
	if today.Windows != 1 || today.Seconds != 120 {
		t.Errorf("Today = %+v, want {1 120}", today)
	}

	// Total spans both days.
	total, err := st2.Total()
	if err != nil {
		t.Fatal(err)
	}
	if total.Windows != 2 || total.Seconds != 180 {
		t.Errorf("Total = %+v, want {2 180}", total)
	}

	days, err := st2.Days()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-06-28" || days[1] != "2026-06-29" {
		t.Errorf("Days = %v, want [2026-06-28 2026-06-29]", days)
	}
}

func TestTodayMissingFileIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "state.json")
	st, _ := New(Options{Path: path, Now: time.Now})
	got, err := st.Today()
	if err != nil {
		t.Fatalf("Today on missing file: %v", err)
	}
	if (got != DayStat{}) {
		t.Errorf("Today on missing file = %+v, want zero", got)
	}
}

func TestEmptyFileIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := New(Options{Path: path, Now: time.Now})
	got, err := st.Today()
	if err != nil {
		t.Fatalf("Today on empty file: %v", err)
	}
	if (got != DayStat{}) {
		t.Errorf("Today on empty file = %+v, want zero", got)
	}
}

func TestCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := New(Options{Path: path, Now: time.Now})
	if _, err := st.Today(); err == nil {
		t.Fatal("Today on corrupt file = nil error, want error")
	}
	if err := st.Record(time.Second); err == nil {
		t.Fatal("Record on corrupt file = nil error, want error")
	}
}

// TestWriteIsAtomicNoTempLeftover ensures the temp file used for the atomic
// rename does not linger after a successful write.
func TestWriteIsAtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st, _ := New(Options{Path: path, Now: time.Now})
	if err := st.Record(time.Second); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected leftover file %q after Record", e.Name())
		}
	}
}

// TestWindowSumsLastNDays checks that Window(n) sums exactly the last n
// calendar days ending today, ignoring older days and gaps.
func TestWindowSumsLastNDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	today := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	// Record on today, 2 days ago, and 8 days ago (outside a 7-day window).
	for _, off := range []int{0, 2, 8} {
		day := today.AddDate(0, 0, -off)
		st, _ := New(Options{Path: path, Now: fixedClock(day)})
		if err := st.Record(time.Duration(60*(off+1)) * time.Second); err != nil {
			t.Fatalf("Record off=%d: %v", off, err)
		}
	}
	st := mustStore(t, path, today)

	// Window(1) == today only: 60s, 1 window.
	got, err := st.Window(1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Windows != 1 || got.Seconds != 60 {
		t.Errorf("Window(1) = %+v, want {1 60}", got)
	}
	// Window(7) covers today (60s) + 2-days-ago (180s) = 240s, 2 windows;
	// the 8-days-ago 540s entry is outside the window.
	got, err = st.Window(7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Windows != 2 || got.Seconds != 240 {
		t.Errorf("Window(7) = %+v, want {2 240}", got)
	}
	// Non-positive n is a zero tally.
	if z, _ := st.Window(0); z != (DayStat{}) {
		t.Errorf("Window(0) = %+v, want zero", z)
	}
}

// TestStreakCountsConsecutiveDaysEndingToday verifies the streak reaches back
// through consecutive days with a window and stops at the first gap, and that
// a today with no window yields a zero streak.
func TestStreakCountsConsecutiveDaysEndingToday(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	today := time.Date(2026, 7, 9, 9, 0, 0, 0, time.Local)
	// Windows on today, -1, -2 (streak of 3); gap at -3; window at -4.
	for _, off := range []int{0, 1, 2, 4} {
		day := today.AddDate(0, 0, -off)
		st, _ := New(Options{Path: path, Now: fixedClock(day)})
		if err := st.Record(30 * time.Second); err != nil {
			t.Fatalf("Record off=%d: %v", off, err)
		}
	}
	st := mustStore(t, path, today)
	got, err := st.Streak()
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("Streak = %d, want 3 (today, -1, -2; gap at -3)", got)
	}

	// A store whose today has no recorded window has a zero current streak
	// even though a prior day did.
	stNoToday := mustStore(t, path, today.AddDate(0, 0, 3)) // 3 days later, no data
	if got, _ := stNoToday.Streak(); got != 0 {
		t.Errorf("Streak with empty today = %d, want 0", got)
	}
}

// TestPruneDropsOldDaysOnWrite checks that a Record beyond the retention window
// prunes day entries older than the window while keeping recent ones.
func TestPruneDropsOldDaysOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	today := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	// Seed an old day well outside a small retention window, plus a recent one.
	old := today.AddDate(0, 0, -10).Format(dateFormat)
	recent := today.AddDate(0, 0, -1).Format(dateFormat)
	seed := []byte(`{"version":1,"days":{"` + old + `":{"windows":2,"seconds":100},"` + recent + `":{"windows":1,"seconds":50}}}`)
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	// Retention of 3 days: only today, -1, -2 survive a write.
	st, _ := New(Options{Path: path, Now: fixedClock(today), RetentionDays: 3})
	if err := st.Record(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	days, err := st.Days()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if d == old {
			t.Errorf("old day %q survived pruning; days=%v", old, days)
		}
	}
	// The recent day and today must remain.
	var haveRecent, haveToday bool
	for _, d := range days {
		if d == recent {
			haveRecent = true
		}
		if d == today.Format(dateFormat) {
			haveToday = true
		}
	}
	if !haveRecent || !haveToday {
		t.Errorf("expected recent(%s) and today kept; days=%v", recent, days)
	}
}

// TestNegativeRetentionDisablesPruning ensures a negative RetentionDays keeps
// all history, even very old entries.
func TestNegativeRetentionDisablesPruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	today := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	old := today.AddDate(0, 0, -400).Format(dateFormat)
	seed := []byte(`{"version":1,"days":{"` + old + `":{"windows":1,"seconds":10}}}`)
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := New(Options{Path: path, Now: fixedClock(today), RetentionDays: -1})
	if err := st.Record(time.Second); err != nil {
		t.Fatal(err)
	}
	days, _ := st.Days()
	var haveOld bool
	for _, d := range days {
		if d == old {
			haveOld = true
		}
	}
	if !haveOld {
		t.Errorf("old day %q pruned despite disabled retention; days=%v", old, days)
	}
}

// TestLegacySingleDayMigration verifies a legacy top-level {windows,seconds}
// file is folded into the per-day map (attributed to its date, or today when
// none), and that the legacy fields are dropped once persisted.
func TestLegacySingleDayMigration(t *testing.T) {
	// Case A: legacy file with an explicit date.
	pathA := filepath.Join(t.TempDir(), "state.json")
	legacyDay := time.Date(2026, 7, 1, 12, 0, 0, 0, time.Local)
	today := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	seedA := []byte(`{"version":1,"windows":4,"seconds":480,"date":"2026-07-01"}`)
	if err := os.WriteFile(pathA, seedA, 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustStore(t, pathA, today)
	hist, err := st.History()
	if err != nil {
		t.Fatal(err)
	}
	if got := hist[legacyDay.Format(dateFormat)]; got.Windows != 4 || got.Seconds != 480 {
		t.Errorf("migrated legacy day = %+v, want {4 480}", got)
	}

	// Case B: legacy file with no date -> attributed to today; a Record then
	// persists the migrated form with legacy fields gone.
	pathB := filepath.Join(t.TempDir(), "state.json")
	seedB := []byte(`{"version":1,"windows":2,"seconds":120}`)
	if err := os.WriteFile(pathB, seedB, 0o644); err != nil {
		t.Fatal(err)
	}
	stB := mustStore(t, pathB, today)
	todayStat, err := stB.Today()
	if err != nil {
		t.Fatal(err)
	}
	if todayStat.Windows != 2 || todayStat.Seconds != 120 {
		t.Errorf("legacy-without-date Today = %+v, want {2 120}", todayStat)
	}
	// Record another window; total for today becomes 3 windows / 150s, and the
	// persisted file must no longer carry the legacy top-level fields.
	if err := stB.Record(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted["windows"]; ok {
		t.Errorf("persisted file still has legacy top-level 'windows': %s", raw)
	}
	if _, ok := persisted["seconds"]; ok {
		t.Errorf("persisted file still has legacy top-level 'seconds': %s", raw)
	}
	after, _ := stB.Today()
	if after.Windows != 3 || after.Seconds != 150 {
		t.Errorf("Today after Record = %+v, want {3 150}", after)
	}
}

// mustStore builds a default-retention Store over path pinned to day.
func mustStore(t *testing.T, path string, day time.Time) *Store {
	t.Helper()
	st, err := New(Options{Path: path, Now: fixedClock(day)})
	if err != nil {
		t.Fatal(err)
	}
	return st
}
