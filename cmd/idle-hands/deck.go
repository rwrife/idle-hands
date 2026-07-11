package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/duckdiff"
	"github.com/rwrife/idle-hands/internal/hook"
	"github.com/rwrife/idle-hands/internal/srs"
)

// cmdDeck implements `idle-hands deck`:
//
//	idle-hands deck            list every available deck (built-in + user + srs)
//	idle-hands deck <name>     preview every card in the named deck
//
// It exists so a user can see what decks they have (including their own under
// ~/.idle-hands/decks and the flashcard "srs" deck loaded from their card file)
// and eyeball the exact cards a deck will surface before pointing config at it.
// Output is the command's real result, so it goes to stdout; the user-deck
// directory and the SRS card-source path are resolved here but injected into
// the testable core so tests can use temp paths without a real $HOME or config.
func cmdDeck(args []string) (int, error) {
	dir, err := config.DecksDir()
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}
	// Load config best-effort so `deck` can surface the srs flashcard deck and
	// the duckdiff review deck when configured. A malformed config shouldn't
	// break listing the built-in decks, so on error we proceed with empty
	// per-deck settings.
	var srsCfg config.SRSConfig
	var duckCfg config.DuckDiffConfig
	var hooksCfg config.HooksConfig
	if cfg, err := config.Load(); err == nil {
		srsCfg = cfg.SRS
		duckCfg = cfg.DuckDiff
		hooksCfg = cfg.Hooks
	}
	return runDeck(stdout, dir, srsCfg, duckCfg, hooksCfg, args)
}

// runDeck is the testable core. userDir is the directory user decks are loaded
// from (may be empty or nonexistent). srsCfg carries the flashcard-deck source
// path (empty when unconfigured); duckCfg carries the duckdiff model/url/timeout
// (all optional). With no args it lists decks; with one arg it previews that
// deck; more than one arg is a usage error.
func runDeck(w io.Writer, userDir string, srsCfg config.SRSConfig, duckCfg config.DuckDiffConfig, hooksCfg config.HooksConfig, args []string) (int, error) {
	switch len(args) {
	case 0:
		return listDecks(w, userDir, srsCfg, duckCfg, hooksCfg)
	case 1:
		return previewDeck(w, userDir, srsCfg, duckCfg, hooksCfg, args[0])
	default:
		return 2, fmt.Errorf("deck: too many arguments (usage: idle-hands deck [name])")
	}
}

// listDecks renders the resolved catalog: one line per deck with its source,
// name, card count, and description. User decks that shadow a built-in of the
// same name are flagged so the override is never a surprise. When an srs card
// source is configured, the flashcard deck is appended too (loaded live) so the
// listing matches what watch would show.
func listDecks(w io.Writer, userDir string, srsCfg config.SRSConfig, duckCfg config.DuckDiffConfig, hooksCfg config.HooksConfig) (int, error) {
	cat, err := deck.Catalog(userDir)
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}

	fmt.Fprintln(w, "idle-hands 🙌 — available decks:")
	fmt.Fprintln(w)

	// Width the name column to the longest name for tidy alignment (include the
	// srs and duckdiff deck names so their rows line up too).
	nameW := 4
	for _, e := range cat {
		if len(e.Deck.Name) > nameW {
			nameW = len(e.Deck.Name)
		}
	}
	if len(srs.DeckName) > nameW {
		nameW = len(srs.DeckName)
	}
	if len(duckdiff.DeckName) > nameW {
		nameW = len(duckdiff.DeckName)
	}
	if len(hook.DeckName) > nameW {
		nameW = len(hook.DeckName)
	}

	for _, e := range cat {
		emoji := e.Deck.Emoji
		if emoji == "" {
			emoji = " "
		}
		line := fmt.Sprintf("  %s  %-*s  %-8s  %s — %s",
			emoji,
			nameW, e.Deck.Name,
			"["+e.Source.String()+"]",
			countNoun(len(e.Deck.Cards), "card", "cards"),
			e.Deck.Description,
		)
		if e.Shadows {
			line += "  (overrides built-in)"
		}
		fmt.Fprintln(w, line)
	}

	// Append the flashcard deck when configured, so `deck` reflects runtime.
	if srsCfg.Source != "" {
		if d, err := srs.LoadDeck(srsCfg.Source); err == nil {
			fmt.Fprintf(w, "  %s  %-*s  %-8s  %s — %s\n",
				d.Emoji, nameW, d.Name, "[srs]",
				countNoun(len(d.Cards), "card", "cards"), d.Description)
		} else {
			fmt.Fprintf(w, "  🧠  %-*s  %-8s  unavailable — %v\n", nameW, srs.DeckName, "[srs]", err)
		}
	}

	// Always list the duckdiff deck: unlike srs it needs no source config, and
	// it degrades to the static duck deck when there's no diff or no Ollama, so
	// it's always usable. It generates a card live, so the list shows its shape,
	// not a card count.
	fmt.Fprintf(w, "  %s  %-*s  %-8s  1 question — %s\n",
		duckDeckEmoji, nameW, duckdiff.DeckName, "[live]",
		"One review question about your staged diff (local LLM); falls back to duck.")

	// List the hook deck when hooks are configured: it runs one registered
	// command per wait and shows the result. Without [[hooks]] there's nothing
	// to run, so we note that instead of offering an empty deck.
	if len(hooksCfg.Specs) > 0 {
		fmt.Fprintf(w, "  %s  %-*s  %-8s  %s — %s\n",
			hookDeckEmoji, nameW, hook.DeckName, "[live]",
			countNoun(len(hooksCfg.Specs), "hook", "hooks"),
			"Runs one of your [[hooks]] each wait and shows its result.")
	} else {
		fmt.Fprintf(w, "  %s  %-*s  %-8s  0 hooks — %s\n",
			hookDeckEmoji, nameW, hook.DeckName, "[live]",
			"Add [[hooks]] to config.toml to run commands during the wait.")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Preview one with `idle-hands deck <name>`; select one in ~/.idle-hands/config.toml.")
	if userDir != "" {
		fmt.Fprintf(w, "Drop your own *.toml decks in %s.\n", userDir)
	}
	return 0, nil
}

