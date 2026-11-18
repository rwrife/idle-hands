package detect

import (
	"math"
	"sort"
	"time"
)

const (
	DefaultAdaptivePercentile = 75
	AdaptiveWarmupSamples     = 20
	AdaptiveHistoryLimit      = 128
	AdaptiveMinThreshold      = 5 * time.Second
	AdaptiveMaxThreshold      = 2 * time.Minute
)

func (d *Detector) observe(gap time.Duration) {
	// Sub-second output chunks are streaming, not useful think windows.
	if gap < time.Second {
		return
	}
	d.samples = append(d.samples, gap)
	if len(d.samples) > AdaptiveHistoryLimit {
		d.samples = d.samples[len(d.samples)-AdaptiveHistoryLimit:]
	}
}

func adaptiveThreshold(samples []time.Duration, percentile float64) time.Duration {
	if math.IsNaN(percentile) || percentile <= 0 || percentile > 100 {
		percentile = DefaultAdaptivePercentile
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	threshold := sorted[int(math.Ceil(percentile/100*float64(len(sorted))))-1]
	if threshold < AdaptiveMinThreshold {
		return AdaptiveMinThreshold
	}
	if threshold > AdaptiveMaxThreshold {
		return AdaptiveMaxThreshold
	}
	return threshold
}
