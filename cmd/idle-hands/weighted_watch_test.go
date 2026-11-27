package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWeightedDecksResolvesNamesInOrder(t *testing.T) {
	userDir := t.TempDir()
	body := "name = 'custom'\n[[cards]]\ntitle = 'One'\ntext = 'Body'\n"
	if err := os.WriteFile(filepath.Join(userDir, "custom.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	decks, err := loadWeightedDecks(map[string]int{"move": 1, "custom": 3}, userDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decks) != 2 || decks[0].Deck.Name != "custom" || decks[0].Weight != 3 || decks[1].Deck.Name != "move" || decks[1].Weight != 1 {
		t.Fatalf("weighted decks = %+v", decks)
	}
	if _, err := loadWeightedDecks(map[string]int{"missing": 1}, userDir, nil); err == nil {
		t.Fatal("unknown weighted deck was accepted")
	}
}
