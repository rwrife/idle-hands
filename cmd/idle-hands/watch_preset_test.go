package main

import (
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/preset"
)

// TestParseWatchFlagsPreset covers the happy paths: no flags, `--preset x`,
// `--preset=x`, and that a leading "--" (or the first non-flag token) stops
// idle-hands flag parsing so the rest reaches the child untouched.
func TestParseWatchFlagsPreset(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantPreset string
		wantRest   []string
	}{
		{"none", []string{"--", "claude"}, "", []string{"--", "claude"}},
		{"no-sep-plain-cmd", []string{"echo", "hi"}, "", []string{"echo", "hi"}},
		{"space form", []string{"--preset", "claude", "--", "claude"}, "claude", []string{"--", "claude"}},
		{"equals form", []string{"--preset=aider", "--", "aider"}, "aider", []string{"--", "aider"}},
		{"alias resolves", []string{"--preset", "gh_copilot", "--", "gh"}, "gh_copilot", []string{"--", "gh"}},
		{"no separator", []string{"--preset", "codex", "codex", "--flag"}, "codex", []string{"codex", "--flag"}},
		{"child keeps its flags after --", []string{"--preset", "claude", "--", "claude", "--dangerously"}, "claude", []string{"--", "claude", "--dangerously"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotFlags, gotRest, err := parseWatchFlags(tc.in)
			if err != nil {
				t.Fatalf("parseWatchFlags(%v) error: %v", tc.in, err)
			}
			if gotFlags.preset != tc.wantPreset {
				t.Errorf("preset = %q, want %q", gotFlags.preset, tc.wantPreset)
			}
			if !equalStrings(gotRest, tc.wantRest) {
				t.Errorf("rest = %v, want %v", gotRest, tc.wantRest)
			}
		})
	}
}

// TestParseWatchFlagsErrors covers the reject paths: unknown preset value,
// missing value, and an unknown idle-hands flag (not forwarded to the child).
func TestParseWatchFlagsErrors(t *testing.T) {
	cases := [][]string{
		{"--preset", "gemini", "--", "gemini"}, // unknown preset
		{"--preset"},                           // missing value
		{"--preset="},                          // empty value
		{"--bogus", "--", "cmd"},               // unknown flag
	}
	for _, in := range cases {
		if _, _, err := parseWatchFlags(in); err == nil {
			t.Errorf("parseWatchFlags(%v) = nil error, want an error", in)
		}
	}
}

// TestDetectorConfigPrecedence verifies the busy-threshold precedence and the
// keyword merge:
//   - no preset → config threshold, no extra keywords;
//   - preset + unset config threshold → preset threshold wins over the default;
//   - preset + explicit config threshold → config wins;
//   - preset always merges its keyword hints on top of the detector defaults.
func TestDetectorConfigPrecedence(t *testing.T) {
	base := config.Default() // BusyThreshold defaulted, BusyThresholdSet false

	// No preset: unchanged config-driven behavior.
	dc, err := detectorConfig(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if dc.BusyThreshold != base.BusyThreshold {
		t.Errorf("no-preset threshold = %v, want %v", dc.BusyThreshold, base.BusyThreshold)
	}
	if dc.Keywords != nil {
		t.Errorf("no-preset should not set keywords, got %v", dc.Keywords)
	}

	// Preset, config threshold NOT explicitly set → preset threshold applies.
	dc, err = detectorConfig(base, "claude")
	if err != nil {
		t.Fatal(err)
	}
	claudeP, ok := preset.Lookup("claude")
	if !ok {
		t.Fatal("claude preset should exist")
	}
	if dc.BusyThreshold != claudeP.BusyThreshold {
		t.Errorf("preset threshold = %v, want %v", dc.BusyThreshold, claudeP.BusyThreshold)
	}
	// Keywords must include both a default and a claude-specific hint.
	if !containsStr(dc.Keywords, "thinking") {
		t.Errorf("merged keywords missing default 'thinking': %v", dc.Keywords)
	}
	if !containsStr(dc.Keywords, "esc to interrupt") {
		t.Errorf("merged keywords missing claude hint 'esc to interrupt': %v", dc.Keywords)
	}

	// Preset, config threshold explicitly set → config wins.
	explicit := base
	explicit.BusyThreshold = 42 * time.Second
	explicit.BusyThresholdSet = true
	dc, err = detectorConfig(explicit, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if dc.BusyThreshold != 42*time.Second {
		t.Errorf("explicit config threshold should win: got %v, want 42s", dc.BusyThreshold)
	}
	// Keywords still merged even when config threshold wins.
	if !containsStr(dc.Keywords, "esc to interrupt") {
		t.Errorf("keywords should still merge with explicit threshold: %v", dc.Keywords)
	}
}

// TestDetectorConfigFeedsDetector is a light integration check: a detector
// built from a preset config treats that preset's keyword as thinking-noise
// (stays out of a spurious IDLE snap) — proving the merge actually reaches the
// state machine.
func TestDetectorConfigFeedsDetector(t *testing.T) {
	base := config.Default()
	dc, err := detectorConfig(base, "claude")
	if err != nil {
		t.Fatal(err)
	}
	// Drive with a fixed clock so behavior is deterministic.
	var now time.Time
	dc.Now = func() time.Time { return now }
	d := detect.New(dc)

	// A claude-specific "esc to interrupt" spinner line (no newline) must be
	// treated as noise: feeding it while IDLE produces no transition, and it
	// must not reset progress such that BUSY can't fire.
	if ev, changed := d.Feed([]byte("\r  esc to interrupt ")); changed {
		t.Errorf("preset keyword line should be noise, got transition %+v", ev)
	}
	// Advance past the preset threshold; BUSY should fire.
	claudeP, ok := preset.Lookup("claude")
	if !ok {
		t.Fatal("claude preset should exist")
	}
	now = now.Add(claudeP.BusyThreshold + time.Second)
	if _, changed := d.Tick(now); !changed {
		t.Error("expected BUSY after quiet gap past preset threshold")
	}
}

// --- helpers ---

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
