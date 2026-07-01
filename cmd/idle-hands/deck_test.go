package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	code, err := runDeck(&buf, t.TempDir(), nil)
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
	if _, err := runDeck(&buf, dir, nil); err != nil {
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
	code, err := runDeck(&buf, t.TempDir(), []string{"duck"})
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
	if _, err := runDeck(&buf, dir, []string{"focus"}); err != nil {
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
	code, err := runDeck(&buf, t.TempDir(), []string{"nope"})
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
	code, err := runDeck(&buf, t.TempDir(), []string{"a", "b"})
	if err == nil {
		t.Fatal("runDeck(a b) expected error, got nil")
	}
	if code != 2 {
		t.Errorf("code = %d, want 2 (usage)", code)
	}
}
