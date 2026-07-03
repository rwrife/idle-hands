package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rwrife/idle-hands/internal/preset"
)

// captureStdout runs fn with the package stdout redirected to a buffer and
// returns what was written. It mirrors the pattern used by the stats/deck tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := stdout
	var buf bytes.Buffer
	stdout = &buf
	defer func() { stdout = old }()
	fn()
	return buf.String()
}

// TestRunPresetList lists all presets and checks every canonical name shows up.
func TestRunPresetList(t *testing.T) {
	out := captureStdout(t, func() {
		if code, err := runPreset(stdout, nil); code != 0 || err != nil {
			t.Fatalf("runPreset(list) = (%d, %v), want (0, nil)", code, err)
		}
	})
	for _, name := range preset.Names() {
		if !strings.Contains(out, name) {
			t.Errorf("preset list missing %q:\n%s", name, out)
		}
	}
}

// TestRunPresetShow shows one preset and checks its keyword hints render.
func TestRunPresetShow(t *testing.T) {
	out := captureStdout(t, func() {
		if code, err := runPreset(stdout, []string{"claude"}); code != 0 || err != nil {
			t.Fatalf("runPreset(claude) = (%d, %v), want (0, nil)", code, err)
		}
	})
	if !strings.Contains(out, "esc to interrupt") {
		t.Errorf("preset show missing a known claude hint:\n%s", out)
	}
	// Accepts an alias and still renders the canonical name.
	out = captureStdout(t, func() {
		if code, _ := runPreset(stdout, []string{"gh_copilot"}); code != 0 {
			t.Fatalf("runPreset(gh_copilot) code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "gh-copilot") {
		t.Errorf("alias should resolve to canonical name:\n%s", out)
	}
}

// TestRunPresetErrors covers the unknown-name and too-many-args paths.
func TestRunPresetErrors(t *testing.T) {
	if code, err := runPreset(stdout, []string{"nope"}); code == 0 || err == nil {
		t.Errorf("runPreset(nope) = (%d, %v), want non-zero + error", code, err)
	}
	if code, err := runPreset(stdout, []string{"a", "b"}); code == 0 || err == nil {
		t.Errorf("runPreset(too many) = (%d, %v), want non-zero + error", code, err)
	}
}

// TestPresetCommandRouting checks `run(["preset"])` dispatches and exits 0.
func TestPresetCommandRouting(t *testing.T) {
	_ = captureStdout(t, func() {
		if code := run([]string{"preset"}); code != 0 {
			t.Fatalf("run(preset) = %d, want 0", code)
		}
	})
}
