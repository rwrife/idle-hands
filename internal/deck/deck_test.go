package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinsLoad verifies every embedded deck parses, validates, and is keyed
// by its declared name — this is the build-time contract that the go:embed
// decks are always shippable.
func TestBuiltinsLoad(t *testing.T) {
	decks, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins() error: %v", err)
	}
	for _, want := range []string{"move", "duck", "tidy"} {
		d, ok := decks[want]
		if !ok {
			t.Errorf("missing built-in deck %q", want)
			continue
		}
		if d.Name != want {
			t.Errorf("deck keyed under %q has Name %q", want, d.Name)
		}
		if len(d.Cards) == 0 {
			t.Errorf("deck %q has no cards", want)
		}
		for i, c := range d.Cards {
			if strings.TrimSpace(c.Title) == "" {
				t.Errorf("deck %q card %d has empty title", want, i)
			}
			if strings.TrimSpace(c.Text) == "" {
				t.Errorf("deck %q card %d has empty text", want, i)
			}
		}
	}
}

// TestBuiltinNames checks the names are returned sorted and cover the trio.
func TestBuiltinNames(t *testing.T) {
	got := BuiltinNames()
	want := []string{"duck", "move", "tidy"} // sorted
	if len(got) != len(want) {
		t.Fatalf("BuiltinNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BuiltinNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestBuiltinByName returns a known deck and errors on an unknown one.
func TestBuiltinByName(t *testing.T) {
	d, err := Builtin("duck")
	if err != nil {
		t.Fatalf("Builtin(duck) error: %v", err)
	}
	if d.Name != "duck" {
		t.Errorf("Builtin(duck).Name = %q", d.Name)
	}
	if _, err := Builtin("nope"); err == nil {
		t.Error("Builtin(nope) expected error, got nil")
	}
}

// TestParseValidation rejects malformed decks and accepts good ones.
func TestParseValidation(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantErr bool
	}{
		{
			name: "valid",
			toml: `name = "x"
[[cards]]
title = "t"
text = "body"`,
			wantErr: false,
		},
		{
			name:    "no name",
			toml:    "[[cards]]\ntitle = \"t\"\ntext = \"b\"",
			wantErr: true,
		},
		{
			name:    "no cards",
			toml:    `name = "x"`,
			wantErr: true,
		},
		{
			name: "card missing text",
			toml: `name = "x"
[[cards]]
title = "t"`,
			wantErr: true,
		},
		{
			name: "card missing title",
			toml: `name = "x"
[[cards]]
text = "b"`,
			wantErr: true,
		},
		{
			name:    "garbage",
			toml:    "this is = = not toml",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml))
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// writeDeck is a test helper that writes a deck TOML file into dir.
func writeDeck(t *testing.T, dir, file, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

const userFocusDeck = `name = "focus"
description = "Tiny refocus prompts."
emoji = "🎯"
[[cards]]
title = "One thing"
text = "Name the single next action."`

// TestLoadDirEmptyOrMissing returns an empty map (not an error) for an empty
// path and for a directory that does not exist — a fresh install with no user
// decks must just work.
func TestLoadDirEmptyOrMissing(t *testing.T) {
	got, err := LoadDir("")
	if err != nil {
		t.Fatalf("LoadDir(\"\") error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadDir(\"\") = %v, want empty", got)
	}

	missing := filepath.Join(t.TempDir(), "nope")
	got, err = LoadDir(missing)
	if err != nil {
		t.Fatalf("LoadDir(missing) error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadDir(missing) = %v, want empty", got)
	}
}

// TestLoadDirReadsDecks parses *.toml files in a dir, keys them by Name, and
// ignores non-toml files.
func TestLoadDirReadsDecks(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "focus.toml", userFocusDeck)
	writeDeck(t, dir, "notes.txt", "ignored, not a deck")

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("LoadDir loaded %d decks, want 1", len(got))
	}
	d, ok := got["focus"]
	if !ok {
		t.Fatalf("missing user deck %q", "focus")
	}
	if len(d.Cards) != 1 || d.Cards[0].Title != "One thing" {
		t.Errorf("unexpected deck contents: %+v", d)
	}
}

// TestLoadDirMalformedIsError surfaces a malformed user deck rather than
// silently dropping it — the user should see their typo.
func TestLoadDirMalformedIsError(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "bad.toml", `name = "bad"`) // no cards
	if _, err := LoadDir(dir); err == nil {
		t.Error("LoadDir(malformed) expected error, got nil")
	}
}

// TestLoadDirDuplicateNames rejects two files declaring the same deck name.
func TestLoadDirDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "a.toml", userFocusDeck)
	writeDeck(t, dir, "b.toml", userFocusDeck)
	if _, err := LoadDir(dir); err == nil {
		t.Error("LoadDir(duplicate names) expected error, got nil")
	}
}

