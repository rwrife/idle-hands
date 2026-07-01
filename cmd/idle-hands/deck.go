package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
)

// cmdDeck implements `idle-hands deck`:
//
//	idle-hands deck            list every available deck (built-in + user)
//	idle-hands deck <name>     preview every card in the named deck
//
// It exists so a user can see what decks they have (including their own under
// ~/.idle-hands/decks) and eyeball the exact cards a deck will surface before
// pointing config at it. Output is the command's real result, so it goes to
// stdout; the user-deck directory is resolved from the home dir but injected
// into the testable core so tests can use a temp dir without a real $HOME.
func cmdDeck(args []string) (int, error) {
	dir, err := config.DecksDir()
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}
	return runDeck(stdout, dir, args)
}

// runDeck is the testable core. userDir is the directory user decks are loaded
// from (may be empty or nonexistent). With no args it lists decks; with one arg
// it previews that deck; more than one arg is a usage error.
func runDeck(w io.Writer, userDir string, args []string) (int, error) {
	switch len(args) {
	case 0:
		return listDecks(w, userDir)
	case 1:
		return previewDeck(w, userDir, args[0])
	default:
		return 2, fmt.Errorf("deck: too many arguments (usage: idle-hands deck [name])")
	}
}

// listDecks renders the resolved catalog: one line per deck with its source,
// name, card count, and description. User decks that shadow a built-in of the
// same name are flagged so the override is never a surprise.
func listDecks(w io.Writer, userDir string) (int, error) {
	cat, err := deck.Catalog(userDir)
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}

	fmt.Fprintln(w, "idle-hands 🙌 — available decks:")
	fmt.Fprintln(w)

	// Width the name column to the longest name for tidy alignment.
	nameW := 4
	for _, e := range cat {
		if len(e.Deck.Name) > nameW {
			nameW = len(e.Deck.Name)
		}
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

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Preview one with `idle-hands deck <name>`; select one in ~/.idle-hands/config.toml.")
	if userDir != "" {
		fmt.Fprintf(w, "Drop your own *.toml decks in %s.\n", userDir)
	}
	return 0, nil
}

// previewDeck prints every card in the named deck, in order, with its source.
// This is the "see exactly what I'll get" view; it resolves user decks over
// built-ins just as watch does, so the preview matches runtime behavior.
func previewDeck(w io.Writer, userDir, name string) (int, error) {
	d, src, err := deck.Resolve(name, userDir)
	if err != nil {
		return 1, fmt.Errorf("deck: %w", err)
	}

	emoji := d.Emoji
	if emoji != "" {
		emoji += " "
	}
	fmt.Fprintf(w, "%s%s  [%s] — %s\n", emoji, d.Name, src, d.Description)
	fmt.Fprintf(w, "%s\n", strings.Repeat("─", 48))

	for i, c := range d.Cards {
		fmt.Fprintf(w, "%2d. %s\n", i+1, c.Title)
		fmt.Fprintf(w, "    %s\n", c.Text)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s · one of these per busy window, never twice in a row.\n",
		countNoun(len(d.Cards), "card", "cards"))
	return 0, nil
}
