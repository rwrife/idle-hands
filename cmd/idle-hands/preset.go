package main

import (
	"fmt"
	"io"
	"time"

	"github.com/rwrife/idle-hands/internal/preset"
)

// cmdPreset implements `idle-hands preset`:
//
//	idle-hands preset            list every built-in agent preset
//	idle-hands preset <name>     show one preset's tuning (threshold + hints)
//
// Presets bundle known-good detector tuning per agent (Claude Code, Aider,
// Cursor, Codex, gh copilot) so `idle-hands watch --preset <name>` picks a busy
// threshold and "thinking" keyword hints matched to that agent instead of the
// user hand-tuning config. This command lets a user see what a preset does
// before selecting it. Output is the command's real result, so it goes to
// stdout.
func cmdPreset(args []string) (int, error) {
	return runPreset(stdout, args)
}

// runPreset is the testable core: no args lists all presets; one arg shows a
// single preset's detail; more than one is a usage error.
func runPreset(w io.Writer, args []string) (int, error) {
	switch len(args) {
	case 0:
		return listPresets(w), nil
	case 1:
		return showPreset(w, args[0])
	default:
		return 2, fmt.Errorf("preset: too many arguments (usage: idle-hands preset [name])")
	}
}

// listPresets renders one line per built-in preset: name, suggested busy
// threshold, and a short description. It mirrors the `deck` listing style.
func listPresets(w io.Writer) int {
	all := preset.All()

	fmt.Fprintln(w, "idle-hands 🙌 — agent presets (use with `watch --preset <name>`):")
	fmt.Fprintln(w)

	nameW := 4
	for _, p := range all {
		if len(p.Name) > nameW {
			nameW = len(p.Name)
		}
	}

	for _, p := range all {
		fmt.Fprintf(w, "  %-*s  %-6s  %s\n",
			nameW, p.Name,
			shortDur(p.BusyThreshold),
			p.Description)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "With no preset, idle-hands uses generic quiet-timeout detection.")
	fmt.Fprintln(w, "An explicit busy_threshold in ~/.idle-hands/config.toml still wins over a preset.")
	return 0
}

// showPreset prints the full tuning for one preset: its busy threshold and the
// agent-specific keyword hints it adds on top of the generic defaults.
func showPreset(w io.Writer, name string) (int, error) {
	p, ok := preset.Lookup(name)
	if !ok {
		return 1, preset.ErrorFor(name)
	}
	fmt.Fprintf(w, "%s — %s\n", p.Name, p.Description)
	fmt.Fprintf(w, "  busy threshold : %s\n", shortDur(p.BusyThreshold))
	if len(p.Keywords) == 0 {
		fmt.Fprintln(w, "  keyword hints  : (none beyond the generic defaults)")
	} else {
		fmt.Fprintf(w, "  keyword hints  : %s\n", countNoun(len(p.Keywords), "hint", "hints"))
		for _, kw := range p.Keywords {
			fmt.Fprintf(w, "                   • %q\n", kw)
		}
		fmt.Fprintln(w, "  (added on top of the built-in thinking/working/... defaults)")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Use it: idle-hands watch --preset %s -- %s\n", p.Name, exampleCmd(p.Name))
	return 0, nil
}

// shortDur renders a threshold like "25s" / "1m30s" without trailing zero
// units, matching how config expresses busy_threshold.
func shortDur(d time.Duration) string {
	return d.Round(time.Second).String()
}

// exampleCmd suggests the command a user most likely wraps for a given preset,
// so the "Use it" line is copy-pasteable. It's cosmetic guidance only.
func exampleCmd(name string) string {
	switch name {
	case "gh-copilot":
		return "gh copilot suggest"
	case "claude":
		return "claude"
	default:
		return name
	}
}
