package detect

import (
	"testing"
	"time"
)

// fakeClock is a controllable time source for deterministic timeline tests.
// Drive it with advance(); the detector reads now() with no real sleeping.
type fakeClock struct {
	t time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestDetector builds a detector with a 20s threshold wired to clk.
func newTestDetector(clk *fakeClock) *Detector {
	return New(Config{BusyThreshold: 20 * time.Second, Now: clk.now})
}

func TestStartsIdle(t *testing.T) {
	d := newTestDetector(newClock())
	if got := d.State(); got != StateIdle {
		t.Fatalf("initial state = %s, want IDLE", got)
	}
}

func TestDefaultThreshold(t *testing.T) {
	d := New(Config{}) // no threshold -> default
	if got := d.Threshold(); got != DefaultBusyThreshold {
		t.Fatalf("threshold = %s, want %s", got, DefaultBusyThreshold)
	}
}

// Quiet for the full threshold should flip to BUSY exactly once, and emit one
// event carrying the idle span.
func TestQuietGapEntersBusy(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	// 19s of quiet: still IDLE, no event.
	clk.advance(19 * time.Second)
	if ev, ok := d.Tick(clk.now()); ok {
		t.Fatalf("unexpected transition before threshold: %+v", ev)
	}
	if d.State() != StateIdle {
		t.Fatalf("state = %s before threshold, want IDLE", d.State())
	}

	// Cross the threshold.
	clk.advance(1 * time.Second) // total 20s
	ev, ok := d.Tick(clk.now())
	if !ok {
		t.Fatal("expected BUSY transition at threshold, got none")
	}
	if ev.State != StateBusy {
		t.Fatalf("event state = %s, want BUSY", ev.State)
	}
	if ev.IdleFor < 20*time.Second {
		t.Fatalf("BUSY IdleFor = %s, want >= 20s", ev.IdleFor)
	}
	if !ev.At.Equal(clk.now()) {
		t.Fatalf("event At = %v, want %v", ev.At, clk.now())
	}

	// A second tick must NOT re-emit (no flapping / no duplicate BUSY).
	clk.advance(5 * time.Second)
	if ev, ok := d.Tick(clk.now()); ok {
		t.Fatalf("duplicate BUSY transition: %+v", ev)
	}
}

// Real output during a BUSY window flips back to IDLE with one event.
func TestFreshOutputReturnsIdle(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	clk.advance(20 * time.Second)
	if _, ok := d.Tick(clk.now()); !ok {
		t.Fatal("expected BUSY first")
	}

	clk.advance(3 * time.Second) // busy window lasts 3s
	ev, ok := d.Feed([]byte("── round 1/2 ─ working ──\n  [1.1] doing a thing...\n"))
	if !ok {
		t.Fatal("expected IDLE transition on fresh output")
	}
	if ev.State != StateIdle {
		t.Fatalf("event state = %s, want IDLE", ev.State)
	}
	if ev.IdleFor < 3*time.Second {
		t.Fatalf("reclaimed window = %s, want ~3s", ev.IdleFor)
	}
	if d.State() != StateIdle {
		t.Fatalf("state = %s after output, want IDLE", d.State())
	}
}

// Short pauses below the threshold must not flap to BUSY.
func TestShortPausesDoNotFlap(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	for i := 0; i < 10; i++ {
		clk.advance(5 * time.Second) // well under 20s each time
		if ev, ok := d.Tick(clk.now()); ok {
			t.Fatalf("flapped to %s on a short pause (iter %d)", ev.State, i)
		}
		// A burst of real output resets the timer.
		if _, ok := d.Feed([]byte("  [x] line of real work\n")); ok {
			t.Fatalf("unexpected transition on plain output (iter %d)", i)
		}
		if d.State() != StateIdle {
			t.Fatalf("state drifted to %s (iter %d)", d.State(), i)
		}
	}
}

// The crux of the DoD: a spinner that keeps repainting "\r  thinking | " must
// NOT keep the detector out of BUSY. Even though bytes keep arriving, they are
// noise, so the quiet timer keeps running and BUSY fires at the threshold.
func TestSpinnerNoiseStillEntersBusy(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	frames := []string{
		"\r  thinking | ",
		"\r  thinking / ",
		"\r  thinking - ",
		"\r  thinking \\ ",
	}
	// 30s of pure spinner repaints at ~100ms cadence.
	transitions := 0
	for elapsed := time.Duration(0); elapsed < 30*time.Second; elapsed += 100 * time.Millisecond {
		clk.advance(100 * time.Millisecond)
		if ev, ok := d.Feed([]byte(frames[int(elapsed/(100*time.Millisecond))%len(frames)])); ok {
			t.Fatalf("spinner frame caused a transition to %s — it should be noise", ev.State)
		}
		if ev, ok := d.Tick(clk.now()); ok {
			transitions++
			if ev.State != StateBusy {
				t.Fatalf("tick transition = %s, want BUSY", ev.State)
			}
		}
	}
	if transitions != 1 {
		t.Fatalf("expected exactly one BUSY transition under spinner load, got %d", transitions)
	}
	if d.State() != StateBusy {
		t.Fatalf("final state = %s, want BUSY", d.State())
	}
}

// Bare carriage-return repaints and whitespace-only frames are noise too.
func TestCarriageReturnAndBlankAreNoise(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	for _, chunk := range []string{"\r", "\r   \r", "   ", "\r|\r", "⠙"} {
		if _, ok := d.Feed([]byte(chunk)); ok {
			t.Fatalf("chunk %q wrongly treated as real progress", chunk)
		}
	}
	// Still IDLE, but the idle timer was never reset by the noise, so BUSY
	// still fires on schedule.
	clk.advance(20 * time.Second)
	if _, ok := d.Tick(clk.now()); !ok {
		t.Fatal("expected BUSY after threshold despite noise chunks")
	}
}

// A line that merely *contains* a keyword but has real substance is progress.
func TestSubstantiveKeywordLineIsProgress(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)
	clk.advance(20 * time.Second)
	if _, ok := d.Tick(clk.now()); !ok {
		t.Fatal("expected BUSY first")
	}
	// "thinking" appears, but so does a real sentence with a newline.
	if _, ok := d.Feed([]byte("thinking: I'll now edit cmd/idle-hands/main.go and add a flag\n")); !ok {
		t.Fatal("substantive line containing a keyword should flip back to IDLE")
	}
	if d.State() != StateIdle {
		t.Fatalf("state = %s, want IDLE", d.State())
	}
}

