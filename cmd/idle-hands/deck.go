package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
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
	// Load config best-effort so `deck` can surface the srs flashcard deck when
	// one is configured. A malformed config shouldn't break listing the
	// built-in decks, so on error we just proceed with no SRS source.
	var srsCfg config.SRSConfig
	if cfg, err := config.Load(); err == nil {
		srsCfg = cfg.SRS
	}
	return runDeck(stdout, dir, srsCfg, args)
}

// runDeck is the testable core. userDir is the directory user decks are loaded
// from (may be empty or nonexistent). srsCfg carries the flashcard-deck source
// path (empty when unconfigured). With no args it lists decks; with one arg it
// previews that deck; more than one arg is a usage error.
func runDeck(w io.Writer, userDir string, srsCfg config.SRSConfig, args []string) (int, error) {
	switch len(args) {
	case 0:
		return listDecks(w, userDir, srsCfg)
	case 1:
		return previewDeck(w, userDir, srsCfg, args[0])
	default:
		return 2, fmt.Errorf("deck: too many arguments (usage: idle-hands deck [name])")
	}
}

// listDecks renders the resolved catalog: one line per deck with its source,
// name, card count, and description. User decks that shadow a built-in of the
// same name are flagged so the override is never a surprise. When an srs card
// source is configured, the flashcard deck is appended too (loaded live) so the
// listing matches what watch would show.
func listDecks(w io.Writer, userDir string, srsCfg config.SRSConfig) (int, error) {
	cat, err := deck.Catalog(userDir)
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}

	fmt.Fprintln(w, "idle-hands 🙌 — available decks:")
	fmt.Fprintln(w)

	// Width the name column to the longest name for tidy alignment (include the
	// srs deck name so its row lines up too).
	nameW := 4
	for _, e := range cat {
		if len(e.Deck.Name) > nameW {
			nameW = len(e.Deck.Name)
		}
	}
	if len(srs.DeckName) > nameW {
		nameW = len(srs.DeckName)
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

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Preview one with `idle-hands deck <name>`; select one in ~/.idle-hands/config.toml.")
	if userDir != "" {
		fmt.Fprintf(w, "Drop your own *.toml decks in %s.\n", userDir)
	}
	return 0, nil
}

// previewDeck prints every card in the named deck, in order, with its source.
// This is the "see exactly what I'll get" view; it resolves user decks over
// built-ins just as watch does, and handles the special "srs" name by loading
// the configured flashcard source, so the preview always matches runtime.
func previewDeck(w io.Writer, userDir string, srsCfg config.SRSConfig, name string) (int, error) {
	if name == srs.DeckName {
		d, err := srs.LoadDeck(srsCfg.Source)
		if err != nil {
			return 1, fmt.Errorf("deck: %w", err)
		}
		return printDeck(w, d, srs.DeckName), nil
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
