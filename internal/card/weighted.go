package card

import (
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

type selectionPicker interface {
	Next() (deck.Deck, deck.Card)
}

type singleDeckPicker struct {
	deck   deck.Deck
	picker *Picker
}

func (p *singleDeckPicker) Next() (deck.Deck, deck.Card) {
	return p.deck, p.picker.Next()
}

// WeightedDeck is a static deck and its relative selection weight.
type WeightedDeck struct {
	Deck   deck.Deck
	Weight int
}

type weightedCard struct {
	deck   deck.Deck
	card   deck.Card
	weight float64
}

type WeightedPicker struct {
	cards  []weightedCard
	rng    *rand.Rand
	last   int
	recent []int
	window int
}

// NewWeightedPicker creates a picker whose per-deck probability follows Weight,
// independent of the number of cards in that deck. It avoids recent card IDs
// when alternatives exist; rng is injectable for deterministic tests.
func NewWeightedPicker(decks []WeightedDeck, rng *rand.Rand, window int) (*WeightedPicker, error) {
	if len(decks) == 0 {
		return nil, fmt.Errorf("weighted picker needs at least one deck")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if window < 0 {
		return nil, fmt.Errorf("card spacing must be >= 0, got %d", window)
	}

	p := &WeightedPicker{rng: rng, last: -1, window: window}
	seen := make(map[string]struct{}, len(decks))
	for _, d := range decks {
		if d.Weight <= 0 {
			return nil, fmt.Errorf("weight for deck %q must be positive", d.Deck.Name)
		}
		if len(d.Deck.Cards) == 0 {
			return nil, fmt.Errorf("deck %q has no cards", d.Deck.Name)
		}
		if _, ok := seen[d.Deck.Name]; ok {
			return nil, fmt.Errorf("duplicate weighted deck %q", d.Deck.Name)
		}
		seen[d.Deck.Name] = struct{}{}
		cardWeight := float64(d.Weight) / float64(len(d.Deck.Cards))
		for _, c := range d.Deck.Cards {
			p.cards = append(p.cards, weightedCard{deck: d.Deck, card: c, weight: cardWeight})
		}
	}
	if p.window > len(p.cards)-1 {
		p.window = len(p.cards) - 1
	}
	return p, nil
}

// NewWeightedRenderer builds a Renderer that draws from weighted static decks.
func NewWeightedRenderer(w io.Writer, opts Options, decks []WeightedDeck) (*Renderer, error) {
	picker, err := NewWeightedPicker(decks, opts.Rand, opts.Spacing)
	if err != nil {
		return nil, err
	}
	if len(decks) > 0 {
		opts.Deck = decks[0].Deck
	}
	return newRenderer(w, opts, picker), nil
}

// Next returns the selected deck and card. Recent-card avoidance relaxes to an
// immediate-repeat guard if the configured history would exclude every choice.
func (p *WeightedPicker) Next() (deck.Deck, deck.Card) {
	if len(p.cards) == 0 {
		return deck.Deck{}, deck.Card{}
	}
	excluded := make([]bool, len(p.cards))
	marked := 0
	mark := func(i int) {
		if i >= 0 && i < len(p.cards) && !excluded[i] && marked < len(p.cards)-1 {
			excluded[i] = true
			marked++
		}
	}
	mark(p.last)
	for i := len(p.recent) - 1; i >= 0; i-- {
		mark(p.recent[i])
	}

	total := 0.0
	for i, c := range p.cards {
		if !excluded[i] {
			total += c.weight
		}
	}
	target := p.rng.Float64() * total
	selected := -1
	for i, c := range p.cards {
		if excluded[i] {
			continue
		}
		target -= c.weight
		if target < 0 {
			selected = i
			break
		}
	}
	if selected < 0 {
		for i := len(p.cards) - 1; i >= 0; i-- {
			if !excluded[i] {
				selected = i
				break
			}
		}
	}
	p.last = selected
	if p.window > 0 {
		p.recent = append(p.recent, selected)
		if len(p.recent) > p.window {
			p.recent = p.recent[len(p.recent)-p.window:]
		}
	}
	item := p.cards[selected]
	return item.deck, item.card
}
