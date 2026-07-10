package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	got := Default()
	if got.Deck != DefaultDeck {
		t.Errorf("Default().Deck = %q, want %q", got.Deck, DefaultDeck)
	}
	if got.BusyThreshold != DefaultBusyThreshold {
		t.Errorf("Default().BusyThreshold = %s, want %s", got.BusyThreshold, DefaultBusyThreshold)
	}
	if got.Quiet.Enabled() {
		t.Errorf("Default().Quiet should be disabled, got %+v", got.Quiet)
	}
	if got.SRS.Reveal != DefaultSRSReveal {
		t.Errorf("Default().SRS.Reveal = %s, want %s", got.SRS.Reveal, DefaultSRSReveal)
	}
	if got.SRS.Spacing != DefaultSRSSpacing {
		t.Errorf("Default().SRS.Spacing = %d, want %d", got.SRS.Spacing, DefaultSRSSpacing)
	}
	if got.SRS.Source != "" {
		t.Errorf("Default().SRS.Source = %q, want empty", got.SRS.Source)
	}
	if got.DuckDiff.Timeout != DefaultDuckDiffTimeout {
		t.Errorf("Default().DuckDiff.Timeout = %s, want %s", got.DuckDiff.Timeout, DefaultDuckDiffTimeout)
	}
	if got.DuckDiff.Model != "" || got.DuckDiff.URL != "" {
		t.Errorf("Default().DuckDiff model/url should be empty (defaults applied by duckdiff), got %+v", got.DuckDiff)
	}
}

// TestParseSRSValues checks the flashcard-deck keys resolve onto Config.SRS,
// and that omitted srs_reveal/srs_spacing fall back to their defaults while a
// present srs_source is taken verbatim.
func TestParseSRSValues(t *testing.T) {
	got, err := Parse([]byte(`
deck = "srs"
srs_source = "/home/me/.idle-hands/cards.md"
srs_reveal = "8s"
srs_spacing = 5
`))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Deck != "srs" {
		t.Errorf("Deck = %q, want srs", got.Deck)
	}
	if got.SRS.Source != "/home/me/.idle-hands/cards.md" {
		t.Errorf("SRS.Source = %q", got.SRS.Source)
	}
	if got.SRS.Reveal != 8*time.Second {
		t.Errorf("SRS.Reveal = %s, want 8s", got.SRS.Reveal)
	}
	if got.SRS.Spacing != 5 {
		t.Errorf("SRS.Spacing = %d, want 5", got.SRS.Spacing)
	}
}

