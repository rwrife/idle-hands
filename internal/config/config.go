// Package config loads the small, human-editable TOML that tunes idle-hands:
// which deck to show, how long the agent must go quiet before a card fires, and
// the quiet hours during which cards are suppressed entirely. It lives at
// ~/.idle-hands/config.toml.
//
// The guiding rule (M5's definition of done) is "editing config changes
// behavior on the next run." So Load is forgiving about a missing file — a
// fresh install with no config gets sane defaults — but strict about a present
// one: a malformed config is a real error the user should see and fix, not a
// silent fallback that makes their edits look ignored.
//
// Everything here is plain data with no side effects beyond reading the file,
// and the home-directory lookup is injectable, so the package is fully testable
// without touching a real $HOME.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultDeck is the deck shown when config selects none. It matches the M4
// default so behavior is unchanged until the user opts into another deck.
const DefaultDeck = "move"

// DefaultBusyThreshold is the quiet span before a card fires when config sets
// none. It mirrors detect.DefaultBusyThreshold (kept as a literal here to avoid
// a dependency cycle and to keep config self-describing).
const DefaultBusyThreshold = 20 * time.Second

// DefaultSRSReveal is how long the flashcard deck shows the question before
// revealing the answer when config sets no srs_reveal. Long enough to actually
// try to recall, short enough to still finish inside a typical think window.
const DefaultSRSReveal = 6 * time.Second

// DefaultSRSSpacing is how many recently-shown flashcards to deprioritize when
// config sets no srs_spacing, so the same card doesn't resurface for a few
// waits. It only applies to the "srs" deck.
const DefaultSRSSpacing = 3

// DefaultDuckDiffTimeout bounds the whole Ollama round-trip for the "duckdiff"
// deck when config sets no duckdiff_timeout. Past it, watch falls back to the
// static "duck" deck rather than make you wait on the model. It mirrors
// duckdiff.DefaultTimeout (kept as a literal here to avoid a dependency cycle).
const DefaultDuckDiffTimeout = 4 * time.Second

// DefaultHookTimeout bounds a single hook command when config sets no
// hook_timeout. It is short enough to comfortably finish inside a typical
// think window; a hook still gets cancelled the moment the window ends, so this
// is only the hard ceiling for a hook that outlives the wait. It mirrors
// hook.DefaultTimeout (kept as a literal here to avoid a dependency cycle).
const DefaultHookTimeout = 10 * time.Second

// dirName / fileName are the on-disk locations under the user's home directory.
const (
	dirName   = ".idle-hands"
	fileName  = "config.toml"
	decksName = "decks"
)

// Config is the fully-resolved idle-hands configuration. Construct it via Load
// (which applies defaults) rather than by hand; the zero value is not valid.
type Config struct {
	// Deck is the name of the deck to show during a BUSY window (e.g. "move").
	Deck string
	// BusyThreshold is how long output must stay quiet before a card fires.
	BusyThreshold time.Duration
	// BusyThresholdSet reports whether busy_threshold was explicitly present in
	// the config file (as opposed to defaulted). Consumers such as the --preset
	// flag use it to decide precedence: an explicit config value wins over a
	// preset's suggested threshold, while a preset still overrides the built-in
	// default when the user left busy_threshold unset.
	BusyThresholdSet bool
	// Quiet is the daily window during which cards are suppressed.
	Quiet QuietHours
	// SRS holds the flashcard-deck settings, used only when Deck == "srs".
	SRS SRSConfig
	// DuckDiff holds the diff-review-deck settings, used only when Deck ==
	// "duckdiff".
	DuckDiff DuckDiffConfig
	// Hooks holds the user-registered hook commands, used only when Deck ==
	// "hook". Empty when no [[hooks]] blocks are configured.
	Hooks HooksConfig
}

// HooksConfig holds the registered hook commands and the shared per-hook
// timeout for the "hook" deck. It is only consulted when Deck is "hook"; for
// any other deck these fields are ignored. Hooks are strictly opt-in: with no
// [[hooks]] blocks, Specs is empty and selecting deck = "hook" is an error the
// watch layer surfaces (there is nothing to run).
type HooksConfig struct {
	// Specs are the registered hooks, in config order. A hook deck runs one of
	// these per BUSY window (round-robin, like the other decks pick a card).
	Specs []HookSpec
	// Timeout is the hard ceiling for a single hook command. <= 0 is treated as
	// DefaultHookTimeout by consumers. A hook is also cancelled when the BUSY
	// window ends, whichever comes first.
	Timeout time.Duration
}

