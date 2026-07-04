// Package procwatch turns a running process's CPU activity into the same
// BUSY/IDLE "real progress vs. quiet" signal that internal/wrap derives from a
// wrapped command's output. It is what powers `idle-hands watch --process
// <name>`: instead of wrapping and tee-ing an agent's output, we watch an
// already-running process (a GUI agent, an IDE sidebar, a background CLI) and
// treat "the process is burning CPU" as it being busy/thinking.
//
// The design mirrors internal/detect's split of concerns so the two paths reuse
// the same brain:
//
//   - A Sampler reports a process's cumulative CPU time (and whether it's still
//     alive). It is platform-specific (Linux reads /proc/<pid>/stat) and, above
//     all, injectable — tests drive a synthetic sampler with an exact timeline
//     instead of spawning real processes.
//   - A Poller samples on an interval, converts the *delta* in CPU time between
//     samples into a per-sample activity signal, and reports Active/Quiet plus
//     process exit. The watch loop feeds those into detect.Detector exactly like
//     it feeds output chunks: an Active sample is "real progress" (agent is
//     working → IDLE/you're-up), a run of Quiet samples lets the detector's
//     timeout fire BUSY (the process is blocked/waiting → your window).
//
// Why CPU rather than window titles: CPU busy-ness is available on every OS
// without special permissions or an accessibility API, degrades predictably,
// and matches the tool's existing "quiet == a window you can reclaim" model. A
// window-title / focus signal is a documented follow-up (see issue #10) that
// can layer on top of this same Poller as an additional activity source.
package procwatch

import (
	"errors"
	"fmt"
	"time"
)

// Sample is one observation of a watched process's CPU usage.
type Sample struct {
	// CPU is the process's cumulative CPU time since it started (user+system).
	// Only the delta between consecutive samples matters to the poller, so the
	// absolute epoch is irrelevant as long as it's monotonic for a given pid.
	CPU time.Duration
	// Alive is false once the process has exited (or was never found). The
	// poller stops and reports exit on the first non-alive sample.
	Alive bool
}

// Sampler reads the current CPU usage of a specific process. Implementations
// are constructed for one target (a pid, resolved from a name) and are called
// repeatedly by a Poller. A Sampler must be cheap: it is called every poll
// interval on the watch loop's cadence.
type Sampler interface {
	// Sample returns the process's current cumulative CPU time and liveness. A
	// transient read error should be surfaced (the poller treats an error as a
	// skipped sample, not process death) so a one-off /proc hiccup doesn't end
	// the watch. A definitively-gone process returns Alive=false, nil error.
	Sample() (Sample, error)
	// Describe returns a short human label for the target (e.g. "pid 4242
	// (claude)") used in the watch startup notice.
	Describe() string
}

// ActivityKind classifies a poll result for the watch loop.
type ActivityKind int

const (
	// Quiet means CPU usage since the last sample was at or below the activity
	// threshold: the process is (near) idle. A sustained run of these lets the
	// detector fire BUSY.
	Quiet ActivityKind = iota
	// Active means CPU usage since the last sample exceeded the threshold: the
	// process is doing real work. This is fed to the detector as real progress
	// and snaps it back to IDLE.
	Active
	// Exited means the process is gone; the watch loop should stop.
	Exited
)

// String renders an ActivityKind for logs and tests.
func (k ActivityKind) String() string {
	switch k {
	case Quiet:
		return "QUIET"
	case Active:
		return "ACTIVE"
	case Exited:
		return "EXITED"
	default:
		return "UNKNOWN"
	}
}

// Reading is the result of one Poller.Poll: how the process looked this tick,
// and (for observability/tests) the CPU delta that produced the classification.
type Reading struct {
	Kind ActivityKind
	// Delta is the CPU time consumed since the previous successful sample. It is
	// zero for the very first sample (no baseline yet) and for Exited readings.
	Delta time.Duration
}

// DefaultActiveCPURatio is the fraction of a single core's wall-clock time a
// process must consume between samples to count as Active. 0.05 (5%) filters
// out idle-loop / heartbeat jitter while still catching a process that has
// clearly started working. It is deliberately low: the detector's quiet
// *duration* threshold, not this per-sample cutoff, is the main knob for how
// long "thinking" must last before a card fires.
const DefaultActiveCPURatio = 0.05

