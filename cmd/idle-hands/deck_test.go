package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwrife/idle-hands/internal/config"
)

// noSRS is the zero SRSConfig used by deck-command tests that don't exercise
// the flashcard deck (no card source configured).
var noSRS = config.SRSConfig{}

// writeUserDeck writes a deck TOML into dir for the deck-command tests.
func writeUserDeck(t *testing.T, dir, file, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

// TestRunDeckListBuiltins lists the three built-in decks when there is no user
// deck directory.
func TestRunDeckListBuiltins(t *testing.T) {
	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), noSRS, nil)
	if err != nil {
		t.Fatalf("runDeck list: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{"move", "duck", "tidy", "built-in", "available decks"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q, got:\n%s", want, out)
		}
	}
}

// TestRunDeckListUserOverride shows a user deck, flags it as overriding a
// built-in of the same name, and labels it [user].
func TestRunDeckListUserOverride(t *testing.T) {
	dir := t.TempDir()
	writeUserDeck(t, dir, "move.toml", `name = "move"
description = "custom"
[[cards]]
title = "Push-up"
text = "Drop and give me five."`)

	var buf bytes.Buffer
	if _, err := runDeck(&buf, dir, noSRS, nil); err != nil {
		t.Fatalf("runDeck list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[user]") {
		t.Errorf("expected a [user] deck, got:\n%s", out)
	}
	if !strings.Contains(out, "overrides built-in") {
		t.Errorf("expected override flag, got:\n%s", out)
	}
}

// TestRunDeckPreview prints every card of the named deck in order.
func TestRunDeckPreview(t *testing.T) {
	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), noSRS, []string{"duck"})
	if err != nil {
		t.Fatalf("runDeck preview: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, "duck") || !strings.Contains(out, "[built-in]") {
		t.Errorf("preview header missing, got:\n%s", out)
	}
	// Numbered cards present.
	if !strings.Contains(out, " 1. ") {
		t.Errorf("preview missing numbered cards, got:\n%s", out)
	}
}

// TestRunDeckPreviewUser previews a user deck and labels its source.
func TestRunDeckPreviewUser(t *testing.T) {
	dir := t.TempDir()
	writeUserDeck(t, dir, "focus.toml", `name = "focus"
description = "Tiny refocus prompts."
[[cards]]
title = "One thing"
text = "Name the single next action."`)

	var buf bytes.Buffer
	if _, err := runDeck(&buf, dir, noSRS, []string{"focus"}); err != nil {
		t.Fatalf("runDeck preview user: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[user]") {
		t.Errorf("expected [user] source, got:\n%s", out)
	}
	if !strings.Contains(out, "One thing") || !strings.Contains(out, "Name the single next action.") {
		t.Errorf("user card not shown, got:\n%s", out)
	}
}

// TestRunDeckUnknownErrors returns a non-zero code and an error for an unknown
// deck name.
func TestRunDeckUnknownErrors(t *testing.T) {
	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), noSRS, []string{"nope"})
	if err == nil {
		t.Fatal("runDeck(nope) expected error, got nil")
	}
	if code == 0 {
		t.Error("runDeck(nope) code = 0, want non-zero")
	}
}

// TestRunDeckTooManyArgs is a usage error.
func TestRunDeckTooManyArgs(t *testing.T) {
	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), noSRS, []string{"a", "b"})
	if err == nil {
		t.Fatal("runDeck(a b) expected error, got nil")
	}
	if code != 2 {
		t.Errorf("code = %d, want 2 (usage)", code)
	}
}

// writeSRSSource writes a Markdown Q/A card file into dir and returns its path,
// for the flashcard-deck command tests.
func writeSRSSource(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "cards.md")
	body := "Q: Capital of France?\nA: Paris\nQ: 2 + 2?\nA: Four\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write srs source: %v", err)
	}
	return path
}

// TestRunDeckListIncludesSRS shows the flashcard deck in the listing when a
// card source is configured, labeled [srs] with its card count.
func TestRunDeckListIncludesSRS(t *testing.T) {
	dir := t.TempDir()
	srsCfg := config.SRSConfig{Source: writeSRSSource(t, dir)}

	var buf bytes.Buffer
	if _, err := runDeck(&buf, t.TempDir(), srsCfg, nil); err != nil {
		t.Fatalf("runDeck list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[srs]") {
		t.Errorf("listing missing the srs deck, got:\n%s", out)
	}
	if !strings.Contains(out, "2 cards") {
		t.Errorf("listing missing srs card count, got:\n%s", out)
	}
}

// TestRunDeckListSRSAbsentWhenUnconfigured confirms the srs row is omitted when
// no card source is set (so the listing doesn't advertise an empty deck).
func TestRunDeckListSRSAbsentWhenUnconfigured(t *testing.T) {
	var buf bytes.Buffer
	if _, err := runDeck(&buf, t.TempDir(), noSRS, nil); err != nil {
		t.Fatalf("runDeck list: %v", err)
	}
	if strings.Contains(buf.String(), "[srs]") {
		t.Errorf("unconfigured srs deck should not appear, got:\n%s", buf.String())
	}
}

// TestRunDeckPreviewSRS previews the flashcard deck by loading the configured
// source, printing the fronts and backs.
func TestRunDeckPreviewSRS(t *testing.T) {
	dir := t.TempDir()
	srsCfg := config.SRSConfig{Source: writeSRSSource(t, dir)}

	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), srsCfg, []string{"srs"})
	if err != nil {
		t.Fatalf("runDeck preview srs: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, "Capital of France?") || !strings.Contains(out, "Paris") {
		t.Errorf("srs preview missing cards, got:\n%s", out)
	}
}

// TestRunDeckPreviewSRSUnconfigured errors clearly when the srs deck is
// previewed but no card source is configured.
func TestRunDeckPreviewSRSUnconfigured(t *testing.T) {
	var buf bytes.Buffer
	code, err := runDeck(&buf, t.TempDir(), noSRS, []string{"srs"})
	if err == nil {
		t.Fatal("runDeck(srs) with no source expected error, got nil")
	}
	if code == 0 {
		t.Error("code = 0, want non-zero")
	}
}