// HookSpec is one registered hook: a display name and the command (argv) to
// run. Both are required. The command is never a shell string — it is an argv
// slice run directly — so nothing is word-split or glob-expanded and only the
// exact user-configured program runs.
type HookSpec struct {
	// Name labels the hook on its card (e.g. "fetch"). Required, unique.
	Name string
	// Cmd is the argv to execute (Cmd[0] is the program). Required, non-empty.
	Cmd []string
}

// DuckDiffConfig tunes the "duckdiff" deck: which local Ollama model to ask for
// a review question about the staged diff, where to reach Ollama, and how long
// to wait before falling back to the static "duck" deck. It is only consulted
// when Deck is "duckdiff"; for any other deck these fields are ignored. Every
// field is optional — an empty model/url uses duckdiff's defaults — so
// deck = "duckdiff" works with zero extra config for anyone already running
// Ollama.
type DuckDiffConfig struct {
	// Model is the Ollama model asked for the question. Empty uses the default.
	Model string
	// URL is the Ollama generate endpoint. Empty uses the default.
	URL string
	// Timeout bounds the model round-trip. <= 0 is treated as the default by
	// consumers.
	Timeout time.Duration
}

// SRSConfig tunes the spaced-repetition ("srs") flashcard deck: where the cards
// come from, how long to show the question before revealing the answer, and how
// many recently-seen cards to hold back. It is only consulted when Deck is
// "srs"; for any other deck these fields are ignored (and Source may be empty).
type SRSConfig struct {
	// Source is the path to the card file (Markdown Q/A or Anki text export).
	// Required when Deck == "srs"; watch surfaces a clear error if it's unset
	// or unreadable.
	Source string
	// Reveal is the front-only delay before the answer is shown. <= 0 is
	// treated as DefaultSRSReveal by consumers.
	Reveal time.Duration
	// Spacing is how many recently-shown cards to deprioritize.
	Spacing int
}

// QuietHours is a daily clock-time range during which idle-hands shows no
// cards (the agent is still wrapped and stats still record; only the card is
// withheld). It is expressed in the machine's local time. A zero value
// (Start == End) means "no quiet hours" and never suppresses.
//
// Ranges may wrap midnight: Start 22:00, End 07:00 means 22:00→07:00 the next
// morning, which is the common case, so it must work.
type QuietHours struct {
	// Start is the inclusive minute-of-day the quiet window opens.
	Start ClockTime
	// End is the exclusive minute-of-day the quiet window closes.
	End ClockTime
}

// fileConfig is the wire shape decoded from TOML. It is intentionally separate
// from Config so the on-disk format (strings, "HH:MM") stays decoupled from the
// resolved runtime type (time.Duration, parsed clock times), and so unknown or
// omitted keys fall back to defaults cleanly.
type fileConfig struct {
	Deck          string         `toml:"deck"`
	BusyThreshold string         `toml:"busy_threshold"`
	Quiet         fileQuietHours `toml:"quiet_hours"`
	SRSSource     string         `toml:"srs_source"`
	SRSReveal     string         `toml:"srs_reveal"`
	SRSSpacing    *int           `toml:"srs_spacing"`
	DuckDiffModel string         `toml:"duckdiff_model"`
	DuckDiffURL   string         `toml:"duckdiff_url"`
	DuckDiffTO    string         `toml:"duckdiff_timeout"`
	HookTimeout   string         `toml:"hook_timeout"`
	Hooks         []fileHook     `toml:"hooks"`
}

// fileHook is the wire shape of one [[hooks]] block.
type fileHook struct {
	Name string   `toml:"name"`
	Cmd  []string `toml:"cmd"`
}

type fileQuietHours struct {
	Start string `toml:"start"`
	End   string `toml:"end"`
}

// Default returns the configuration used when no config file exists: the M4
// behavior (move deck, 20s threshold, no quiet hours). SRS defaults are filled
// in too, though they only matter once the user selects deck = "srs".
func Default() Config {
	return Config{
		Deck:          DefaultDeck,
		BusyThreshold: DefaultBusyThreshold,
		Quiet:         QuietHours{},
		SRS: SRSConfig{
			Reveal:  DefaultSRSReveal,
			Spacing: DefaultSRSSpacing,
		},
		DuckDiff: DuckDiffConfig{
			Timeout: DefaultDuckDiffTimeout,
		},
		Hooks: HooksConfig{
			Timeout: DefaultHookTimeout,
		},
	}
}

// Path returns the absolute path to the config file (~/.idle-hands/config.toml)
// using the OS user home directory. It is where Load reads from by default.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, dirName, fileName), nil
}

// DecksDir returns the absolute path to the user deck directory
// (~/.idle-hands/decks) using the OS user home directory. It is where user
// decks (*.toml) are loaded from; the directory need not exist.
func DecksDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, dirName, decksName), nil
}

