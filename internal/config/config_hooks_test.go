package config

import (
	"strings"
	"testing"
	"time"
)

// TestParseHooksValues checks [[hooks]] blocks and hook_timeout resolve onto
// Config.Hooks in order, with a present hook_timeout taken verbatim.
func TestParseHooksValues(t *testing.T) {
	got, err := Parse([]byte(`
deck = "hook"
hook_timeout = "5s"

[[hooks]]
name = "fetch"
cmd = ["git", "fetch", "--quiet"]

[[hooks]]
name = "vet"
cmd = ["go", "vet", "./..."]
`))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Deck != "hook" {
		t.Errorf("Deck = %q, want hook", got.Deck)
	}
	if got.Hooks.Timeout != 5*time.Second {
		t.Errorf("Hooks.Timeout = %s, want 5s", got.Hooks.Timeout)
	}
	if len(got.Hooks.Specs) != 2 {
		t.Fatalf("len(Hooks.Specs) = %d, want 2", len(got.Hooks.Specs))
	}
	if got.Hooks.Specs[0].Name != "fetch" || got.Hooks.Specs[1].Name != "vet" {
		t.Errorf("hook order/names wrong: %+v", got.Hooks.Specs)
	}
	if len(got.Hooks.Specs[0].Cmd) != 3 || got.Hooks.Specs[0].Cmd[0] != "git" {
		t.Errorf("Hooks.Specs[0].Cmd = %v", got.Hooks.Specs[0].Cmd)
	}
}

// TestParseHooksDefaults checks that with hooks but no hook_timeout the default
// timeout is applied, and that a config with no hooks leaves Specs empty (and is
// not itself an error).
func TestParseHooksDefaults(t *testing.T) {
	got, err := Parse([]byte(`
[[hooks]]
name = "fetch"
cmd = ["git", "fetch"]
`))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Hooks.Timeout != DefaultHookTimeout {
		t.Errorf("Hooks.Timeout = %s, want default %s", got.Hooks.Timeout, DefaultHookTimeout)
	}

	none, err := Parse([]byte(`deck = "move"`))
	if err != nil {
		t.Fatalf("Parse(no hooks) error = %v", err)
	}
	if len(none.Hooks.Specs) != 0 {
		t.Errorf("no-hooks config should have empty Specs, got %+v", none.Hooks.Specs)
	}
}

// TestParseHooksMalformed checks the strict-config behavior: missing name,
// missing/empty cmd, duplicate name, and a bad hook_timeout are all errors.
func TestParseHooksMalformed(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "missing name",
			toml: "[[hooks]]\ncmd = [\"git\", \"fetch\"]\n",
			want: "name is required",
		},
		{
			name: "missing cmd",
			toml: "[[hooks]]\nname = \"fetch\"\n",
			want: "cmd is required",
		},
		{
			name: "empty cmd program",
			toml: "[[hooks]]\nname = \"fetch\"\ncmd = [\"\"]\n",
			want: "cmd is required",
		},
		{
			name: "duplicate name",
			toml: "[[hooks]]\nname = \"x\"\ncmd = [\"a\"]\n[[hooks]]\nname = \"x\"\ncmd = [\"b\"]\n",
			want: "duplicate hook name",
		},
		{
			name: "bad hook_timeout",
			toml: "hook_timeout = \"soon\"\n[[hooks]]\nname = \"x\"\ncmd = [\"a\"]\n",
			want: "hook_timeout",
		},
		{
			name: "nonpositive hook_timeout",
			toml: "hook_timeout = \"0s\"\n[[hooks]]\nname = \"x\"\ncmd = [\"a\"]\n",
			want: "must be positive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml))
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse(%q) error = %v, want containing %q", tc.name, err, tc.want)
			}
		})
	}
}
