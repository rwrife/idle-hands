// Package focus implements idle-hands' opt-in focus-safe mode: a chosen window
// during which cards are suppressed even while the agent is BUSY, so a
// mid-thought moment isn't interrupted by a card. Reclaimed windows are still
// detected and (by default) still counted toward stats; only the on-screen card
// is withheld.
//
// The focus window is a single "focus-until" wall-clock timestamp persisted in
// ~/.idle-hands/focus.json so it survives restarts. `idle-hands focus <dur>`
// sets it, `idle-hands focus off` clears it, and `idle-hands focus` reports the
// remaining time. A focus block is "active" only while now is before that
// timestamp; once it passes, focus is simply expired (no card suppression).
//
// The clock and file path are injectable so the whole thing tests without a
// real $HOME or wall clock.
package focus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// dirName / fileName locate the focus-state file under the user's home dir.
const (
	dirName  = ".idle-hands"
	fileName = "focus.json"
)

// State is a focus window's persisted shape: the UTC instant until which cards
// are suppressed. A zero Until means "no focus block".
type State struct {
	// Until is the instant the focus block ends. Zero means no active block.
	Until time.Time `json:"until,omitempty"`
}

// Active reports whether the focus block is still running as of now.
func (s State) Active(now time.Time) bool {
	return !s.Until.IsZero() && now.Before(s.Until)
}

// Remaining returns how much focus time is left as of now, or zero if the block
// is inactive or already expired.
func (s State) Remaining(now time.Time) time.Duration {
	if !s.Active(now) {
		return 0
	}
	return s.Until.Sub(now)
}

// document is the on-disk shape: a version marker plus the focus state.
type document struct {
	Version int       `json:"version"`
	Until   time.Time `json:"until,omitempty"`
}

const currentVersion = 1

// Store reads and writes the focus-state file. It holds only its path and
// clock, so it is cheap to construct.
type Store struct {
	path string
	now  func() time.Time
}

// Options configure a Store.
type Options struct {
	// Path is the focus file location. Empty selects ~/.idle-hands/focus.json.
	Path string
	// Now returns the current time. Nil selects time.Now.
	Now func() time.Time
}

// New builds a Store. With a zero Options it targets the default path and the
// real clock.
func New(opts Options) (*Store, error) {
	path := opts.Path
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{path: path, now: now}, nil
}

// DefaultPath returns ~/.idle-hands/focus.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, dirName, fileName), nil
}

// Path returns the file path this Store reads and writes.
func (s *Store) Path() string { return s.path }

// Get returns the current focus state (zero value if none set or expired file
// missing). Whether the block is currently active is up to the caller via
// State.Active; Get just reports what is persisted.
func (s *Store) Get() (State, error) {
	d, err := s.read()
	if err != nil {
		return State{}, err
	}
	return State{Until: d.Until}, nil
}

// Set starts a focus block lasting d from now and persists it. A non-positive
// duration is an error — a zero-length focus block would suppress nothing and
// is almost certainly a mistake; use Clear to turn focus off.
func (s *Store) Set(d time.Duration) (State, error) {
	if d <= 0 {
		return State{}, fmt.Errorf("focus duration must be positive, got %s", d)
	}
	until := s.now().Add(d).UTC()
	st := State{Until: until}
	if err := s.write(document{Version: currentVersion, Until: until}); err != nil {
		return State{}, err
	}
	return st, nil
}

// Clear turns focus off by persisting an empty state. Clearing when no block is
// active is not an error (idempotent "off").
func (s *Store) Clear() error {
	return s.write(document{Version: currentVersion})
}

// read loads and parses the focus file. A missing or empty file is not an
// error — it reads as "no focus block". A present-but-corrupt file is an error
// so the user notices.
func (s *Store) read() (document, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return document{Version: currentVersion}, nil
		}
		return document{}, fmt.Errorf("read focus %q: %w", s.path, err)
	}
	if len(raw) == 0 {
		return document{Version: currentVersion}, nil
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return document{}, fmt.Errorf("parse focus %q: %w", s.path, err)
	}
	if d.Version == 0 {
		d.Version = currentVersion
	}
	return d, nil
}

// write persists d atomically (temp file + rename) so a reader never sees a
// half-written file, creating the parent directory if needed.
func (s *Store) write(d document) error {
	d.Version = currentVersion
	buf, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode focus: %w", err)
	}
	buf = append(buf, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create focus dir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".focus-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp focus file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp focus file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp focus file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace focus file: %w", err)
	}
	return nil
}
