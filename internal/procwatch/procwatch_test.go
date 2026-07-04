package procwatch

import (
	"errors"
	"testing"
	"time"
)

// fakeSampler is a scripted Sampler for driving the Poller with an exact
// timeline of CPU readings, without a real process. Each call to Sample pops
// the next scripted step; running past the end returns the last step forever.
type fakeSampler struct {
	steps []fakeStep
	i     int
	label string
}

type fakeStep struct {
	cpu   time.Duration
	alive bool
	err   error
}

func (f *fakeSampler) Sample() (Sample, error) {
	step := f.steps[len(f.steps)-1]
	if f.i < len(f.steps) {
		step = f.steps[f.i]
		f.i++
	}
	if step.err != nil {
		return Sample{}, step.err
	}
	return Sample{CPU: step.cpu, Alive: step.alive}, nil
}

func (f *fakeSampler) Describe() string {
	if f.label == "" {
		return "fake"
	}
	return f.label
}

// fakeClock advances by a fixed step each time it's read, so the Poller sees a
// deterministic elapsed time between samples.
type fakeClock struct {
	t    time.Time
	step time.Duration
}

func (c *fakeClock) now() time.Time {
	cur := c.t
	c.t = c.t.Add(c.step)
	return cur
}

// TestPollerFirstSampleIsBaseline verifies the first poll only establishes a
// baseline: it reports Quiet with a zero delta even if the process already has
// significant CPU time, so startup never spuriously looks Active.
func TestPollerFirstSampleIsBaseline(t *testing.T) {
	s := &fakeSampler{steps: []fakeStep{{cpu: 5 * time.Second, alive: true}}}
	clk := &fakeClock{t: time.Unix(0, 0), step: 250 * time.Millisecond}
	p, err := NewPoller(s, Options{Interval: 250 * time.Millisecond, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != Quiet || r.Delta != 0 {
		t.Fatalf("first poll = %v (delta %v), want QUIET delta 0", r.Kind, r.Delta)
	}
}

// TestPollerActiveVsQuiet checks the ratio-based classification: a CPU delta
// above the per-tick budget is Active; a delta below it is Quiet. With a 250ms
// interval and the default 5% ratio, the budget is ~12.5ms per tick.
func TestPollerActiveVsQuiet(t *testing.T) {
	// baseline 0, then +100ms CPU (busy), then +0ms (idle), then +1ms (idle).
	s := &fakeSampler{steps: []fakeStep{
		{cpu: 0, alive: true},
		{cpu: 100 * time.Millisecond, alive: true},
		{cpu: 100 * time.Millisecond, alive: true},
		{cpu: 101 * time.Millisecond, alive: true},
	}}
	clk := &fakeClock{t: time.Unix(0, 0), step: 250 * time.Millisecond}
	p, _ := NewPoller(s, Options{Interval: 250 * time.Millisecond, Now: clk.now})

	want := []ActivityKind{Quiet, Active, Quiet, Quiet}
	for i, w := range want {
		r, err := p.Poll()
		if err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		if r.Kind != w {
			t.Errorf("poll %d = %v (delta %v), want %v", i, r.Kind, r.Delta, w)
		}
	}
}

// TestPollerExit verifies a non-alive sample is reported as Exited.
func TestPollerExit(t *testing.T) {
	s := &fakeSampler{steps: []fakeStep{
		{cpu: 0, alive: true},
		{alive: false},
	}}
	clk := &fakeClock{t: time.Unix(0, 0), step: 250 * time.Millisecond}
	p, _ := NewPoller(s, Options{Now: clk.now})

	if r, _ := p.Poll(); r.Kind != Quiet {
		t.Fatalf("baseline poll = %v, want QUIET", r.Kind)
	}
	r, err := p.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != Exited {
		t.Fatalf("second poll = %v, want EXITED", r.Kind)
	}
}

// TestPollerSamplerErrorIsSkip verifies a sampler error is surfaced and leaves
// the baseline intact (a skipped tick, not process death), so the next good
// sample still diffs against the prior real one.
func TestPollerSamplerErrorIsSkip(t *testing.T) {
	boom := errors.New("boom")
	s := &fakeSampler{steps: []fakeStep{
		{cpu: 0, alive: true},                    // baseline
		{err: boom},                              // transient error
		{cpu: 5 * time.Millisecond, alive: true}, // small delta vs baseline → quiet
	}}
	clk := &fakeClock{t: time.Unix(0, 0), step: 250 * time.Millisecond}
	p, _ := NewPoller(s, Options{Interval: 250 * time.Millisecond, Now: clk.now})

	p.Poll() // baseline
	if _, err := p.Poll(); err == nil {
		t.Fatal("expected sampler error to be surfaced")
	}
	r, err := p.Poll()
	if err != nil {
		t.Fatalf("post-error poll errored: %v", err)
	}
	if r.Kind != Quiet {
		t.Fatalf("post-error poll = %v, want QUIET (delta vs baseline)", r.Kind)
	}
}

// TestPollerCounterResetIsQuiet verifies a backwards CPU delta (pid reuse /
// counter wrap) is clamped to zero and classified Quiet rather than a huge
// bogus Active spike.
func TestPollerCounterResetIsQuiet(t *testing.T) {
	s := &fakeSampler{steps: []fakeStep{
		{cpu: 10 * time.Second, alive: true},
		{cpu: 1 * time.Second, alive: true}, // went backwards
	}}
	clk := &fakeClock{t: time.Unix(0, 0), step: 250 * time.Millisecond}
	p, _ := NewPoller(s, Options{Now: clk.now})
	p.Poll()
	r, _ := p.Poll()
	if r.Kind != Quiet || r.Delta != 0 {
		t.Fatalf("reset poll = %v (delta %v), want QUIET delta 0", r.Kind, r.Delta)
	}
}

// TestNilSampler ensures NewPoller rejects a nil sampler.
func TestNilSampler(t *testing.T) {
	if _, err := NewPoller(nil, Options{}); err == nil {
		t.Fatal("NewPoller(nil) = nil error, want error")
	}
}

// TestUnsupportedSampler checks the unsupported-platform sampler reports itself
// via IsUnsupported so callers can special-case the message.
func TestUnsupportedSampler(t *testing.T) {
	u := unsupportedSampler{name: "x"}
	_, err := u.Sample()
	if !IsUnsupported(err) {
		t.Fatalf("IsUnsupported(%v) = false, want true", err)
	}
	if u.Describe() == "" {
		t.Error("unsupported sampler Describe() empty")
	}
}

// TestActivityKindString covers the stringer for logs/tests.
func TestActivityKindString(t *testing.T) {
	cases := map[ActivityKind]string{Quiet: "QUIET", Active: "ACTIVE", Exited: "EXITED", ActivityKind(99): "UNKNOWN"}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(k), got, want)
		}
	}
}
