package card

import (
	"io"
	"math/rand"
	"strconv"
	"testing"

	"github.com/rwrife/idle-hands/internal/deck"
)

func testWeightedDeck(name string, count int) deck.Deck {
	d := deck.Deck{Name: name, Emoji: name}
	for i := 0; i < count; i++ {
		d.Cards = append(d.Cards, deck.Card{Title: name + strconv.Itoa(i), Text: "body"})
	}
	return d
}

func TestWeightedPickerHonorsDeckWeights(t *testing.T) {
	p, err := NewWeightedPicker([]WeightedDeck{
		{Deck: testWeightedDeck("move", 80), Weight: 3},
		{Deck: testWeightedDeck("duck", 80), Weight: 1},
	}, rand.New(rand.NewSource(42)), 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for i := 0; i < 40000; i++ {
		d, _ := p.Next()
		counts[d.Name]++
	}
	ratio := float64(counts["move"]) / float64(counts["duck"])
	if ratio < 2.8 || ratio > 3.2 {
		t.Fatalf("deck distribution = %+v (ratio %.2f), want approximately 3:1", counts, ratio)
	}
}

func TestWeightedPickerAvoidsRecentCardsAndRelaxesForTinyDecks(t *testing.T) {
	p, err := NewWeightedPicker([]WeightedDeck{
		{Deck: testWeightedDeck("move", 4), Weight: 3},
		{Deck: testWeightedDeck("duck", 4), Weight: 1},
	}, rand.New(rand.NewSource(7)), 3)
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := NewWeightedPicker([]WeightedDeck{
		{Deck: testWeightedDeck("move", 4), Weight: 3},
		{Deck: testWeightedDeck("duck", 4), Weight: 1},
	}, rand.New(rand.NewSource(7)), 3)
	if err != nil {
		t.Fatal(err)
	}
	recent := []string{}
	for i := 0; i < 100; i++ {
		d, c := p.Next()
		d2, c2 := parallel.Next()
		if d.Name != d2.Name || c.Title != c2.Title {
			t.Fatalf("seeded sequences diverged: %s:%s vs %s:%s", d.Name, c.Title, d2.Name, c2.Title)
		}
		id := d.Name + ":" + c.Title
		for _, prior := range recent {
			if prior == id {
				t.Fatalf("card %q repeated within spacing window %v", id, recent)
			}
		}
		recent = append(recent, id)
		if len(recent) > 3 {
			recent = recent[1:]
		}
	}

	tiny, err := NewWeightedPicker([]WeightedDeck{
		{Deck: testWeightedDeck("one", 1), Weight: 1},
		{Deck: testWeightedDeck("two", 1), Weight: 1},
	}, rand.New(rand.NewSource(1)), 3)
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for i := 0; i < 20; i++ {
		d, _ := tiny.Next()
		if d.Name == last {
			t.Fatalf("tiny deck repeated immediately: %q", d.Name)
		}
		last = d.Name
	}
}

func TestWeightedPickerRejectsInvalidDecks(t *testing.T) {
	cases := []struct {
		name  string
		decks []WeightedDeck
	}{
		{name: "empty"},
		{name: "zero weight", decks: []WeightedDeck{{Deck: testWeightedDeck("move", 1)}}},
		{name: "empty deck", decks: []WeightedDeck{{Deck: deck.Deck{Name: "empty"}, Weight: 1}}},
		{name: "duplicate", decks: []WeightedDeck{{Deck: testWeightedDeck("move", 1), Weight: 1}, {Deck: testWeightedDeck("move", 1), Weight: 2}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWeightedPicker(tc.decks, nil, 1); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestWeightedRendererReportsSelectedDeck(t *testing.T) {
	var got string
	r, err := NewWeightedRenderer(io.Discard, Options{
		OnShow: func(name, _ string) { got = name },
	}, []WeightedDeck{{Deck: testWeightedDeck("custom", 1), Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	r.OnBusy(0)
	if got != "custom" {
		t.Fatalf("OnShow deck = %q, want custom", got)
	}
}
