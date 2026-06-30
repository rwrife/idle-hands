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
	// Quiet is the daily window during which cards are suppressed.
	Quiet QuietHours
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
}

type fileQuietHours struct {
	Start string `toml:"start"`
	End   string `toml:"end"`
}

// Default returns the configuration used when no config file exists: the M4
// behavior (move deck, 20s threshold, no quiet hours).
func Default() Config {
	return Config{
		Deck:          DefaultDeck,
		BusyThreshold: DefaultBusyThreshold,
		Quiet:         QuietHours{},
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
	}

	quiet, err := parseQuietHours(fc.Quiet)
	if err != nil {
		return Config{}, err
	}
	cfg.Quiet = quiet

	return cfg, nil
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
