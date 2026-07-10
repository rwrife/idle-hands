package main

import (
	"fmt"
	"io"
	"time"

	"github.com/rwrife/idle-hands/internal/store"
)

// cmdRecap implements `idle-hands recap`: a cheeky roundup of reclaimed time
// beyond the single-day `stats` view. It prints today's total, the rolling
// 7-day total, and the current day streak; with --weekly it adds a per-day
// breakdown of the last week. Like stats it writes to stdout (this is the
// command's output, not a notice) and returns a process exit code.
//
// The store and clock come from a small seam so tests drive a temp-file store
// and a fixed clock without a real $HOME or wall clock.
func cmdRecap(args []string) (int, error) {
	weekly, err := parseRecapArgs(args)
	if err != nil {
		return 2, err
	}
	st, err := store.New(store.Options{})
	if err != nil {
		return 1, fmt.Errorf("recap: %w", err)
	}
	return runRecap(stdout, st, time.Now, weekly)
}

// parseRecapArgs handles recap's tiny flag surface manually (mirroring how the
// other subcommands parse args) rather than pulling in the flag package: the
// only option is --weekly / -w. Any other argument is a usage error.
func parseRecapArgs(args []string) (weekly bool, err error) {
	for _, a := range args {
		switch a {
		case "--weekly", "-weekly", "-w":
			weekly = true
		default:
			return false, fmt.Errorf("recap: unknown argument %q (usage: idle-hands recap [--weekly])", a)
		}
	}
	return weekly, nil
}

// weekDays is the size of the rolling window recap summarizes.
const weekDays = 7

// runRecap is the testable core: it formats the roundup for st's current state
// to w. now supplies "today" so the day keys match what watch records; weekly
// toggles the per-day breakdown.
func runRecap(w io.Writer, st *store.Store, now func() time.Time, weekly bool) (int, error) {
	today, err := st.Window(1)
	if err != nil {
		return 1, fmt.Errorf("recap: %w", err)
	}
	week, err := st.Window(weekDays)
	if err != nil {
		return 1, fmt.Errorf("recap: %w", err)
	}
	streak, err := st.Streak()
	if err != nil {
		return 1, fmt.Errorf("recap: %w", err)
	}

	// Nothing recorded across the whole rolling week (and nothing today): show
	// the same friendly nudge as stats rather than a wall of zeros.
	if week.Windows == 0 && today.Windows == 0 {
		fmt.Fprintln(w, "idle-hands 🙌 — no reclaimed idle windows yet this week.")
		fmt.Fprintln(w, "Wrap your agent with `idle-hands watch -- <cmd>` and come back after it's had a think.")
		return 0, nil
	}

	fmt.Fprintf(w, "idle-hands 🙌 — reclaimed %s across %s today.\n",
		humanDuration(time.Duration(today.Seconds)*time.Second),
		countNoun(today.Windows, "wait", "waits"),
	)
	fmt.Fprintf(w, "This week: %s across %s.\n",
		humanDuration(time.Duration(week.Seconds)*time.Second),
		countNoun(week.Windows, "wait", "waits"),
	)
	fmt.Fprintln(w, streakLine(streak))

	if weekly {
		fmt.Fprintln(w)
		writeWeekly(w, st, now)
	}
	return 0, nil
}

// streakLine renders the current streak with a bit of flair. A zero streak is
// stated plainly (no fire); any active streak gets the 🔥 and reads naturally
// as "N-day streak".
func streakLine(streak int) string {
	if streak <= 0 {
		return "No active streak — reclaim a window today to start one."
	}
	return fmt.Sprintf("🔥 %d-day streak.", streak)
}

// writeWeekly prints a per-day breakdown for the last weekDays days, most
// recent first, using the store's clock for "today". Days with no reclaimed
// windows are shown as a dash so the streak/gap pattern is visible at a glance.
func writeWeekly(w io.Writer, st *store.Store, now func() time.Time) {
	hist, err := st.History()
	if err != nil {
		// History read failure is non-fatal for the breakdown; the summary
		// lines above already succeeded. Note it and skip the breakdown.
		fmt.Fprintf(w, "(weekly breakdown unavailable: %v)\n", err)
		return
	}
	fmt.Fprintln(w, "Last 7 days:")
	today := now()
	for i := 0; i < weekDays; i++ {
		d := today.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		label := d.Format("Mon 01-02")
		if day, ok := hist[key]; ok && day.Windows > 0 {
			fmt.Fprintf(w, "  %s  %s across %s\n",
				label,
				humanDuration(time.Duration(day.Seconds)*time.Second),
				countNoun(day.Windows, "wait", "waits"),
			)
		} else {
			fmt.Fprintf(w, "  %s  —\n", label)
		}
	}
}
