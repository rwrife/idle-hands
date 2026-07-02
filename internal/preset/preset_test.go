package preset

import (
	"testing"
	"time"
)

// TestLookupCanonical checks every advertised name resolves to a profile whose
// Name matches, with a positive threshold and no empty keyword hints.
func TestLookupCanonical(t *testing.T) {
	for _, name := range Names() {
		p, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) = _, false; want a profile", name)
		}
		if p.Name != name {
			t.Errorf("Lookup(%q).Name = %q, want %q", name, p.Name, name)
		}
		if p.BusyThreshold <= 0 {
			t.Errorf("preset %q has non-positive BusyThreshold %v", name, p.BusyThreshold)
		}
		for _, kw := range p.Keywords {
			if kw == "" {
				t.Errorf("preset %q has an empty keyword hint", name)
			}
		}
		if p.Description == "" {
			t.Errorf("preset %q has no description", name)
		}
	}
}

// TestLookupCaseAndAlias verifies normalization and a representative set of
// aliases all resolve to the expected canonical preset.
func TestLookupCaseAndAlias(t *testing.T) {
	cases := map[string]string{
		"claude":      "claude",
		"CLAUDE":      "claude",
		"  claude  ":  "claude",
		"Claude Code": "claude",
		"claude-code": "claude",
		"claude_code": "claude",
		"cursor-cli":  "cursor",
		"codex_cli":   "codex",
		"copilot":     "gh-copilot",
		"gh copilot":  "gh-copilot",
		"gh_copilot":  "gh-copilot",
		"GH-Copilot":  "gh-copilot",
	}
	for in, want := range cases {
		p, ok := Lookup(in)
		if !ok {
			t.Errorf("Lookup(%q) = _, false; want %q", in, want)
			continue
		}
		if p.Name != want {
			t.Errorf("Lookup(%q).Name = %q, want %q", in, p.Name, want)
		}
	}
}

// TestLookupUnknown ensures unknown or blank names don't resolve and that the
// error message lists the valid names.
func TestLookupUnknown(t *testing.T) {
	for _, bad := range []string{"", "   ", "gemini", "nope", "cla"} {
		if _, ok := Lookup(bad); ok {
			t.Errorf("Lookup(%q) resolved, want no match", bad)
		}
	}
	err := ErrorFor("gemini")
	if err == nil {
		t.Fatal("ErrorFor returned nil")
	}
	msg := err.Error()
	for _, name := range Names() {
		if !contains(msg, name) {
			t.Errorf("ErrorFor message %q missing valid preset %q", msg, name)
		}
	}
}

// TestMergeKeywordsAugments confirms preset keywords are appended to the base
// (defaults) without dropping any base entry, de-duplicated case-insensitively,
// and that base is never mutated.
func TestMergeKeywordsAugments(t *testing.T) {
	base := []string{"thinking", "working"}
	baseCopy := append([]string(nil), base...)

	p := Profile{Keywords: []string{"Working", "esc to interrupt", "  ", "TOKENS", "tokens"}}
	got := p.MergeKeywords(base)

	// Base preserved and untouched.
	if len(base) != len(baseCopy) || base[0] != baseCopy[0] || base[1] != baseCopy[1] {
		t.Fatalf("MergeKeywords mutated base: %v", base)
	}
	// Every base keyword survives.
	for _, kw := range base {
		if !hasString(got, kw) {
			t.Errorf("merged set %v dropped base keyword %q", got, kw)
		}
	}
	// New, non-duplicate hints are added (lowercased) exactly once.
	if countString(got, "esc to interrupt") != 1 {
		t.Errorf("expected 'esc to interrupt' once, got %v", got)
	}
	if countString(got, "tokens") != 1 {
		t.Errorf("expected 'tokens' de-duplicated to once, got %v", got)
	}
	// "Working" duplicate of base "working" must not create a second entry.
	if countString(got, "working") != 1 {
		t.Errorf("case-insensitive duplicate 'working' not collapsed: %v", got)
	}
	// No blank slipped through.
	for _, kw := range got {
		if kw == "" {
			t.Fatalf("merged set contains empty keyword: %v", got)
		}
	}
}

// TestAllMatchesNames guards against the registry and the listing drifting: All
// must return one profile per Name, in the same order.
func TestAllMatchesNames(t *testing.T) {
	names := Names()
	all := All()
	if len(all) != len(names) {
		t.Fatalf("All() has %d profiles, Names() has %d", len(all), len(names))
	}
	for i, p := range all {
		if p.Name != names[i] {
			t.Errorf("All()[%d].Name = %q, want %q", i, p.Name, names[i])
		}
	}
}

// TestThresholdsAreSane is a light sanity bound so a future edit can't set an
// absurd threshold (e.g. 0 or hours) without a test noticing.
func TestThresholdsAreSane(t *testing.T) {
	for _, p := range All() {
		if p.BusyThreshold < 5*time.Second || p.BusyThreshold > 2*time.Minute {
			t.Errorf("preset %q threshold %v outside sane [5s,2m] range", p.Name, p.BusyThreshold)
		}
	}
}

// --- tiny local helpers (avoid importing strings/slices in the test) ---

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func hasString(list []string, want string) bool { return countString(list, want) > 0 }

func countString(list []string, want string) int {
	c := 0
	for _, v := range list {
		if v == want {
			c++
		}
	}
	return c
}
