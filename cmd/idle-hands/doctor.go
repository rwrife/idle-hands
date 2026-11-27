package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
	"github.com/rwrife/idle-hands/internal/duckdiff"
	"github.com/rwrife/idle-hands/internal/focus"
	"github.com/rwrife/idle-hands/internal/hook"
	"github.com/rwrife/idle-hands/internal/srs"
	"github.com/rwrife/idle-hands/internal/store"
	"github.com/rwrife/idle-hands/internal/wrap"
)

type doctorPaths struct {
	config, state, focus, userDecks, cwd string
}

type doctorDeck struct {
	Name    string `json:"name"`
	Cards   int    `json:"cards"`
	Shadows bool   `json:"shadows,omitempty"`
	Live    bool   `json:"live,omitempty"`
}

type doctorReport struct {
	TS               time.Time               `json:"ts"`
	Event            string                  `json:"event"`
	OS               string                  `json:"os"`
	PTYAvailable     bool                    `json:"pty_available"`
	ConfigPath       string                  `json:"config_path"`
	EffectiveConfig  map[string]any          `json:"effective_config"`
	StatePath        string                  `json:"state_path"`
	StateWritable    bool                    `json:"state_writable"`
	UserDecksPath    string                  `json:"user_decks_path"`
	TeamDeckPaths    []string                `json:"team_deck_paths"`
	Decks            map[string][]doctorDeck `json:"decks"`
	QuietHoursActive bool                    `json:"quiet_hours_active"`
	FocusSafeActive  bool                    `json:"focus_safe_active"`
	BusyThreshold    string                  `json:"busy_threshold"`
	Warnings         []string                `json:"warnings"`
	Errors           []string                `json:"errors"`
}

func cmdDoctor(args []string) (int, error) {
	asJSON := false
	if len(args) == 1 && args[0] == "--json" {
		asJSON = true
	} else if len(args) != 0 {
		return 2, fmt.Errorf("doctor: usage: idle-hands doctor [--json]")
	}
	cfgPath, err := config.Path()
	if err != nil {
		return 1, err
	}
	statePath, err := store.DefaultPath()
	if err != nil {
		return 1, err
	}
	focusPath, err := focus.DefaultPath()
	if err != nil {
		return 1, err
	}
	userDir, err := config.DecksDir()
	if err != nil {
		return 1, err
	}
	return runDoctor(stdout, doctorPaths{
		config: cfgPath, state: statePath, focus: focusPath, userDecks: userDir,
	}, time.Now(), asJSON)
}

func runDoctor(w io.Writer, paths doctorPaths, now time.Time, asJSON bool) (int, error) {
	report := resolveDoctor(paths, now)
	var err error
	if asJSON {
		err = json.NewEncoder(w).Encode(report)
	} else {
		err = printDoctor(w, report)
	}
	if err != nil {
		return 1, fmt.Errorf("doctor: write report: %w", err)
	}
	if len(report.Errors) != 0 {
		return 1, nil // The report itself includes every diagnostic error.
	}
	return 0, nil
}

func resolveDoctor(paths doctorPaths, now time.Time) doctorReport {
	r := doctorReport{
		TS: now.UTC(), Event: "doctor", OS: runtime.GOOS,
		ConfigPath: paths.config, StatePath: paths.state, UserDecksPath: paths.userDecks,
		TeamDeckPaths: []string{},
		Decks: map[string][]doctorDeck{
			"embedded": {}, "user": {}, "team": {}, "dynamic": {},
		},
		Warnings: []string{}, Errors: []string{},
	}
	problem := func(label string, err error) {
		if err != nil {
			r.Errors = append(r.Errors, label+": "+err.Error())
		}
	}
	if err := wrap.ProbePTY(); err != nil {
		r.Warnings = append(r.Warnings, "PTY: "+err.Error()+"; stdio fallback remains available")
	} else {
		r.PTYAvailable = true
	}
	info, err := os.Stat(filepath.Dir(paths.config))
	if err == nil && !info.IsDir() {
		err = fmt.Errorf("not a directory")
	}
	problem("config directory "+filepath.Dir(paths.config), err)
	if _, err := os.Stat(paths.config); errors.Is(err, os.ErrNotExist) {
		r.Warnings = append(r.Warnings, "config file is absent; using defaults")
	}
	cfg, cfgErr := config.LoadFile(paths.config)
	problem("config", cfgErr)

	err = probeStateWritable(paths.state)
	r.StateWritable = err == nil
	problem("state writability", err)
	st, err := store.New(store.Options{Path: paths.state})
	if err == nil {
		_, err = st.Today()
	}
	problem("state", err)
	foc, err := focus.New(focus.Options{Path: paths.focus})
	if err == nil {
		var state focus.State
		state, err = foc.Get()
		if err == nil {
			r.FocusSafeActive = state.Active(now)
		}
	}
	problem("focus-safe", err)
	if cfgErr != nil {
		return r // Never present defaults as the effective config after a parse error.
	}
	r.EffectiveConfig = doctorConfig(cfg)
	r.BusyThreshold = cfg.BusyThreshold.String()
	r.QuietHoursActive = cfg.Quiet.Contains(now)
	if cfg.Adaptive {
		r.Warnings = append(r.Warnings, "busy_threshold is the baseline; adaptive thresholds depend on the wrapped command's history")
	}
	var repoDirs []string
	if cfg.Decks.RepoDiscovery {
		repoDirs, err = deck.DiscoverRepoDeckDirs(paths.cwd)
		problem("team deck discovery", err)
		if repoDirs != nil {
			r.TeamDeckPaths = repoDirs
		}
	}
	catalog, err := deck.CatalogWithRepo(paths.userDecks, repoDirs)
	problem("decks", err)
	for _, entry := range catalog {
		source := "embedded"
		switch entry.Source {
		case deck.SourceUser:
			source = "user"
		case deck.SourceRepo:
			source = "team"
		}
		r.Decks[source] = append(r.Decks[source], doctorDeck{
			Name: entry.Key, Cards: len(entry.Deck.Cards), Shadows: entry.Shadows,
		})
	}
	if cfg.SRS.Source != "" || cfg.Deck == srs.DeckName {
		d, err := srs.LoadDeck(cfg.SRS.Source)
		problem("srs deck", err)
		if err == nil {
			r.Decks["dynamic"] = append(r.Decks["dynamic"], doctorDeck{Name: d.Name, Cards: len(d.Cards)})
		}
	}
	if len(cfg.Hooks.Specs) > 0 || cfg.Deck == hook.DeckName {
		_, err := hook.LoadDeck(hook.Options{Specs: cfg.Hooks.Specs, Timeout: cfg.Hooks.Timeout})
		problem("hook deck", err)
		if err == nil {
			r.Decks["dynamic"] = append(r.Decks["dynamic"], doctorDeck{Name: hook.DeckName, Cards: len(cfg.Hooks.Specs), Live: true})
		}
		r.Warnings = append(r.Warnings, "hook commands were not executed")
	}
	r.Decks["dynamic"] = append(r.Decks["dynamic"], doctorDeck{Name: duckdiff.DeckName, Cards: 1, Live: true})
	r.Warnings = append(r.Warnings, "duckdiff model and staged diff were not probed; runtime may fall back to duck")
	switch cfg.Deck {
	case srs.DeckName, hook.DeckName, duckdiff.DeckName:
	default:
		_, _, err := deck.ResolveWithRepo(cfg.Deck, paths.userDecks, repoDirs)
		problem("selected deck", err)
	}
	return r
}

