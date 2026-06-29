package main

import (
	"fmt"
	"io"
	"time"

	"github.com/rwrife/idle-hands/internal/store"
)

// cmdStats implements `idle-hands stats`: it reads the JSON scoreboard and
// prints today's reclaimed-time summary (plus an all-time line once there's
// history beyond today). It writes to stdout — this is the command's actual
// output, not a side notice — and returns a process exit code.
//
// The store and clock are taken from a small struct so tests can drive a
// temp-file store and a fixed clock without a real $HOME.
func cmdStats(args []string) (int, error) {
	st, err := store.New(store.Options{})
	if err != nil {
		return 1, fmt.Errorf("stats: %w", err)
	}
	return runStats(stdout, st, time.Now)
}

// runStats is the testable core: it formats the summary for st's current state
// to w. now supplies "today" so the day key matches what watch records.
func runStats(w io.Writer, st *store.Store, now func() time.Time) (int, error) {
	today, err := st.Today()
	if err != nil {
		return 1, fmt.Errorf("stats: %w", err)
	}

	if today.Windows == 0 {
		fmt.Fprintln(w, "idle-hands 🙌 — no reclaimed idle windows yet today.")
		fmt.Fprintln(w, "Wrap your agent with `idle-hands watch -- <cmd>` and come back after it's had a think.")
		return 0, nil
	}

	fmt.Fprintf(w, "idle-hands 🙌 — reclaimed %s across %s today.\n",
		humanDuration(time.Duration(today.Seconds)*time.Second),
		countNoun(today.Windows, "wait", "waits"),
	)

	// Show an all-time line only when there's history beyond today, so a
	// first-day user isn't shown two identical numbers.
	total, err := st.Total()
	if err != nil {
		return 1, fmt.Errorf("stats: %w", err)
	}
	if total.Windows > today.Windows {
		fmt.Fprintf(w, "All-time: %s across %s.\n",
			humanDuration(time.Duration(total.Seconds)*time.Second),
			countNoun(total.Windows, "wait", "waits"),
		)
	}
	return 0, nil
}

// humanDuration renders a reclaimed span in friendly units: seconds under a
// minute, whole-plus-fraction minutes under an hour, and hours+minutes beyond.
// It favors readability over precision (this is a cheeky scoreboard, not a
// stopwatch): "45s", "12 min", "1 h 5 min".
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	if d < time.Hour {
		mins := int(d.Round(time.Minute) / time.Minute)
		return fmt.Sprintf("%d min", mins)
	}
	h := int(d / time.Hour)
	mins := int((d % time.Hour).Round(time.Minute) / time.Minute)
	if mins == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, mins)
}

// countNoun formats a count with a singular/plural noun: 1 → "1 wait",
// 3 → "3 waits".
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