// A keyword-only banner with no newline ("⠋ thinking…") is noise.
func TestKeywordOnlyBannerIsNoise(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)
	for _, chunk := range []string{"\r⠋ thinking… ", "\r  working... ", "\rgenerating  "} {
		if _, ok := d.Feed([]byte(chunk)); ok {
			t.Fatalf("keyword-only banner %q wrongly treated as progress", chunk)
		}
	}
	clk.advance(20 * time.Second)
	if _, ok := d.Tick(clk.now()); !ok {
		t.Fatal("expected BUSY: keyword banners should not reset the idle timer")
	}
}

// With the spinner heuristic disabled, ANY non-empty output is activity (pure
// quiet-timeout behavior) — so a spinner keeps it pinned to IDLE.
func TestDisableSpinnerHeuristic(t *testing.T) {
	clk := newClock()
	d := New(Config{BusyThreshold: 20 * time.Second, Now: clk.now, DisableSpinnerHeuristic: true})

	// Spinner repaints now count as activity.
	for elapsed := time.Duration(0); elapsed < 30*time.Second; elapsed += 1 * time.Second {
		clk.advance(1 * time.Second)
		_, _ = d.Feed([]byte("\r  thinking | "))
		if ev, ok := d.Tick(clk.now()); ok {
			t.Fatalf("entered %s despite continuous output with heuristic off", ev.State)
		}
	}
	if d.State() != StateIdle {
		t.Fatalf("state = %s, want IDLE (output never went quiet)", d.State())
	}

	// Now go genuinely quiet → BUSY.
	clk.advance(20 * time.Second)
	if _, ok := d.Tick(clk.now()); !ok {
		t.Fatal("expected BUSY once truly quiet")
	}
}

// A full think→work→think cycle (mirrors fake-agent.sh) should produce a clean
// BUSY, IDLE, BUSY sequence and nothing else.
func TestFullCycleSequence(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)

	var seq []State
	record := func(ev Event, ok bool) {
		if ok {
			seq = append(seq, ev.State)
		}
	}

	// Think gap 1: spinner for 25s -> BUSY.
	for i := 0; i < 250; i++ {
		clk.advance(100 * time.Millisecond)
		record(d.Feed([]byte("\r  thinking | ")))
		record(d.Tick(clk.now()))
	}
	// Work burst: real lines -> IDLE.
	for i := 0; i < 8; i++ {
		clk.advance(50 * time.Millisecond)
		record(d.Feed([]byte("  [1.x] doing a thing...\n")))
		record(d.Tick(clk.now()))
	}
	// Think gap 2: quiet 25s -> BUSY.
	for i := 0; i < 250; i++ {
		clk.advance(100 * time.Millisecond)
		record(d.Feed([]byte("\r  thinking / ")))
		record(d.Tick(clk.now()))
	}

	want := []State{StateBusy, StateIdle, StateBusy}
	if len(seq) != len(want) {
		t.Fatalf("transition sequence = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("transition[%d] = %s, want %s (full seq %v)", i, seq[i], want[i], seq)
		}
	}
}

// Tick with the zero time should fall back to the detector's own clock.
func TestTickZeroUsesClock(t *testing.T) {
	clk := newClock()
	d := newTestDetector(clk)
	clk.advance(20 * time.Second)
	if _, ok := d.Tick(time.Time{}); !ok {
		t.Fatal("Tick(zero) should consult the detector clock and fire BUSY")
	}
}
