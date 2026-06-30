package store

import (
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
