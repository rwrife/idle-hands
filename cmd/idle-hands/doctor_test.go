package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

func doctorTestPaths(t *testing.T) doctorPaths {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "home", ".idle-hands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return doctorPaths{
		config: filepath.Join(dir, "config.toml"), state: filepath.Join(dir, "state.json"),
		focus: filepath.Join(dir, "focus.json"), userDecks: filepath.Join(dir, "decks"),
		cwd: cwd,
	}
}

func doctorWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorResolvesRuntime(t *testing.T) {
	p := doctorTestPaths(t)
	// Team decks are keyed by file namespace, not declared name. A team deck
	// overrides the user deck and the built-in with that same selector.
	card := "name = 'declared-name'\n[[cards]]\ntitle = 'Reset'\ntext = 'Take a break'\n"
	doctorWrite(t, filepath.Join(p.userDecks, "custom.toml"), strings.ReplaceAll(card, "declared-name", "custom"))
	doctorWrite(t, filepath.Join(p.userDecks, "move.toml"), strings.ReplaceAll(card, "declared-name", "move"))
	project := filepath.Join(p.cwd, "project")
	doctorWrite(t, filepath.Join(project, ".idle-hands", "decks", "move.toml"), card)
	p.cwd = project
	doctorWrite(t, p.config, "deck = 'move'\nbusy_threshold = '7s'\n[quiet_hours]\nstart = '22:00'\nend = '07:00'\n")
	doctorWrite(t, p.focus, `{"version":1,"until":"2026-11-20T00:00:00Z"}`)
	state := `{"version":1,"days":{"2026-11-19":{"windows":2,"seconds":42}}}`
	doctorWrite(t, p.state, state)
	now := time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC)

	r := resolveDoctor(p, now)
	if len(r.Errors) != 0 || r.BusyThreshold != "7s" || !r.QuietHoursActive || !r.FocusSafeActive || !r.StateWritable {
		t.Fatalf("unexpected report: %+v", r)
	}
	if r.EffectiveConfig["busy_threshold"] != "7s" || r.ConfigPath != p.config || r.StatePath != p.state {
		t.Fatalf("incorrect resolved config/paths: %+v", r)
	}
	if got := r.Decks["team"]; len(got) != 1 || got[0].Name != "move" || got[0].Cards != 1 || !got[0].Shadows {
		t.Fatalf("team override not resolved: %+v", got)
	}
	if got := r.Decks["user"]; len(got) != 1 || got[0].Name != "custom" {
		t.Fatalf("user overrides not resolved: %+v", got)
	}
	if len(r.Decks["embedded"]) != len(deck.BuiltinNames())-1 {
		t.Fatalf("shadowed built-in still listed: %+v", r.Decks)
	}
	got, err := os.ReadFile(p.state)
	if err != nil || string(got) != state {
		t.Fatalf("state was changed: %q, %v", got, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(p.state), ".doctor-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("probe left files behind: %v, %v", leftovers, err)
	}
}

func TestDoctorDefaultsJSONAndHumanOutput(t *testing.T) {
	p := doctorTestPaths(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	var b bytes.Buffer
	code, err := runDoctor(&b, p, now, true)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v: %s", code, err, &b)
	}
	var r doctorReport
	decoder := json.NewDecoder(&b)
	if err := decoder.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Event != "doctor" || !r.TS.Equal(now) || r.BusyThreshold != "20s" || r.QuietHoursActive || r.FocusSafeActive {
		t.Fatalf("unexpected defaults: %+v", r)
	}
	if r.EffectiveConfig["deck"] != "move" || len(r.Warnings) == 0 {
		t.Fatalf("missing config defaults/warning: %+v", r)
	}
	if _, err := os.Stat(p.state); !os.IsNotExist(err) {
		t.Fatalf("doctor created state: %v", err)
	}
	b.Reset()
	code, err = runDoctor(&b, p, now, false)
	if code != 0 || err != nil {
		t.Fatalf("human report: code=%d err=%v", code, err)
	}
	for _, want := range []string{p.config, p.state, "embedded decks", "user decks (0)", "team decks (0)", "Busy threshold: 20s", "Focus-safe active: false"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in %s", want, &b)
		}
	}
}

