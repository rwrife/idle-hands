package config

import "testing"

func TestDecksRepoDiscoveryDefaultsTrue(t *testing.T) {
	cfg, err := Parse([]byte(``))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !cfg.Decks.RepoDiscovery {
		t.Errorf("repo_discovery default = false, want true")
	}
	if !Default().Decks.RepoDiscovery {
		t.Errorf("Default().Decks.RepoDiscovery = false, want true")
	}
}

func TestDecksRepoDiscoveryExplicitFalse(t *testing.T) {
	cfg, err := Parse([]byte("[decks]\nrepo_discovery = false\n"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if cfg.Decks.RepoDiscovery {
		t.Errorf("repo_discovery = true, want false when explicitly disabled")
	}
}

func TestDecksRepoDiscoveryExplicitTrue(t *testing.T) {
	cfg, err := Parse([]byte("[decks]\nrepo_discovery = true\n"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !cfg.Decks.RepoDiscovery {
		t.Errorf("repo_discovery = false, want true")
	}
}
