// Package preset bundles known-good detector tuning per AI coding agent so a
// user can say `idle-hands watch --preset claude -- claude` and get a busy
// threshold and "thinking" keyword hints matched to that agent, instead of
// hand-tuning ~/.idle-hands/config.toml.
//
// A preset is deliberately thin: it contributes only the two things the
// BUSY/IDLE detector (internal/detect) actually consumes — a busy threshold and
// a set of lowercase keyword hints that mark a chunk of output as "thinking"
// noise rather than real progress. Everything else (spinner heuristic, card
// engine, stats) is unchanged. Presets never wrap or steer the agent; they only
// pre-fill detection knobs.
//
// The values here are starting points chosen from each agent's typical
// "thinking/working" phrasing and how long it tends to churn between visible
// output. They are intentionally conservative: when in doubt a preset leans on
// the generic quiet-timeout behavior (that's what you get with no preset at
// all) rather than being clever. Users can still override any of it in config.
package preset

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Profile is the detector tuning a preset contributes. It maps directly onto
// the fields of detect.Config that a preset is allowed to influence.
type Profile struct {
	// Name is the canonical preset key (e.g. "claude"), lowercase.
	Name string
	// Description is a one-line human summary for `idle-hands preset` / help.
	Description string
	// BusyThreshold is how long output should stay quiet before this agent is
	// considered "thinking". Zero means "use the generic default".
	BusyThreshold time.Duration
	// Keywords are extra lowercase substrings that mark a chunk as thinking
	// noise for this agent, on top of detect.DefaultKeywords. They are merged
	// with (not a replacement for) the defaults so generic spinners still work.
	Keywords []string
}

// builtins is the registry of shipped presets, keyed by canonical name. The
// thresholds and keywords are per-agent starting points; see the package doc.
//
// Guidance behind the numbers:
//   - claude (Claude Code): shows a live "esc to interrupt" thinking spinner and
//     can reason for a while before the first token; a slightly higher threshold
//     avoids firing on brief tool round-trips.
//   - aider: prints "Thinking..."/"Applying edit" and streams fairly promptly;
//     a lower threshold reclaims its shorter stalls.
//   - cursor (Cursor CLI/agent): "Generating"/"Working"; middle-of-the-road.
//   - codex (Codex CLI): "Thinking"/"Running"; comparable to claude.
//   - gh-copilot (gh copilot suggest/explain): short, snappy calls; a low
//     threshold so its brief waits still count.
var builtins = map[string]Profile{
	"claude": {
		Name:          "claude",
		Description:   "Claude Code — longer reasoning windows; 'thinking/esc to interrupt'.",
		BusyThreshold: 25 * time.Second,
		Keywords:      []string{"esc to interrupt", "tokens", "pondering", "cogitating", "herding"},
	},
	"aider": {
		Name:          "aider",
		Description:   "Aider — 'Thinking…/Applying edit'; streams fairly promptly.",
		BusyThreshold: 15 * time.Second,
		Keywords:      []string{"applying edit", "committing", "scanning repo", "tokens"},
	},
	"cursor": {
		Name:          "cursor",
		Description:   "Cursor agent — 'Generating/Working'; middle-of-the-road stalls.",
		BusyThreshold: 20 * time.Second,
		Keywords:      []string{"generating", "working", "indexing", "searching codebase"},
	},
	"codex": {
		Name:          "codex",
		Description:   "Codex CLI — 'Thinking/Running'; longer reasoning windows.",
		BusyThreshold: 25 * time.Second,
		Keywords:      []string{"running", "executing", "tokens used", "codex"},
	},
	"gh-copilot": {
		Name:          "gh-copilot",
		Description:   "gh copilot (suggest/explain) — short, snappy calls.",
		BusyThreshold: 10 * time.Second,
		Keywords:      []string{"generating response", "consulting", "copilot"},
	},
}

// aliases maps user-friendly spellings to a canonical preset name, so
// "gh_copilot", "ghcopilot", "claude-code" and the like all resolve. Matching is
// done after normalization (see normalize), so case and surrounding spaces are
// already handled by the caller.
var aliases = map[string]string{
	"claude-code": "claude",
	"claudecode":  "claude",
	"cursor-cli":  "cursor",
	"cursoragent": "cursor",
	"codex-cli":   "codex",
	"copilot":     "gh-copilot",
	"gh":          "gh-copilot",
	"ghcopilot":   "gh-copilot",
	"gh_copilot":  "gh-copilot",
	"gh-cli":      "gh-copilot",
}

// Lookup resolves a preset name (case-insensitive, tolerant of a few common
// aliases and separators) to its Profile. The returned bool is false when no
// preset matches, so callers can report a clear error listing the valid names.
//
// A blank name is treated as "no preset" and returns (zero, false) without
// error semantics — callers decide whether that's allowed (watch treats absent
// --preset as generic detection).
func Lookup(name string) (Profile, bool) {
	key := normalize(name)
	if key == "" {
		return Profile{}, false
	}
	if canonical, ok := aliases[key]; ok {
		key = canonical
	}
	p, ok := builtins[key]
	return p, ok
}

// Names returns the canonical preset names in sorted order, for help text and
// the `preset` listing command. Aliases are intentionally omitted to keep the
// list short and unambiguous.
func Names() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// All returns every built-in Profile sorted by name. It backs the `preset`
// listing command so the CLI and the registry can never drift apart.
func All() []Profile {
	names := Names()
	out := make([]Profile, 0, len(names))
	for _, n := range names {
		out = append(out, builtins[n])
	}
	return out
}

// MergeKeywords returns base with the preset's Keywords appended, de-duplicated
// (case-insensitively) and with order preserved (base first, then any new
// preset keywords). It never mutates base. This is how a preset augments — but
// does not replace — detect.DefaultKeywords, so generic spinner/thinking
// detection keeps working alongside the agent-specific hints.
func (p Profile) MergeKeywords(base []string) []string {
	seen := make(map[string]struct{}, len(base)+len(p.Keywords))
	out := make([]string, 0, len(base)+len(p.Keywords))
	add := func(list []string) {
		for _, kw := range list {
			k := strings.ToLower(strings.TrimSpace(kw))
			if k == "" {
				continue
			}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	add(base)
	add(p.Keywords)
	return out
}

// normalize lower-cases and trims a preset name and collapses internal spaces to
// single hyphens, so "Claude Code", "claude_code" and "claude-code" all map to
// the same alias key.
func normalize(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	// Collapse runs of separators (space/underscore) to a single hyphen.
	s = strings.ReplaceAll(s, "_", "-")
	fields := strings.Fields(strings.ReplaceAll(s, "-", " "))
	return strings.Join(fields, "-")
}

// Error is returned (as a string via ErrorFor) when a preset name doesn't
// resolve; it lists the valid names so the user can correct the flag. It is a
// helper rather than an error type because callers just need the message.
func ErrorFor(name string) error {
	return fmt.Errorf("unknown preset %q (valid: %s)", name, strings.Join(Names(), ", "))
}