func doctorConfig(cfg config.Config) map[string]any {
	if cfg.DuckDiff.Model == "" {
		cfg.DuckDiff.Model = duckdiff.DefaultModel
	}
	if cfg.DuckDiff.URL == "" {
		cfg.DuckDiff.URL = duckdiff.DefaultURL
	}
	return map[string]any{
		"deck": cfg.Deck, "busy_threshold": cfg.BusyThreshold.String(),
		"busy_threshold_set": cfg.BusyThresholdSet,
		"adaptive":           cfg.Adaptive, "adaptive_percentile": cfg.AdaptivePercentile,
		"quiet_hours": map[string]string{"start": cfg.Quiet.Start.String(), "end": cfg.Quiet.End.String()},
		"srs_source":  cfg.SRS.Source, "srs_reveal": cfg.SRS.Reveal.String(), "srs_spacing": cfg.SRS.Spacing,
		"duckdiff_model": cfg.DuckDiff.Model, "duckdiff_url": cfg.DuckDiff.URL,
		"duckdiff_timeout": cfg.DuckDiff.Timeout.String(),
		"hooks":            cfg.Hooks.Specs, "hook_timeout": cfg.Hooks.Timeout.String(),
		"focus_safe": map[string]bool{"suppress_stats": cfg.FocusSafe.SuppressStats},
		"json":       cfg.JSON.Enabled, "json_fd": cfg.JSON.FD,
		"decks": map[string]bool{"repo_discovery": cfg.Decks.RepoDiscovery},
	}
}

// The store uses temp-file + rename. Check both its directory and any existing
// target without truncating, replacing, or creating state.json.
func probeStateWritable(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".doctor-*")
	if err != nil {
		return err
	}
	_, writeErr := f.Write([]byte("idle-hands doctor\n"))
	closeErr := f.Close()
	removeErr := os.Remove(f.Name())
	return errors.Join(writeErr, closeErr, removeErr)
}

func printDoctor(w io.Writer, r doctorReport) error {
	// Buffer the human report so all writes (including failures) share one path.
	var b []byte
	cfg, err := json.MarshalIndent(r.EffectiveConfig, "", "  ")
	if err != nil {
		return err
	}
	b = fmt.Appendf(b, "idle-hands doctor\nOS: %s\nPTY available: %t\nConfig: %s\nEffective config:\n%s\nState: %s (writable: %t)\nUser decks: %s\nTeam deck paths: %v\nBusy threshold: %s\nQuiet hours active: %t\nFocus-safe active: %t\n",
		r.OS, r.PTYAvailable, r.ConfigPath, cfg, r.StatePath, r.StateWritable,
		r.UserDecksPath, r.TeamDeckPaths, r.BusyThreshold, r.QuietHoursActive, r.FocusSafeActive)
	for _, source := range []string{"embedded", "user", "team", "dynamic"} {
		b = fmt.Appendf(b, "%s decks (%d):\n", source, len(r.Decks[source]))
		for _, d := range r.Decks[source] {
			b = fmt.Appendf(b, "  %s: %d cards (live: %t, overrides another deck: %t)\n", d.Name, d.Cards, d.Live, d.Shadows)
		}
	}
	for _, warning := range r.Warnings {
		b = fmt.Appendf(b, "WARNING: %s\n", warning)
	}
	for _, problem := range r.Errors {
		b = fmt.Appendf(b, "ERROR: %s\n", problem)
	}
	_, err = w.Write(b)
	return err
}