// duckDeckEmoji flavors the duckdiff row/preview. It matches the duckdiff
// package's deck emoji without exporting it, so the listing reads consistently
// even before the deck is loaded live.
const duckDeckEmoji = "🦆"

// hookDeckEmoji flavors the hook row/preview, matching internal/hook's deck
// emoji without exporting it.
const hookDeckEmoji = "🪝"

// previewDeck prints every card in the named deck, in order, with its source.
// This is the "see exactly what I'll get" view; it resolves user decks over
// built-ins just as watch does, and handles the special "srs" and "duckdiff"
// names by loading them live (the configured flashcard source; a question
// generated from the staged diff), so the preview always matches runtime.
func previewDeck(w io.Writer, userDir string, srsCfg config.SRSConfig, duckCfg config.DuckDiffConfig, hooksCfg config.HooksConfig, name string) (int, error) {
	if name == hook.DeckName {
		hd, err := hook.LoadDeck(hook.Options{Specs: hooksCfg.Specs, Timeout: hooksCfg.Timeout})
		if err != nil {
			return 1, fmt.Errorf("deck: %w", err)
		}
		fmt.Fprintf(w, "idle-hands 🙌 — deck %q [live hooks]:\n\n", hook.DeckName)
		for _, n := range hd.Names() {
			fmt.Fprintf(w, "  %s  %s — runs each wait, shows ✅/❌ and last output line\n", hookDeckEmoji, n)
		}
		return 0, nil
	}

	if name == srs.DeckName {
		d, err := srs.LoadDeck(srsCfg.Source)
		if err != nil {
			return 1, fmt.Errorf("deck: %w", err)
		}
		return printDeck(w, d, srs.DeckName), nil
	}

	if name == duckdiff.DeckName {
		res, err := duckdiff.LoadDeck(duckdiff.Options{
			Model:   duckCfg.Model,
			URL:     duckCfg.URL,
			Timeout: duckCfg.Timeout,
		})
		if err != nil {
			return 1, fmt.Errorf("deck: %w", err)
		}
		source := "live"
		if !res.Live {
			source = "fallback: " + res.Reason
		}
		return printDeck(w, res.Deck, source), nil
	}

	d, src, err := deck.Resolve(name, userDir)
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}
	return printDeck(w, d, src.String()), nil
}

// printDeck writes the shared preview layout for a resolved deck and returns 0.
func printDeck(w io.Writer, d deck.Deck, source string) int {
	emoji := d.Emoji
	if emoji != "" {
		emoji += " "
	}
	fmt.Fprintf(w, "%s%s  [%s] — %s\n", emoji, d.Name, source, d.Description)
	fmt.Fprintf(w, "%s\n", strings.Repeat("─", 48))

	for i, c := range d.Cards {
		fmt.Fprintf(w, "%2d. %s\n", i+1, c.Title)
		fmt.Fprintf(w, "    %s\n", c.Text)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s · one of these per busy window, never twice in a row.\n",
		countNoun(len(d.Cards), "card", "cards"))
	return 0
}
