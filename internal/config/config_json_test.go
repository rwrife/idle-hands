package config

import "testing"

func TestParseJSONDefaults(t *testing.T) {
	cfg, err := Parse([]byte(``))
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if cfg.JSON.Enabled {
		t.Errorf("JSON.Enabled = true by default, want false")
	}
	if cfg.JSON.FD != DefaultJSONFD {
		t.Errorf("JSON.FD = %d, want default %d", cfg.JSON.FD, DefaultJSONFD)
	}
}

func TestParseJSONEnabled(t *testing.T) {
	cfg, err := Parse([]byte("json = true\njson_fd = 2\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.JSON.Enabled {
		t.Errorf("JSON.Enabled = false, want true")
	}
	if cfg.JSON.FD != 2 {
		t.Errorf("JSON.FD = %d, want 2", cfg.JSON.FD)
	}
}

func TestParseJSONRejectsStdout(t *testing.T) {
	if _, err := Parse([]byte("json_fd = 1\n")); err == nil {
		t.Fatal("json_fd = 1 should be rejected (would corrupt agent stdout)")
	}
}

func TestParseJSONRejectsBadFD(t *testing.T) {
	if _, err := Parse([]byte("json_fd = 5\n")); err == nil {
		t.Fatal("json_fd = 5 should be rejected (unsupported fd)")
	}
}
