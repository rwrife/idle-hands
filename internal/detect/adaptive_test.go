package detect

import (
	"math"
	"testing"
	"time"
)

func TestAdaptivePercentileAndClamps(t *testing.T) {
	samples := []time.Duration{40 * time.Second, 10 * time.Second, 30 * time.Second, 20 * time.Second}
	for _, tt := range []struct {
		name    string
		p       float64
		samples []time.Duration
		want    time.Duration
	}{
		{"p75", 75, samples, 30 * time.Second},
		{"p50", 50, samples, 20 * time.Second},
		{"p100", 100, samples, 40 * time.Second},
		{"fractional", 25.1, samples, 20 * time.Second},
		{"default", 0, samples, 30 * time.Second},
		{"nan", math.NaN(), samples, 30 * time.Second},
		{"minimum", 75, []time.Duration{time.Second}, AdaptiveMinThreshold},
		{"maximum", 75, []time.Duration{time.Hour}, AdaptiveMaxThreshold},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveThreshold(tt.samples, tt.p); got != tt.want {
				t.Fatalf("threshold = %v, want %v", got, tt.want)
			}
		})
	}
	if samples[0] != 40*time.Second {
		t.Fatal("percentile sorted caller's history in place")
	}
}

func TestAdaptiveWarmupAndObservation(t *testing.T) {
	clk := newClock()
	var observed []time.Duration
	d := New(Config{
		Adaptive: true, BusyThreshold: 42 * time.Second, Now: clk.now,
		OnIdleWindow: func(gap time.Duration) { observed = append(observed, gap) },
	})
	// Noise must not split a completed quiet gap.
	for i := 0; i < AdaptiveWarmupSamples; i++ {
		if got := d.Threshold(); got != 42*time.Second {
			t.Fatalf("warm-up threshold after %d samples = %v", i, got)
		}
		clk.advance(4 * time.Second)
		d.Feed([]byte("\r thinking |"))
		clk.advance(4 * time.Second)
		if _, ok := d.Feed([]byte("result\n")); ok {
			t.Fatal("sub-threshold sample caused a transition")
		}
	}
	if len(observed) != AdaptiveWarmupSamples || observed[0] != 8*time.Second {
		t.Fatalf("observed gaps = %v", observed)
	}
	if d.Threshold() != 8*time.Second {
		t.Fatalf("learned threshold = %v", d.Threshold())
	}
	clk.advance(7 * time.Second)
	if _, ok := d.Tick(clk.now()); ok {
		t.Fatal("BUSY before adaptive threshold")
	}
	clk.advance(time.Second)
	if ev, ok := d.Tick(clk.now()); !ok || ev.State != StateBusy {
		t.Fatal("no BUSY at adaptive threshold")
	}
	clk.advance(3 * time.Second)
	if ev, ok := d.Feed([]byte("more output\n")); !ok || ev.IdleFor != 3*time.Second {
		t.Fatalf("reclaimed duration changed: %+v, %v", ev, ok)
	}
	if observed[len(observed)-1] != 11*time.Second {
		t.Fatal("sample must include the pre-BUSY quiet time")
	}
}

func TestAdaptiveHistoryAndDisabledMode(t *testing.T) {
	clk := newClock()
	samples := make([]time.Duration, AdaptiveHistoryLimit+20)
	for i := range samples {
		samples[i] = time.Hour
	}
	for i := len(samples) - AdaptiveHistoryLimit; i < len(samples); i++ {
		samples[i] = 9 * time.Second
	}
	d := New(Config{Adaptive: true, IdleSamples: samples, Now: clk.now})
	if len(d.samples) != AdaptiveHistoryLimit || d.Threshold() != 9*time.Second {
		t.Fatal("did not keep only the latest history")
	}
	clk.advance(100 * time.Millisecond)
	d.Feed([]byte("streaming\n"))
	if len(d.samples) != AdaptiveHistoryLimit || d.samples[len(d.samples)-1] != 9*time.Second {
		t.Fatal("streaming gap entered history")
	}
	static := New(Config{
		IdleSamples: samples, Now: clk.now,
		OnIdleWindow: func(time.Duration) { t.Fatal("disabled mode recorded history") },
	})
	clk.advance(30 * time.Second)
	static.Feed([]byte("result\n"))
	if static.Threshold() != DefaultBusyThreshold || len(static.samples) != 0 {
		t.Fatal("disabled mode used adaptive history")
	}
}