// Load reads and resolves the config file at the user's default path. A missing
// file is not an error — it returns Default() so a fresh install just works. A
// present-but-malformed file is an error so the user notices their edit was
// rejected rather than silently ignored.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path)
}

// LoadFile is Load against an explicit path (used by tests and any future
// --config flag). A non-existent path yields Default(); any other read or parse
// failure is returned.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes a config from TOML bytes, applies defaults for any omitted
// field, and validates the result. It is the single code path both Load and
// tests use, so on-disk and in-memory configs are validated identically.
func Parse(data []byte) (Config, error) {
	var fc fileConfig
	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		// Reject unknown keys so a typo'd setting ("buzy_threshold") is caught
		// loudly rather than silently doing nothing.
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return Config{}, fmt.Errorf("unknown config key(s): %s", strings.Join(keys, ", "))
	}

	cfg := Default()

	if v := strings.TrimSpace(fc.Deck); v != "" {
		cfg.Deck = v
	}

	if v := strings.TrimSpace(fc.BusyThreshold); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("busy_threshold %q: %w", v, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("busy_threshold must be positive, got %q", v)
		}
		cfg.BusyThreshold = d
		cfg.BusyThresholdSet = true
	}

	quiet, err := parseQuietHours(fc.Quiet)
	if err != nil {
		return Config{}, err
	}
	cfg.Quiet = quiet

	if err := applySRS(&cfg, fc); err != nil {
		return Config{}, err
	}

	if err := applyDuckDiff(&cfg, fc); err != nil {
		return Config{}, err
	}

	if err := applyHooks(&cfg, fc); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// applyHooks resolves the hook-deck settings onto cfg. A blank hook_timeout
// keeps the default; a present one must be a positive duration. Each [[hooks]]
// block must have a non-empty name and a non-empty cmd; names must be unique.
// Malformed hook config is a real error (consistent with the strict config
// behavior elsewhere) so a typo isn't silently ignored. Having zero hooks is
// not itself an error here — deck != "hook" configs must not be rejected for an
// empty hooks list; the watch layer errors only when deck = "hook" and there
// are no hooks to run.
func applyHooks(cfg *Config, fc fileConfig) error {
	if v := strings.TrimSpace(fc.HookTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("hook_timeout %q: %w", v, err)
		}
		if d <= 0 {
			return fmt.Errorf("hook_timeout must be positive, got %q", v)
		}
		cfg.Hooks.Timeout = d
	}
	seen := make(map[string]struct{}, len(fc.Hooks))
	for i, h := range fc.Hooks {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			return fmt.Errorf("hooks[%d]: name is required", i)
		}
		if len(h.Cmd) == 0 || strings.TrimSpace(h.Cmd[0]) == "" {
			return fmt.Errorf("hooks[%d] (%q): cmd is required and its first element must be a program", i, name)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("duplicate hook name %q", name)
		}
		seen[name] = struct{}{}
		cfg.Hooks.Specs = append(cfg.Hooks.Specs, HookSpec{Name: name, Cmd: append([]string(nil), h.Cmd...)})
	}
	return nil
}

// applySRS resolves the flashcard-deck settings onto cfg. The source path is
// taken verbatim (existence is validated at load time by the srs loader, not
// here, so a config with deck != "srs" isn't rejected for a missing card file).
// A blank srs_reveal keeps the default; a present one must be a positive
// duration. A negative srs_spacing is rejected; 0 is allowed (means "only avoid
// immediate repeats").
func applySRS(cfg *Config, fc fileConfig) error {
	if v := strings.TrimSpace(fc.SRSSource); v != "" {
		expanded, err := expandHome(v)
		if err != nil {
			return fmt.Errorf("srs_source %q: %w", v, err)
		}
		cfg.SRS.Source = expanded
	}
	if v := strings.TrimSpace(fc.SRSReveal); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("srs_reveal %q: %w", v, err)
		}
		if d <= 0 {
			return fmt.Errorf("srs_reveal must be positive, got %q", v)
		}
		cfg.SRS.Reveal = d
	}
	if fc.SRSSpacing != nil {
		if *fc.SRSSpacing < 0 {
			return fmt.Errorf("srs_spacing must be >= 0, got %d", *fc.SRSSpacing)
		}
		cfg.SRS.Spacing = *fc.SRSSpacing
	}
	return nil
}

