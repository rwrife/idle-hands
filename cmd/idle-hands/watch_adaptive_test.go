package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/detect"
	"github.com/rwrife/idle-hands/internal/store"
)

func TestWrappedAdaptiveHistoryAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.New(store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Adaptive = true
	now := time.Unix(1700000000, 0)
	dc := detect.Config{BusyThreshold: 42 * time.Second, Now: func() time.Time { return now }}
	first, err := adaptiveDetectorConfig(cfg, dc, st, "claude")
	if err != nil {
		t.Fatal(err)
	}
	d := detect.New(first)
	for i := 0; i < detect.AdaptiveWarmupSamples; i++ {
		now = now.Add(12 * time.Second)
		d.Feed([]byte("result\n"))
	}
	fresh, err := store.New(store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	second, err := adaptiveDetectorConfig(cfg, dc, fresh, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := detect.New(second).Threshold(); got != 12*time.Second {
		t.Fatalf("reloaded threshold = %v", got)
	}
	other, err := adaptiveDetectorConfig(cfg, dc, fresh, "aider")
	if err != nil {
		t.Fatal(err)
	}
	if got := detect.New(other).Threshold(); got != 42*time.Second {
		t.Fatalf("different command inherited history: %v", got)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := adaptiveDetectorConfig(cfg, dc, fresh, "claude"); err == nil {
		t.Fatal("adaptive mode ignored history load error")
	}
	cfg.Adaptive = false
	disabled, err := adaptiveDetectorConfig(cfg, dc, fresh, "claude")
	if err != nil || detect.New(disabled).Threshold() != 42*time.Second {
		t.Fatalf("disabled mode accessed corrupt history: %v", err)
	}
}
