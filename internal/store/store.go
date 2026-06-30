// Package store persists the cheeky little scoreboard idle-hands keeps: how
// many BUSY windows you reclaimed and how many seconds they added up to, per
// day. It is a single JSON file at ~/.idle-hands/state.json — no database, no
// schema migrations, just a map of date → tally.
//
// Stats are keyed by *local* calendar date ("YYYY-MM-DD") so "today" means the
// user's today. Each completed BUSY window is one Record call; `idle-hands
// stats` reads the file and reports the current day's totals.
//
// Writes are atomic (temp file + rename) so a crash mid-write can't corrupt the
// scoreboard, and a missing or empty file simply reads as "nothing yet." The
// clock and file path are injectable so the whole thing tests without a real
// $HOME or wall clock.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// dirName / fileName locate the stats file under the user's home directory.
const (
	dirName  = ".idle-hands"
	fileName = "state.json"
)

// dateFormat is the local calendar-day key used in the stats map.
const dateFormat = "2006-01-02"

// DayStat is one calendar day's tally.
type DayStat struct {
	// Windows is the number of completed BUSY windows reclaimed that day.
	Windows int `json:"windows"`
	// Seconds is the total reclaimed time that day, in whole seconds.
	Seconds int64 `json:"seconds"`
}

// data is the on-disk document: a version marker plus the per-day tallies keyed
// by "YYYY-MM-DD". The version lets a future format change be detected rather
// than silently misread.
type data struct {
	Version int                `json:"version"`
	Days    map[string]DayStat `json:"days"`
}

const currentVersion = 1

// Store reads and writes the stats file. It is safe to construct cheaply; it
// holds only its path and clock, not an open handle. It is not safe for
// concurrent use across processes, but idle-hands records from one goroutine in
// one process, so a read-modify-write per window is fine.
type Store struct {
	path string
	now  func() time.Time
}

// Options configure a Store.
type Options struct {
	// Path is the stats file location. Empty selects ~/.idle-hands/state.json.
	Path string
	// Now returns the current time; the local date of it keys each record.
	// Nil selects time.Now.
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

// DefaultPath returns ~/.idle-hands/state.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, dirName, fileName), nil
}

// Path returns the file path this Store reads and writes.
func (s *Store) Path() string { return s.path }

// Record adds one completed BUSY window of the given reclaimed duration to
// today's tally and persists the file. A non-positive duration still counts as
// a window but contributes zero seconds (so a sub-second window isn't lost from
// the count). Durations are rounded to the nearest second for a tidy total.
func (s *Store) Record(reclaimed time.Duration) error {
	secs := int64(reclaimed.Round(time.Second) / time.Second)
	if secs < 0 {
		secs = 0
	}
	d, err := s.read()
	if err != nil {
		return err
	}
	key := s.now().Format(dateFormat)
	day := d.Days[key]
	day.Windows++
	day.Seconds += secs
	d.Days[key] = day
	return s.write(d)
}

// Today returns the tally for the current local date (zero value if none yet).
func (s *Store) Today() (DayStat, error) {
	d, err := s.read()
	if err != nil {
		return DayStat{}, err
	}
	return d.Days[s.now().Format(dateFormat)], nil
}

// Total returns the summed tally across every recorded day. It backs an
// all-time line in the stats output and keeps the door open for streak/recap
// features without another read path.
func (s *Store) Total() (DayStat, error) {
	d, err := s.read()
	if err != nil {
		return DayStat{}, err
	}
	var total DayStat
	for _, day := range d.Days {
		total.Windows += day.Windows
		total.Seconds += day.Seconds
	}
	return total, nil
}

// Days returns the recorded dates in ascending order. Useful for tests and any
// future history view; the hot path uses Today.
func (s *Store) Days() ([]string, error) {
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(d.Days))
	for k := range d.Days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// read loads and parses the stats file. A missing or empty file is not an
// error — it returns an initialized, empty document so the first Record just
// works. A present-but-corrupt file is an error so the user/tests notice.
func (s *Store) read() (data, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return data{Version: currentVersion, Days: map[string]DayStat{}}, nil
		}
		return data{}, fmt.Errorf("read stats %q: %w", s.path, err)
	}
	if len(raw) == 0 {
		return data{Version: currentVersion, Days: map[string]DayStat{}}, nil
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return data{}, fmt.Errorf("parse stats %q: %w", s.path, err)
	}
	if d.Days == nil {
		d.Days = map[string]DayStat{}
	}
	if d.Version == 0 {
		d.Version = currentVersion
	}
	return d, nil
}

// write persists d atomically: it creates the parent directory if needed,
// writes to a temp file in the same directory, then renames it over the target
// so a reader never sees a half-written file.
func (s *Store) write(d data) error {
	d.Version = currentVersion
	if d.Days == nil {
		d.Days = map[string]DayStat{}
	}
	buf, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode stats: %w", err)
	}
	buf = append(buf, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create stats dir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp stats file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp stats file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp stats file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace stats file: %w", err)
	}
	return nil
}
