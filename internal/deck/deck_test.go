package deck

import (
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