func TestDoctorHardErrors(t *testing.T) {
	for _, name := range []string{"missing config directory", "state is directory", "malformed config", "malformed state", "malformed focus", "unknown deck", "invalid user deck", "missing srs", "missing hooks"} {
		t.Run(name, func(t *testing.T) {
			p := doctorTestPaths(t)
			switch name {
			case "missing config directory":
				p.config = filepath.Join(p.cwd, "missing", "config.toml")
				p.state = filepath.Join(p.cwd, "missing", "state.json")
			case "state is directory":
				if err := os.Mkdir(p.state, 0o755); err != nil {
					t.Fatal(err)
				}
			case "malformed config":
				doctorWrite(t, p.config, "buzy_threshold = '3s'")
			case "malformed state":
				doctorWrite(t, p.state, "{")
			case "malformed focus":
				doctorWrite(t, p.focus, "{")
			case "unknown deck":
				doctorWrite(t, p.config, "deck = 'nonexistent'")
			case "invalid user deck":
				doctorWrite(t, filepath.Join(p.userDecks, "invalid.toml"), "name = 'empty'")
			case "missing srs":
				doctorWrite(t, p.config, "deck = 'srs'")
			case "missing hooks":
				doctorWrite(t, p.config, "deck = 'hook'")
			}
			var b bytes.Buffer
			code, err := runDoctor(&b, p, time.Now(), true)
			if code != 1 || err != nil {
				t.Fatalf("code=%d err=%v: %s", code, err, &b)
			}
			var r doctorReport
			if err := json.Unmarshal(b.Bytes(), &r); err != nil || len(r.Errors) == 0 {
				t.Fatalf("no structured errors: %s, %v", &b, err)
			}
			if name == "malformed config" && r.EffectiveConfig != nil {
				t.Fatal("invalid config was replaced with defaults")
			}
		})
	}
}

func TestDoctorDiscoveryDisabledAndHooksNotExecuted(t *testing.T) {
	p := doctorTestPaths(t)
	doctorWrite(t, filepath.Join(p.cwd, ".idle-hands", "decks", "team.toml"), "name='team'\n[[cards]]\ntitle='a'\ntext='b'\n")
	// An invalid executable would fail if doctor tried to run it.
	doctorWrite(t, p.config, "deck='hook'\n[[hooks]]\nname='safe'\ncmd=['idle-hands-doctor-do-not-execute']\n[decks]\nrepo_discovery=false\n")
	r := resolveDoctor(p, time.Now())
	if len(r.Errors) != 0 || len(r.TeamDeckPaths) != 0 || len(r.Decks["team"]) != 0 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if !reflect.DeepEqual(r.EffectiveConfig["decks"], map[string]bool{"repo_discovery": false}) {
		t.Fatalf("discovery setting lost: %+v", r.EffectiveConfig)
	}
}

func TestDoctorReadOnlyState(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root can write read-only files")
	}
	p := doctorTestPaths(t)
	doctorWrite(t, p.state, `{"version":1,"days":{}}`)
	if err := os.Chmod(p.state, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(p.state, 0o600); err != nil {
			t.Error(err)
		}
	})
	var b bytes.Buffer
	code, err := runDoctor(&b, p, time.Now(), true)
	if code != 1 || err != nil {
		t.Fatalf("read-only state: code=%d err=%v: %s", code, err, &b)
	}
	var r doctorReport
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.StateWritable || len(r.Errors) == 0 || !strings.Contains(strings.Join(r.Errors, "\n"), "state writability") {
		t.Fatalf("missing writability error: %+v", r)
	}
}

func TestDoctorCommandRouting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	var b bytes.Buffer
	old := stdout
	stdout = &b
	defer func() { stdout = old }()
	if code := run([]string{"doctor", "--json"}); code != 1 {
		t.Fatalf("missing config dir: code=%d, want 1", code)
	}
	if !json.Valid(b.Bytes()) {
		t.Fatalf("not JSON: %s", &b)
	}
	if code, err := cmdDoctor([]string{"--bogus"}); code != 2 || err == nil {
		t.Fatalf("invalid flags: code=%d err=%v", code, err)
	}
}