// TestParseSRSExpandsHomeInSource verifies a leading "~/" in srs_source is
// expanded to the user's home directory, so the documented ~ form works.
func TestParseSRSExpandsHomeInSource(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	got, err := Parse([]byte("deck = \"srs\"\nsrs_source = \"~/.idle-hands/cards.md\"\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	want := filepath.Join(home, ".idle-hands", "cards.md")
	if got.SRS.Source != want {
		t.Errorf("SRS.Source = %q, want expanded %q", got.SRS.Source, want)
	}
}

// TestParseSRSDefaults confirms that selecting the srs deck without tuning keys
// keeps the reveal/spacing defaults, and that srs_spacing = 0 is a valid,
// explicit "only avoid immediate repeats" setting.
func TestParseSRSDefaults(t *testing.T) {
	got, err := Parse([]byte("deck = \"srs\"\nsrs_spacing = 0\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.SRS.Reveal != DefaultSRSReveal {
		t.Errorf("SRS.Reveal = %s, want default %s", got.SRS.Reveal, DefaultSRSReveal)
	}
	if got.SRS.Spacing != 0 {
		t.Errorf("SRS.Spacing = %d, want explicit 0", got.SRS.Spacing)
	}
}

// TestParseDuckDiffValues checks the duckdiff keys resolve onto Config.DuckDiff,
// taking the model/url verbatim and parsing the timeout duration.
func TestParseDuckDiffValues(t *testing.T) {
	got, err := Parse([]byte(`
deck = "duckdiff"
duckdiff_model = "codellama"
duckdiff_url = "http://127.0.0.1:11434/api/generate"
duckdiff_timeout = "7s"
`))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Deck != "duckdiff" {
		t.Errorf("Deck = %q, want duckdiff", got.Deck)
	}
	if got.DuckDiff.Model != "codellama" {
		t.Errorf("DuckDiff.Model = %q, want codellama", got.DuckDiff.Model)
	}
	if got.DuckDiff.URL != "http://127.0.0.1:11434/api/generate" {
		t.Errorf("DuckDiff.URL = %q", got.DuckDiff.URL)
	}
	if got.DuckDiff.Timeout != 7*time.Second {
		t.Errorf("DuckDiff.Timeout = %s, want 7s", got.DuckDiff.Timeout)
	}
}

// TestParseDuckDiffDefaults confirms omitted duckdiff keys keep the default
// timeout and leave model/url empty (so duckdiff applies its own defaults).
func TestParseDuckDiffDefaults(t *testing.T) {
	got, err := Parse([]byte(`deck = "duckdiff"` + "\n"))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.DuckDiff.Timeout != DefaultDuckDiffTimeout {
		t.Errorf("DuckDiff.Timeout = %s, want default %s", got.DuckDiff.Timeout, DefaultDuckDiffTimeout)
	}
	if got.DuckDiff.Model != "" || got.DuckDiff.URL != "" {
		t.Errorf("omitted duckdiff model/url should stay empty, got %+v", got.DuckDiff)
	}
}

// TestParseDuckDiffBadTimeout rejects a non-duration or non-positive timeout.
func TestParseDuckDiffBadTimeout(t *testing.T) {
	for _, bad := range []string{"duckdiff_timeout = \"soon\"", "duckdiff_timeout = \"0s\"", "duckdiff_timeout = \"-3s\""} {
		if _, err := Parse([]byte(bad + "\n")); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", bad)
		}
	}
}

func TestLoadFileMissingReturnsDefault(t *testing.T) {
	// A path that does not exist must yield defaults, not an error.
	missing := filepath.Join(t.TempDir(), "nope", "config.toml")
	got, err := LoadFile(missing)
	if err != nil {
		t.Fatalf("LoadFile(missing) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("LoadFile(missing) = %+v, want Default() %+v", got, Default())
	}
}

func TestLoadFileReadsValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
deck = "duck"
busy_threshold = "45s"

[quiet_hours]
start = "22:30"
end = "07:15"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile error = %v", err)
	}
	if got.Deck != "duck" {
		t.Errorf("Deck = %q, want duck", got.Deck)
	}
	if got.BusyThreshold != 45*time.Second {
		t.Errorf("BusyThreshold = %s, want 45s", got.BusyThreshold)
	}
	if !got.Quiet.Enabled() {
		t.Fatal("Quiet should be enabled")
	}
	wantStart, _ := ParseClock("22:30")
	wantEnd, _ := ParseClock("07:15")
	if got.Quiet.Start != wantStart || got.Quiet.End != wantEnd {
		t.Errorf("Quiet = %s→%s, want 22:30→07:15", got.Quiet.Start, got.Quiet.End)
	}
}

func TestParseDefaultsForOmittedFields(t *testing.T) {
	// Only deck set; threshold and quiet hours should fall back to defaults.
	got, err := Parse([]byte(`deck = "tidy"`))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Deck != "tidy" {
		t.Errorf("Deck = %q, want tidy", got.Deck)
	}
	if got.BusyThreshold != DefaultBusyThreshold {
		t.Errorf("BusyThreshold = %s, want default %s", got.BusyThreshold, DefaultBusyThreshold)
	}
	if got.Quiet.Enabled() {
		t.Errorf("Quiet should be disabled, got %+v", got.Quiet)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		toml string
	}{
		{"unknown key", `buzy_threshold = "20s"`},
		{"bad duration", `busy_threshold = "soon"`},
		{"zero duration", `busy_threshold = "0s"`},
		{"negative duration", `busy_threshold = "-5s"`},
		{"bad srs_reveal", `srs_reveal = "soon"`},
		{"zero srs_reveal", `srs_reveal = "0s"`},
		{"negative srs_reveal", `srs_reveal = "-3s"`},
		{"negative srs_spacing", `srs_spacing = -1`},
		{"quiet start only", "[quiet_hours]\nstart = \"22:00\""},
		{"quiet end only", "[quiet_hours]\nend = \"07:00\""},
		{"quiet equal", "[quiet_hours]\nstart = \"09:00\"\nend = \"09:00\""},
		{"bad hour", "[quiet_hours]\nstart = \"24:00\"\nend = \"07:00\""},
		{"bad minute", "[quiet_hours]\nstart = \"22:60\"\nend = \"07:00\""},
		{"not hhmm", "[quiet_hours]\nstart = \"2200\"\nend = \"07:00\""},
		{"garbage toml", `deck = `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.toml)); err == nil {
				t.Fatalf("Parse(%q) = nil error, want error", tc.toml)
			}
		})
	}
}