// applyDuckDiff resolves the diff-review-deck settings onto cfg. The model name
// and URL are taken verbatim (empty keeps duckdiff's default). A blank
// duckdiff_timeout keeps the default; a present one must be a positive duration.
// None of these are validated against a running Ollama here — a config with
// deck != "duckdiff" must not be rejected for an unreachable model — so
// availability is handled at watch time by falling back to the static duck deck.
func applyDuckDiff(cfg *Config, fc fileConfig) error {
	if v := strings.TrimSpace(fc.DuckDiffModel); v != "" {
		cfg.DuckDiff.Model = v
	}
	if v := strings.TrimSpace(fc.DuckDiffURL); v != "" {
		cfg.DuckDiff.URL = v
	}
	if v := strings.TrimSpace(fc.DuckDiffTO); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("duckdiff_timeout %q: %w", v, err)
		}
		if d <= 0 {
			return fmt.Errorf("duckdiff_timeout must be positive, got %q", v)
		}
		cfg.DuckDiff.Timeout = d
	}
	return nil
}

// expandHome expands a leading "~/" (or a bare "~") in a path to the user's home
// directory, so config values like srs_source = "~/.idle-hands/cards.md" work
// the way users expect. Other paths (absolute or relative) are returned as-is.
// A "~user" form is not supported and is left untouched.
func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// parseQuietHours turns the string "HH:MM" pair into a QuietHours. Both empty
// means "no quiet hours". Specifying only one side is an error: a half-open
// quiet window is almost certainly a mistake, and guessing the other end would
// silently do something the user didn't write.
func parseQuietHours(fq fileQuietHours) (QuietHours, error) {
	start := strings.TrimSpace(fq.Start)
	end := strings.TrimSpace(fq.End)
	switch {
	case start == "" && end == "":
		return QuietHours{}, nil
	case start == "" || end == "":
		return QuietHours{}, fmt.Errorf("quiet_hours needs both start and end (got start=%q end=%q)", start, end)
	}
	s, err := ParseClock(start)
	if err != nil {
		return QuietHours{}, fmt.Errorf("quiet_hours.start: %w", err)
	}
	e, err := ParseClock(end)
	if err != nil {
		return QuietHours{}, fmt.Errorf("quiet_hours.end: %w", err)
	}
	if s == e {
		// A zero-length window would never suppress; reject it so the user
		// isn't surprised that their "quiet hours" do nothing.
		return QuietHours{}, fmt.Errorf("quiet_hours start and end are equal (%s); that suppresses nothing", start)
	}
	return QuietHours{Start: s, End: e}, nil
}

// Enabled reports whether any quiet window is configured.
func (q QuietHours) Enabled() bool { return q.Start != q.End }

// Contains reports whether the local clock time of t falls inside the quiet
// window. It correctly handles windows that wrap past midnight (Start > End):
// for 22:00→07:00, both 23:30 and 02:00 are inside, while 12:00 is not. A
// disabled (zero) window contains nothing.
func (q QuietHours) Contains(t time.Time) bool {
	if !q.Enabled() {
		return false
	}
	now := ClockOf(t)
	if q.Start < q.End {
		// Same-day window, e.g. 09:00→17:00.
		return now >= q.Start && now < q.End
	}
	// Wrapping window, e.g. 22:00→07:00: inside if at/after Start OR before End.
	return now >= q.Start || now < q.End
}

// ClockTime is a minute-of-day in [0,1440): local wall-clock time with no date.
// It is a small integer so QuietHours comparisons are trivial and total.
type ClockTime int

const minutesPerDay = 24 * 60

// ParseClock parses "HH:MM" (24-hour) into a ClockTime. Hours must be 0–23 and
// minutes 0–59; anything else is an error.
func ParseClock(s string) (ClockTime, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	h, err := atoiBounded(parts[0], 0, 23)
	if err != nil {
		return 0, fmt.Errorf("invalid hour in %q: %w", s, err)
	}
	m, err := atoiBounded(parts[1], 0, 59)
	if err != nil {
		return 0, fmt.Errorf("invalid minute in %q: %w", s, err)
	}
	return ClockTime(h*60 + m), nil
}

// ClockOf returns the minute-of-day of t in its own location.
func ClockOf(t time.Time) ClockTime {
	return ClockTime(t.Hour()*60 + t.Minute())
}

// String renders a ClockTime back to "HH:MM" for messages and round-tripping.
func (c ClockTime) String() string {
	c = ((c % minutesPerDay) + minutesPerDay) % minutesPerDay
	return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60)
}

// atoiBounded parses a non-negative integer string and checks it is within
// [lo,hi]. It is stricter than strconv.Atoi about stray characters because the
// time format is fixed and a loose parse would mask typos.
func atoiBounded(s string, lo, hi int) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
		if n > hi {
			break // avoid overflow on absurd input; the bound check below fails
		}
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d out of range [%d,%d]", n, lo, hi)
	}
	return n, nil
}
