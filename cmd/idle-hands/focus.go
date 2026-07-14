package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rwrife/idle-hands/internal/focus"
)

// cmdFocus implements `idle-hands focus`:
//
//	idle-hands focus <duration>   start a focus block (e.g. 25m, 1h30m)
//	idle-hands focus off          clear any active focus block
//	idle-hands focus              report remaining focus time
//
// A focus block suppresses cards during watch even while the agent is BUSY,
// while still recording reclaimed windows for stats (unless focus_safe.
// suppress_stats is set). The focus-until timestamp lives in
// ~/.idle-hands/focus.json so it survives restarts.
func cmdFocus(args []string) (int, error) {
	st, err := focus.New(focus.Options{})
	if err != nil {
		return 1, fmt.Errorf("focus: %w", err)
	}
	return runFocus(stdout, st, time.Now, args)
}

// runFocus is the testable core: it dispatches the focus subcommand against st
// using now for "remaining" math, writing output to w.
func runFocus(w io.Writer, st *focus.Store, now func() time.Time, args []string) (int, error) {
	switch {
	case len(args) == 0:
		return focusStatus(w, st, now())

	case len(args) == 1 && isOffArg(args[0]):
		if err := st.Clear(); err != nil {
			return 1, fmt.Errorf("focus: %w", err)
		}
		fmt.Fprintln(w, "idle-hands 🎯 — focus-safe mode off; cards will show again.")
		return 0, nil

	case len(args) == 1:
		d, err := time.ParseDuration(args[0])
		if err != nil {
			return 2, fmt.Errorf("focus: invalid duration %q (try 25m, 1h, or 90s): %w", args[0], err)
		}
		if d <= 0 {
			return 2, fmt.Errorf("focus: duration must be positive, got %q", args[0])
		}
		state, err := st.Set(d)
		if err != nil {
			return 1, fmt.Errorf("focus: %w", err)
		}
		fmt.Fprintf(w, "idle-hands 🎯 — focus-safe mode on for %s (until %s). Cards hushed; reclaimed time still counts.\n",
			humanRemaining(d), state.Until.Local().Format("15:04"))
		return 0, nil

	default:
		return 2, fmt.Errorf("focus: too many arguments (usage: idle-hands focus [<duration>|off])")
	}
}

// focusStatus reports the current focus state for the no-arg form.
func focusStatus(w io.Writer, st *focus.Store, now time.Time) (int, error) {
	state, err := st.Get()
	if err != nil {
		return 1, fmt.Errorf("focus: %w", err)
	}
	if !state.Active(now) {
		fmt.Fprintln(w, "idle-hands 🎯 — focus-safe mode is off. Start one with `idle-hands focus 25m`.")
		return 0, nil
	}
	fmt.Fprintf(w, "idle-hands 🎯 — focus-safe mode on: %s left (until %s).\n",
		humanRemaining(state.Remaining(now)), state.Until.Local().Format("15:04"))
	return 0, nil
}

// isOffArg reports whether arg turns focus off. A few friendly spellings are
// accepted so the obvious ones all work.
func isOffArg(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "off", "stop", "clear", "cancel", "end":
		return true
	}
	return false
}

// humanRemaining renders a focus duration in friendly units, rounding to the
// nearest minute above a minute and to seconds below: "25 min", "1 h 30 min",
// "45s".
func humanRemaining(d time.Duration) string {
	if d < time.Minute {
		secs := int(d.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		return fmt.Sprintf("%ds", secs)
	}
	if d < time.Hour {
		mins := int(d.Round(time.Minute) / time.Minute)
		if mins < 1 {
			mins = 1
		}
		return fmt.Sprintf("%d min", mins)
	}
	h := int(d / time.Hour)
	mins := int((d % time.Hour).Round(time.Minute) / time.Minute)
	if mins == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, mins)
}
