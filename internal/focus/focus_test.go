package focus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedClock(tm time.Time) func() time.Time {
	return func() time.Time { return tm }
}

func TestSetGetRoundTripAndActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	st, err := New(Options{Path: path, Now: fixedClock(now)})
	if err != nil {
		t.Fatal(err)
	}

	got, err := st.Set(25 * time.Minute)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !got.Active(now) {
		t.Errorf("focus should be active immediately after set")
	}
	if rem := got.Remaining(now); rem != 25*time.Minute {
		t.Errorf("Remaining = %s, want 25m0s", rem)
	}

	// A fresh Store over the same file must see the persisted block.
	st2, _ := New(Options{Path: path, Now: fixedClock(now)})
	reloaded, err := st2.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Active(now) {
		t.Errorf("reloaded focus should be active")
	}
	if !reloaded.Until.Equal(got.Until) {
		t.Errorf("reloaded Until = %v, want %v", reloaded.Until, got.Until)
	}
}

func TestExpiredBlockIsInactive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	start := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	st, _ := New(Options{Path: path, Now: fixedClock(start)})
	if _, err := st.Set(10 * time.Minute); err != nil {
		t.Fatal(err)
	}

	// Read it back with a clock 11 minutes later: block has expired.
	later := start.Add(11 * time.Minute)
	st2, _ := New(Options{Path: path, Now: fixedClock(later)})
	got, _ := st2.Get()
	if got.Active(later) {
		t.Errorf("focus should be inactive 11m after a 10m block")
	}
	if rem := got.Remaining(later); rem != 0 {
		t.Errorf("Remaining after expiry = %s, want 0", rem)
	}
}

func TestClearTurnsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	now := time.Date(2026, 7, 13, 21, 0, 0, 0, time.UTC)
	st, _ := New(Options{Path: path, Now: fixedClock(now)})
	if _, err := st.Set(30 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := st.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ := st.Get()
	if got.Active(now) {
		t.Errorf("focus should be inactive after Clear")
	}
	if !got.Until.IsZero() {
		t.Errorf("Until = %v, want zero after Clear", got.Until)
	}
	// Clearing again is idempotent.
	if err := st.Clear(); err != nil {
		t.Errorf("second Clear: %v", err)
	}
}

func TestSetRejectsNonPositive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	st, _ := New(Options{Path: path, Now: time.Now})
	if _, err := st.Set(0); err == nil {
		t.Error("Set(0) = nil error, want error")
	}
	if _, err := st.Set(-5 * time.Minute); err == nil {
		t.Error("Set(negative) = nil error, want error")
	}
}

func TestMissingAndEmptyFileAreInactive(t *testing.T) {
	now := time.Now()

	// Missing file.
	path := filepath.Join(t.TempDir(), "nope", "focus.json")
	st, _ := New(Options{Path: path, Now: fixedClock(now)})
	got, err := st.Get()
	if err != nil {
		t.Fatalf("Get on missing file: %v", err)
	}
	if got.Active(now) {
		t.Errorf("missing file should be inactive")
	}

	// Empty file.
	epath := filepath.Join(t.TempDir(), "focus.json")
	if err := os.WriteFile(epath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st2, _ := New(Options{Path: epath, Now: fixedClock(now)})
	got2, err := st2.Get()
	if err != nil {
		t.Fatalf("Get on empty file: %v", err)
	}
	if got2.Active(now) {
		t.Errorf("empty file should be inactive")
	}
}

func TestCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := New(Options{Path: path, Now: time.Now})
	if _, err := st.Get(); err == nil {
		t.Error("Get on corrupt file = nil error, want error")
	}
}

func TestWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "focus.json")
	st, _ := New(Options{Path: path, Now: time.Now})
	if _, err := st.Set(time.Minute); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "focus.json" {
			t.Errorf("unexpected leftover file %q after Set", e.Name())
		}
	}
}
