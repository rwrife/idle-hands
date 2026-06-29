package card

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// ansiRE strips ANSI escape sequences so tests can assert on visible text
// regardless of lipgloss styling/color.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func multiDeck() deck.Deck {
	return deck.Deck{
		Name:  "test",
		Emoji: "🧪",
		Cards: []deck.Card{
			{Title: "One", Text: "first body"},
			{Title: "Two", Text: "second body"},
			{Title: "Three", Text: "third body"},
		},
	}
}

func singleDeck() deck.Deck {
	return deck.Deck{
		Name:  "solo",
		Cards: []deck.Card{{Title: "Only", Text: "only body"}},
	}
}

// TestPickerNoImmediateRepeat drives the picker many times with a fixed seed
// and asserts the same card never appears twice in a row (the M4 "no immediate
// repeats" rule).
func TestPickerNoImmediateRepeat(t *testing.T) {
	p := NewPicker(multiDeck(), rand.New(rand.NewSource(1)))
	prev := ""
	for i := 0; i < 500; i++ {
		c := p.Next()
		if c.Title == "" {
			t.Fatalf("iteration %d: empty card", i)
		}
		if c.Title == prev {
			t.Fatalf("iteration %d: immediate repeat of %q", i, c.Title)
		}
		prev = c.Title
	}
}

// TestPickerCoversAllCards confirms the picker can return every card (it isn't
// accidentally pinned to a subset).
func TestPickerCoversAllCards(t *testing.T) {
	p := NewPicker(multiDeck(), rand.New(rand.NewSource(7)))
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		seen[p.Next().Title] = true
	}
	for _, want := range []string{"One", "Two", "Three"} {
		if !seen[want] {
			t.Errorf("card %q never selected", want)
		}
	}
}

// TestPickerSingleCard returns the lone card every time without claiming a
// repeat violation (there is nothing else to pick).
func TestPickerSingleCard(t *testing.T) {
	p := NewPicker(singleDeck(), rand.New(rand.NewSource(1)))
	for i := 0; i < 5; i++ {
		if got := p.Next().Title; got != "Only" {
			t.Fatalf("iteration %d: got %q, want Only", i, got)
		}
	}
}

// TestRenderContainsCardText verifies the rendered frame carries the deck emoji,
// the card title, the body text, and a "thinking" header with the idle span.
func TestRenderContainsCardText(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: multiDeck(), Rand: rand.New(rand.NewSource(2))})
	r.OnBusy(22 * time.Second)

	out := stripANSI(buf.String())
	if !strings.Contains(out, "thinking") {
		t.Errorf("rendered card missing 'thinking' header:\n%s", out)
	}
	if !strings.Contains(out, "22s") {
		t.Errorf("rendered card missing idle span '22s':\n%s", out)
	}
	if !strings.Contains(out, "🧪") {
		t.Errorf("rendered card missing deck emoji:\n%s", out)
	}
	// Body of whichever card was picked should be present.
	if !strings.Contains(out, "body") {
		t.Errorf("rendered card missing card body text:\n%s", out)
	}
}

// TestOneCardPerBusyWindow is the crux M4 invariant: a second OnBusy without an
// intervening OnIdle must NOT render another card.
func TestOneCardPerBusyWindow(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: multiDeck(), Rand: rand.New(rand.NewSource(3))})

	r.OnBusy(20 * time.Second)
	if !r.Shown() {
		t.Fatal("expected a card to be shown after first OnBusy")
	}
	first := buf.String()

	r.OnBusy(25 * time.Second) // should be a no-op
	if buf.String() != first {
		t.Errorf("second OnBusy rendered additional output:\n%s", buf.String()[len(first):])
	}
}

// TestClearOnIdle checks OnIdle prints the "agent's back" line with the
// reclaimed span and resets the latch so the next window shows a fresh card.
func TestClearOnIdle(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: multiDeck(), Rand: rand.New(rand.NewSource(4))})

	r.OnBusy(20 * time.Second)
	buf.Reset()
	r.OnIdle(3 * time.Second)

	out := stripANSI(buf.String())
	if !strings.Contains(out, "agent's back") {
		t.Errorf("OnIdle missing 'agent's back':\n%s", out)
	}
	if !strings.Contains(out, "3s") {
		t.Errorf("OnIdle missing reclaimed span '3s':\n%s", out)
	}
	if r.Shown() {
		t.Error("latch not reset after OnIdle")
	}

	// A fresh window should now render again.
	buf.Reset()
	r.OnBusy(20 * time.Second)
	if !r.Shown() || buf.Len() == 0 {
		t.Error("expected a fresh card on the next BUSY window")
	}
}

// TestClearSeqErasesRenderedLines verifies OnIdle emits one cursor-up + erase
// pair per rendered line (so the card is removed in place, not left as scroll
// litter), proportional to the card height.
func TestClearSeqErasesRenderedLines(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: multiDeck(), Rand: rand.New(rand.NewSource(5))})
	r.OnBusy(20 * time.Second)
	if r.lastLine <= 0 {
		t.Fatalf("expected lastLine > 0, got %d", r.lastLine)
	}
	want := r.lastLine

	buf.Reset()
	r.OnIdle(2 * time.Second)
	got := strings.Count(buf.String(), "\x1b[1A")
	if got != want {
		t.Errorf("cursor-up count = %d, want %d (one per rendered line)", got, want)
	}
	if !strings.Contains(buf.String(), "\x1b[2K") {
		t.Error("clear sequence missing line-erase escape")
	}
}

// TestIdleWithoutBusyStillAnnounces ensures OnIdle is safe even if no card was
// shown (defensive: no clear sequence, but still prints the back line).
func TestIdleWithoutBusyStillAnnounces(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: multiDeck(), Rand: rand.New(rand.NewSource(6))})
	r.OnIdle(1 * time.Second)
	out := stripANSI(buf.String())
	if !strings.Contains(out, "agent's back") {
		t.Errorf("expected back line, got:\n%s", out)
	}
	if strings.Contains(buf.String(), "\x1b[1A") {
		t.Error("should not emit cursor-up clear when nothing was shown")
	}
}
