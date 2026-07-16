package deck

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// repoDeckSubdir is the path, relative to a discovered repo root marker, that
// holds team decks. Discovery walks up from the working directory looking for
// this directory (mirroring how git finds its config), so a team can commit
// shared decks alongside their code.
const repoDeckSubdir = ".idle-hands" + string(filepath.Separator) + "decks"

// DiscoverRepoDeckDirs walks upward from startDir toward the filesystem root,
// collecting every existing ".idle-hands/decks" directory it finds. The result
// is ordered nearest-first (the deepest directory, closest to startDir, comes
// first) so a nested repo's decks take precedence over an outer one's. A blank
// startDir uses the process working directory. Directories that don't exist are
// simply skipped; only a genuine stat error other than "not found" aborts.
//
// The walk stops at the filesystem root. It does not require a .git directory —
// any .idle-hands/decks along the way counts — but it naturally stops climbing
// once it reaches the top of the tree.
func DiscoverRepoDeckDirs(startDir string) ([]string, error) {
	dir := strings.TrimSpace(startDir)
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", dir, err)
	}

	var dirs []string
	for {
		candidate := filepath.Join(abs, repoDeckSubdir)
		info, err := os.Stat(candidate)
		switch {
		case err == nil && info.IsDir():
			dirs = append(dirs, candidate)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("stat %q: %w", candidate, err)
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			break // reached filesystem root
		}
		abs = parent
	}
	return dirs, nil
}

// LoadRepoDecks reads every *.toml deck under the given repo deck directories
// and returns them keyed by a filename-derived namespace (the base name without
// the .toml extension), NOT by the deck's declared Name. This is deliberate:
// two teams (or a team and a user) may pick the same internal deck Name, so
// repo decks are addressed by their file ("onboarding.toml" -> "onboarding")
// and shown as "repo:onboarding" so the source is always clear.
//
// dirs should be ordered nearest-first (as DiscoverRepoDeckDirs returns them);
// when the same file-namespace appears in more than one directory, the nearest
// wins and the farther one is skipped.
//
// A malformed deck file is never fatal: it is logged as a warning and skipped,
// so a bad team deck can't crash a watch session. Missing directories are
// ignored.
func LoadRepoDecks(dirs []string) map[string]Deck {
	out := make(map[string]Deck)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				log.Printf("idle-hands: skipping repo deck dir %q: %v", dir, err)
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			ns := strings.TrimSuffix(e.Name(), ".toml")
			if _, taken := out[ns]; taken {
				continue // nearer dir already provided this namespace
			}
			full := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(full)
			if err != nil {
				log.Printf("idle-hands: skipping repo deck %q: %v", full, err)
				continue
			}
			d, err := Parse(data)
			if err != nil {
				log.Printf("idle-hands: skipping malformed repo deck %q: %v", full, err)
				continue
			}
			// Address the deck by its file namespace so `deck list` and the
			// deck selector use "onboarding", independent of the deck's own
			// declared Name. Keep the declared Name for display of the deck's
			// title where useful, but key the catalog by namespace.
			out[ns] = d
		}
	}
	return out
}