func TestQuietHoursContainsSameDay(t *testing.T) {
	// 09:00 → 17:00 working-hours-style window.
	q := mustQuiet(t, "09:00", "17:00")
	cases := []struct {
		hhmm string
		want bool
	}{
		{"08:59", false},
		{"09:00", true}, // inclusive start
		{"12:30", true},
		{"16:59", true},
		{"17:00", false}, // exclusive end
		{"23:00", false},
	}
	for _, c := range cases {
		if got := q.Contains(at(t, c.hhmm)); got != c.want {
			t.Errorf("Contains(%s) = %v, want %v", c.hhmm, got, c.want)
		}
	}
}

func TestQuietHoursContainsWrapMidnight(t *testing.T) {
	// 22:00 → 07:00, the common overnight window, must wrap past midnight.
	q := mustQuiet(t, "22:00", "07:00")
	cases := []struct {
		hhmm string
		want bool
	}{
		{"21:59", false},
		{"22:00", true}, // inclusive start
		{"23:30", true},
		{"00:00", true}, // across midnight
		{"03:15", true},
		{"06:59", true},
		{"07:00", false}, // exclusive end
		{"12:00", false},
	}
	for _, c := range cases {
		if got := q.Contains(at(t, c.hhmm)); got != c.want {
			t.Errorf("Contains(%s) = %v, want %v", c.hhmm, got, c.want)
		}
	}
}

func TestQuietHoursDisabledContainsNothing(t *testing.T) {
	var q QuietHours // zero value
	if q.Enabled() {
		t.Fatal("zero QuietHours should be disabled")
	}
	if q.Contains(at(t, "03:00")) {
		t.Error("disabled QuietHours.Contains should always be false")
	}
}

func TestClockTimeString(t *testing.T) {
	c, err := ParseClock("07:05")
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != "07:05" {
		t.Errorf("String() = %q, want 07:05", c.String())
	}
}

// mustQuiet builds a QuietHours from two "HH:MM" strings, failing the test on a
// parse error.
func mustQuiet(t *testing.T, start, end string) QuietHours {
	t.Helper()
	s, err := ParseClock(start)
	if err != nil {
		t.Fatalf("ParseClock(%q): %v", start, err)
	}
	e, err := ParseClock(end)
	if err != nil {
		t.Fatalf("ParseClock(%q): %v", end, err)
	}
	return QuietHours{Start: s, End: e}
}

// at returns a time today at the given "HH:MM" in the local zone, for feeding
// QuietHours.Contains (which only looks at hour/minute).
func at(t *testing.T, hhmm string) time.Time {
	t.Helper()
	c, err := ParseClock(hhmm)
	if err != nil {
		t.Fatalf("ParseClock(%q): %v", hhmm, err)
	}
	return time.Date(2026, 6, 29, int(c)/60, int(c)%60, 0, 0, time.Local)
}

// TestDecksDir builds the user-deck directory under the home dir. It sets the
// home-directory env var that os.UserHomeDir reads on this platform
// (USERPROFILE on Windows, HOME elsewhere) so the path is predictable.
func TestDecksDir(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}

	got, err := DecksDir()
	if err != nil {
		t.Fatalf("DecksDir error: %v", err)
	}
	want := filepath.Join(home, ".idle-hands", "decks")
	if got != want {
		t.Errorf("DecksDir() = %q, want %q", got, want)
	}
}
