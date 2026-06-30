// Package deck loads the small collections of micro-action cards that
// idle-hands shows during a BUSY window. A deck is just a name plus a list of
// one-line cards (a title and a short body). Three decks ship embedded in the
// binary via go:embed — move (body resets), duck (one rubber-duck question),
// and tidy (close one stray thing) — so the tool works with zero config.
//
// The model is deliberately tiny and stable: M5/M6 will layer user decks from
// ~/.idle-hands/decks/*.toml on top, but the on-disk format is the same TOML a
// Deck marshals to, so loading a user deck is the same code path as loading a
// built-in one.
package deck

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Card is a single micro-action: a short title and a one- or two-sentence body.
// Both are plain text; rendering/styling is internal/card's job, not the
// deck's.
type Card struct {
	Title string `toml:"title"`
	Text  string `toml:"text"`
}

// Deck is a named collection of cards plus a little presentation metadata.
type Deck struct {
	// Name is the deck's stable identifier (e.g. "move"). It is how a user
	// selects the deck in config and how built-in decks are keyed.
	Name string `toml:"name"`
	// Description is a one-line summary shown by the (future) `deck` subcommand.
	Description string `toml:"description"`
	// Emoji is an optional glyph used to flavor the rendered card. May be empty.
	Emoji string `toml:"emoji"`
	// Cards are the micro-actions. A deck with no cards is invalid.
	Cards []Card `toml:"cards"`
}

//go:embed builtin/*.toml
var builtinFS embed.FS

// builtinDir is the directory inside builtinFS holding the embedded decks.
const builtinDir = "builtin"

// Parse decodes a single deck from TOML bytes and validates it. The deck must
// have a non-empty name and at least one card with non-empty title and text.
func Parse(data []byte) (Deck, error) {
	var d Deck
	if err := toml.Unmarshal(data, &d); err != nil {
		return Deck{}, fmt.Errorf("decode deck: %w", err)
	}
	if err := d.validate(); err != nil {
		return Deck{}, err
	}
	return d, nil
}

// validate enforces the minimal invariants the rest of the tool relies on: a
// named deck with at least one fully-populated card.
func (d Deck) validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("deck has no name")
	}
	if len(d.Cards) == 0 {
		return fmt.Errorf("deck %q has no cards", d.Name)
	}
	for i, c := range d.Cards {
		if strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("deck %q card %d has no title", d.Name, i)
		}
		if strings.TrimSpace(c.Text) == "" {
			return fmt.Errorf("deck %q card %q has no text", d.Name, c.Title)
		}
	}
	return nil
}

// BuiltinNames returns the names of the embedded decks, sorted, so callers and
// help output have a stable ordering.
func BuiltinNames() []string {
	decks, err := loadFS(builtinFS, builtinDir)
	if err != nil {
		// The embedded decks are compiled in; a failure here is a build-time
		// bug, not a runtime condition. Surface nothing rather than panic in
		// the hot path — Builtin/Load return the real error when actually used.
		return nil
	}
	names := make([]string, 0, len(decks))
	for name := range decks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Builtin returns the embedded deck with the given name (e.g. "move"). It
// returns an error if no such built-in deck exists.
func Builtin(name string) (Deck, error) {
	decks, err := loadFS(builtinFS, builtinDir)
	if err != nil {
		return Deck{}, err
	}
	d, ok := decks[name]
	if !ok {
		return Deck{}, fmt.Errorf("no built-in deck %q (have: %s)", name, strings.Join(sortedKeys(decks), ", "))
	}
	return d, nil
}

// Builtins returns all embedded decks keyed by name.
func Builtins() (map[string]Deck, error) {
	return loadFS(builtinFS, builtinDir)
}

// loadFS reads and parses every *.toml deck directly under dir in fsys, keyed
// by each deck's declared Name. It is shared by the built-in loader and (later)
// the user-deck loader so both honor identical format and validation rules.
func loadFS(fsys fs.FS, dir string) (map[string]Deck, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read deck dir %q: %w", dir, err)
	}
	out := make(map[string]Deck)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		full := path.Join(dir, e.Name())
		data, err := fs.ReadFile(fsys, full)
		if err != nil {
			return nil, fmt.Errorf("read deck %q: %w", full, err)
		}
		d, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", full, err)
		}
		if _, dup := out[d.Name]; dup {
			return nil, fmt.Errorf("duplicate deck name %q (in %s)", d.Name, full)
		}
		out[d.Name] = d
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no decks found in %q", dir)
	}
	return out, nil
}