// Poller samples a process on an interval and classifies each tick as Active,
// Quiet, or Exited. It is not safe for concurrent use; drive it from a single
// goroutine (the watch loop). Time is injectable so tests run without sleeping.
type Poller struct {
	sampler  Sampler
	interval time.Duration
	ratio    float64
	now      func() time.Time
	haveBase bool
	lastCPU  time.Duration
	lastAt   time.Time
}

// Options configure a Poller.
type Options struct {
	// Interval is how often Poll is expected to be called; it is used to turn
	// the required CPU ratio into an absolute per-tick CPU budget. Zero selects
	// a sensible default of 250ms (matching the watch loop's poll cadence).
	Interval time.Duration
	// ActiveCPURatio is the single-core fraction (0..1) of the interval a
	// process must burn to be Active. Zero or negative selects
	// DefaultActiveCPURatio.
	ActiveCPURatio float64
	// Now returns the current time; nil selects time.Now. Injected in tests.
	Now func() time.Time
}

// DefaultInterval is the poll cadence used when Options.Interval is unset. It
// matches internal watch's busyPollInterval so the detector is ticked at the
// same resolution whether it's driven by output or by process CPU.
const DefaultInterval = 250 * time.Millisecond

// NewPoller builds a Poller over the given Sampler. It returns an error only if
// the sampler is nil (a programming bug); an unsupported-platform sampler is a
// valid value that simply reports its unsupported error on first Poll.
func NewPoller(s Sampler, opts Options) (*Poller, error) {
	if s == nil {
		return nil, errors.New("procwatch: nil sampler")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	ratio := opts.ActiveCPURatio
	if ratio <= 0 {
		ratio = DefaultActiveCPURatio
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Poller{
		sampler:  s,
		interval: interval,
		ratio:    ratio,
		now:      now,
	}, nil
}

// Describe proxies the sampler's label for the watch startup notice.
func (p *Poller) Describe() string { return p.sampler.Describe() }

// Poll takes one sample and classifies it. The first successful sample only
// establishes a baseline and is reported as Quiet with a zero delta (there's no
// prior point to diff against, and reporting Active would spuriously reset the
// detector at startup). Subsequent samples compare CPU consumed against the
// per-tick budget derived from the elapsed wall time and the active ratio.
//
// A sampler error is returned to the caller and leaves the baseline untouched,
// so a transient /proc read failure is a skipped tick rather than a false
// Exited. A non-alive sample is reported as Exited so the loop can stop.
func (p *Poller) Poll() (Reading, error) {
	s, err := p.sampler.Sample()
	if err != nil {
		return Reading{Kind: Quiet}, err
	}
	if !s.Alive {
		return Reading{Kind: Exited}, nil
	}

	now := p.now()
	if !p.haveBase {
		p.haveBase = true
		p.lastCPU = s.CPU
		p.lastAt = now
		return Reading{Kind: Quiet, Delta: 0}, nil
	}

	elapsed := now.Sub(p.lastAt)
	if elapsed <= 0 {
		// Clock didn't advance (or went backwards); fall back to the configured
		// interval as the budget denominator so we still make a decision.
		elapsed = p.interval
	}
	delta := s.CPU - p.lastCPU
	if delta < 0 {
		// CPU counter reset (pid reused, counter wrapped). Rebase and treat as
		// quiet rather than reporting a bogus huge delta.
		delta = 0
	}
	p.lastCPU = s.CPU
	p.lastAt = now

	budget := time.Duration(float64(elapsed) * p.ratio)
	kind := Quiet
	if delta > budget {
		kind = Active
	}
	return Reading{Kind: kind, Delta: delta}, nil
}

// errUnsupported is the shared sentinel for platforms without a CPU sampler.
var errUnsupported = errors.New("procwatch: process watching is not supported on this platform")

// unsupportedSampler is returned by NewNameSampler on platforms lacking a real
// implementation. It satisfies Sampler but reports errUnsupported on Sample so
// the caller can fail with a clear, platform-specific message instead of the
// package silently doing nothing.
type unsupportedSampler struct{ name string }

func (u unsupportedSampler) Sample() (Sample, error) {
	return Sample{}, errUnsupported
}

func (u unsupportedSampler) Describe() string {
	return fmt.Sprintf("process %q (unsupported platform)", u.name)
}

// IsUnsupported reports whether err indicates this platform has no process
// sampler, so callers can print a caveat-aware message.
func IsUnsupported(err error) bool { return errors.Is(err, errUnsupported) }

// ErrNotFound is returned by a real sampler's constructor when no process
// matching the requested name is currently running.
var ErrNotFound = errors.New("procwatch: no matching process found")
