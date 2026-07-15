package deck

import (
	"os"
	"path/filepath"
	"testing"
)

// writeToml is a small helper that writes a deck file and fails the test on
// error.
func writeToml(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

const onboardingDeck = `name = "welcome"
description = "team onboarding"
cards = [
  { title = "Read the runbook", text = "Skim docs/runbook.md while you wait." },
]
`

// TestDiscoverRepoDeckDirs_UpwardWalk verifies the walk finds every
// .idle-hands/decks up the tree, nearest-first, and stops at the root.
func TestDiscoverRepoDeckDirs_UpwardWalk(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	inner := filepath.Join(outer, "sub", "deep")
	// Decks at outer and inner levels.
	writeToml(t, filepath.Join(outer, ".idle-hands", "decks"), "a.toml", onboardingDeck)
	writeToml(t, filepath.Join(inner, ".idle-hands", "decks"), "b.toml", onboardingDeck)

	dirs, err := DiscoverRepoDeckDirs(inner)
	if err != nil {
		t.Fatalf("DiscoverRepoDeckDirs error: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("found %d dirs, want 2: %v", len(dirs), dirs)
	}
	// Nearest-first: inner before outer.
	wantFirst := filepath.Join(inner, ".idle-hands", "decks")
	if dirs[0] != wantFirst {
		t.Errorf("dirs[0] = %q, want nearest %q", dirs[0], wantFirst)
	}
}

// TestDiscoverRepoDeckDirs_None returns empty when no .idle-hands/decks exists.
func TestDiscoverRepoDeckDirs_None(t *testing.T) {
	dirs, err := DiscoverRepoDeckDirs(t.TempDir())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("want no dirs, got %v", dirs)
	}
}

// TestLoadRepoDecks_NamespaceByFilename checks repo decks are keyed by the file
// base name, not the declared deck Name.
func TestLoadRepoDecks_NamespaceByFilename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".idle-hands", "decks")
	writeToml(t, dir, "onboarding.toml", onboardingDeck)

	decks := LoadRepoDecks([]string{dir})
	if _, ok := decks["onboarding"]; !ok {
		t.Fatalf("expected namespace %q from filename, got keys %v", "onboarding", keysOf(decks))
	}
	if _, ok := decks["welcome"]; ok {
		t.Errorf("repo deck should be keyed by filename, not declared name %q", "welcome")
	}
}

// TestLoadRepoDecks_NearestWins checks that when the same filename appears in
// two dirs, the nearest (first) one is kept.
func TestLoadRepoDecks_NearestWins(t *testing.T) {
	near := filepath.Join(t.TempDir(), "near")
	far := filepath.Join(t.TempDir(), "far")
	writeToml(t, near, "x.toml", `name = "near-deck"
cards = [{ title = "N", text = "near" }]
`)
	writeToml(t, far, "x.toml", `name = "far-deck"
cards = [{ title = "F", text = "far" }]
`)
	decks := LoadRepoDecks([]string{near, far})
	if got := decks["x"].Name; got != "near-deck" {
		t.Errorf("nearest should win: got %q, want near-deck", got)
	}
}

// TestLoadRepoDecks_MalformedSkipped verifies a bad deck file is skipped, never
// fatal, and good decks in the same dir still load.
func TestLoadRepoDecks_MalformedSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "decks")
	writeToml(t, dir, "good.toml", onboardingDeck)
	writeToml(t, dir, "bad.toml", "this is = not [valid toml at all")
	writeToml(t, dir, "empty.toml", `name = "nope"
`) // no cards -> validation error

	decks := LoadRepoDecks([]string{dir})
	if _, ok := decks["good"]; !ok {
		t.Errorf("good deck should still load; got %v", keysOf(decks))
	}
	if _, ok := decks["bad"]; ok {
		t.Errorf("malformed deck should be skipped")
	}
	if _, ok := decks["empty"]; ok {
		t.Errorf("card-less deck should be skipped")
	}
}

// TestResolveWithRepo_Precedence verifies repo > user > builtin.
func TestResolveWithRepo_Precedence(t *testing.T) {
	userDir := t.TempDir()
	repoDir := filepath.Join(t.TempDir(), "decks")

	// A repo deck file named move.toml overrides the built-in "move".
	writeToml(t, repoDir, "move.toml", `name = "move"
cards = [{ title = "Repo move", text = "from the repo" }]
`)
	// Also a user deck named move so we can confirm repo beats user.
	writeToml(t, userDir, "move.toml", `name = "move"
cards = [{ title = "User move", text = "from the user" }]
`)

	d, src, err := ResolveWithRepo("move", userDir, []string{repoDir})
	if err != nil {
		t.Fatalf("ResolveWithRepo error: %v", err)
	}
	if src != SourceRepo {
		t.Errorf("source = %v, want repo", src)
	}
	if d.Cards[0].Title != "Repo move" {
		t.Errorf("resolved wrong deck: %+v", d)
	}

	// With repo discovery off (no dirs), user deck should win over built-in.
	d2, src2, err := ResolveWithRepo("move", userDir, nil)
	if err != nil {
		t.Fatalf("ResolveWithRepo(no repo) error: %v", err)
	}
	if src2 != SourceUser || d2.Cards[0].Title != "User move" {
		t.Errorf("without repo dirs, user should win; got src=%v deck=%+v", src2, d2)
	}
}

// TestCatalogWithRepo_LabelsSource checks the catalog marks a repo deck's
// source and flags shadowing.
func TestCatalogWithRepo_LabelsSource(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "decks")
	writeToml(t, repoDir, "onboarding.toml", onboardingDeck)
	// A repo file that shadows the built-in "move".
	writeToml(t, repoDir, "move.toml", `name = "move"
cards = [{ title = "Repo move", text = "x" }]
`)

	cat, err := CatalogWithRepo(t.TempDir(), []string{repoDir})
	if err != nil {
		t.Fatalf("CatalogWithRepo error: %v", err)
	}
	var sawOnboarding, sawShadowingMove bool
	for _, e := range cat {
		if e.Source == SourceRepo && e.Deck.Cards[0].Title == "Read the runbook" {
			sawOnboarding = true
		}
		if e.Source == SourceRepo && e.Shadows && e.Deck.Cards[0].Title == "Repo move" {
			sawShadowingMove = true
		}
	}
	if !sawOnboarding {
		t.Error("catalog missing repo:onboarding deck")
	}
	if !sawShadowingMove {
		t.Error("catalog should flag repo deck shadowing built-in move")
	}
}

func keysOf(m map[string]Deck) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