// TestResolvePrefersUserDeck returns a user deck over a built-in of the same
// name, and reports the source.
func TestResolvePrefersUserDeck(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "move.toml", `name = "move"
description = "custom"
[[cards]]
title = "Push-up"
text = "Drop and give me five."`)

	d, src, err := Resolve("move", dir)
	if err != nil {
		t.Fatalf("Resolve(move) error: %v", err)
	}
	if src != SourceUser {
		t.Errorf("Resolve(move) source = %v, want user", src)
	}
	if len(d.Cards) != 1 || d.Cards[0].Title != "Push-up" {
		t.Errorf("Resolve(move) returned built-in, want user deck: %+v", d)
	}
}

// TestResolveFallsBackToBuiltin returns a built-in when no user deck shadows it.
func TestResolveFallsBackToBuiltin(t *testing.T) {
	d, src, err := Resolve("duck", t.TempDir())
	if err != nil {
		t.Fatalf("Resolve(duck) error: %v", err)
	}
	if src != SourceBuiltin {
		t.Errorf("Resolve(duck) source = %v, want built-in", src)
	}
	if d.Name != "duck" {
		t.Errorf("Resolve(duck).Name = %q", d.Name)
	}
}

// TestResolveUnknownErrors errors on a name that is neither a user deck nor a
// built-in, and the message lists what's available.
func TestResolveUnknownErrors(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "focus.toml", userFocusDeck)
	_, _, err := Resolve("nope", dir)
	if err == nil {
		t.Fatal("Resolve(nope) expected error, got nil")
	}
	for _, want := range []string{"duck", "focus", "move", "tidy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Resolve(nope) error %q missing available deck %q", err, want)
		}
	}
}

// TestCatalogMergesAndShadows lists built-ins plus user decks, sorted, flagging
// a user deck that overrides a built-in of the same name.
func TestCatalogMergesAndShadows(t *testing.T) {
	dir := t.TempDir()
	writeDeck(t, dir, "focus.toml", userFocusDeck) // new
	writeDeck(t, dir, "move.toml", `name = "move"
description = "custom"
[[cards]]
title = "X"
text = "y"`) // shadows built-in

	cat, err := Catalog(dir)
	if err != nil {
		t.Fatalf("Catalog error: %v", err)
	}

	byName := make(map[string]Entry, len(cat))
	var names []string
	for _, e := range cat {
		byName[e.Deck.Name] = e
		names = append(names, e.Deck.Name)
	}

	// Sorted order.
	want := []string{"duck", "focus", "move", "tidy"}
	if len(names) != len(want) {
		t.Fatalf("Catalog names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("Catalog names[%d] = %q, want %q", i, names[i], want[i])
		}
	}

	if e := byName["focus"]; e.Source != SourceUser || e.Shadows {
		t.Errorf("focus entry = %+v, want user/non-shadow", e)
	}
	if e := byName["move"]; e.Source != SourceUser || !e.Shadows {
		t.Errorf("move entry = %+v, want user/shadow", e)
	}
	if e := byName["duck"]; e.Source != SourceBuiltin {
		t.Errorf("duck entry = %+v, want built-in", e)
	}
}