// sortedKeys returns the keys of m in sorted order (for stable error messages).
func sortedKeys(m map[string]Deck) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Source records where a resolved deck came from, so `deck` list output can
// label a user deck and flag when it shadows a built-in of the same name.
type Source int

const (
	// SourceBuiltin is a deck embedded in the binary.
	SourceBuiltin Source = iota
	// SourceUser is a deck loaded from ~/.idle-hands/decks/*.toml.
	SourceUser
)

// String renders a Source for list/preview output.
func (s Source) String() string {
	switch s {
	case SourceUser:
		return "user"
	default:
		return "built-in"
	}
}

// Entry is one deck in the resolved catalog: the deck itself, where it came
// from, and (for a user deck) whether it overrides a built-in of the same name.
type Entry struct {
	Deck   Deck
	Source Source
	// Shadows is true when this is a user deck whose name also exists as a
	// built-in; the user deck wins, but the list can say so.
	Shadows bool
}

// LoadDir reads and parses every *.toml deck directly under dir on the real
// filesystem, keyed by each deck's declared Name. A missing dir is not an error
// — it returns an empty map, so a fresh install with no ~/.idle-hands/decks
// just works. A present-but-malformed deck file is a real error the user should
// see and fix. dir may be empty, which is treated as "no user decks".
func LoadDir(dir string) (map[string]Deck, error) {
	if strings.TrimSpace(dir) == "" {
		return map[string]Deck{}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]Deck{}, nil
		}
		return nil, fmt.Errorf("read user deck dir %q: %w", dir, err)
	}
	out := make(map[string]Deck)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		full := filepathJoin(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read user deck %q: %w", full, err)
		}
		d, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", full, err)
		}
		if _, dup := out[d.Name]; dup {
			return nil, fmt.Errorf("duplicate user deck name %q (in %s)", d.Name, full)
		}
		out[d.Name] = d
	}
	return out, nil
}

// Resolve returns the deck named name, preferring a user deck in userDir over a
// built-in of the same name (so a user can override a shipped deck). It also
// reports the Source so callers can tell the user which one they got. An empty
// userDir means "built-ins only". An unknown name is an error listing the names
// that are available.
func Resolve(name, userDir string) (Deck, Source, error) {
	user, err := LoadDir(userDir)
	if err != nil {
		return Deck{}, SourceUser, err
	}
	if d, ok := user[name]; ok {
		return d, SourceUser, nil
	}
	d, err := Builtin(name)
	if err != nil {
		// Re-build a friendlier "available" list that includes user decks.
		return Deck{}, SourceBuiltin, fmt.Errorf("no deck %q (have: %s)", name, strings.Join(availableNames(user), ", "))
	}
	return d, SourceBuiltin, nil
}

// Catalog returns every available deck — built-ins plus user decks in userDir —
// sorted by name, with user decks overriding built-ins of the same name. It is
// what the `deck` list command renders. A malformed user deck is an error.
func Catalog(userDir string) ([]Entry, error) {
	builtins, err := Builtins()
	if err != nil {
		return nil, err
	}
	user, err := LoadDir(userDir)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]Entry, len(builtins)+len(user))
	for name, d := range builtins {
		byName[name] = Entry{Deck: d, Source: SourceBuiltin}
	}
	for name, d := range user {
		_, shadows := builtins[name]
		byName[name] = Entry{Deck: d, Source: SourceUser, Shadows: shadows}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Entry, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out, nil
}

// availableNames returns the sorted union of user deck names and built-in deck
// names, for friendly "have: …" error messages.
func availableNames(user map[string]Deck) []string {
	set := make(map[string]struct{})
	for _, n := range BuiltinNames() {
		set[n] = struct{}{}
	}
	for n := range user {
		set[n] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// filepathJoin joins a directory and filename with the OS separator. It is a
// thin wrapper kept here so this file's only filesystem-path dependency is
// obvious; user-deck paths are real OS paths (unlike the embedded FS, which
// uses path.Join with forward slashes).
func filepathJoin(dir, name string) string {
	return filepath.Join(dir, name)
}
