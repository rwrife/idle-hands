package card

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// flashDeck is a small deck used to exercise reveal mode: distinct fronts and
// backs so the test can tell a question from its answer.
func flashDeck() deck.Deck {
	return deck.Deck{
		Name:  "srs",
		Emoji: "🧠",
		Cards: []deck.Card{
			{Title: "Capital of France?", Text: "Paris"},
			{Title: "2 + 2 = ?", Text: "Four"},
		},
	}
}

// manualTimer is a controllable revealTimer: it captures the callback so a test
// can fire it exactly when it wants instead of sleeping, and records Stop calls.
type manualTimer struct {
	fn      func()
	stopped bool
	fired   bool
}

func (m *manualTimer) Stop() bool {
	m.stopped = true
	return !m.fired
}

func (m *manualTimer) fire() {
	if !m.stopped {
		m.fired = true
		m.fn()
	}
}

// newManualRenderer builds a reveal-mode renderer whose timer the returned
// *manualTimer controls.
func newManualRenderer(t *testing.T, buf *bytes.Buffer) (*Renderer, *manualTimer) {
	t.Helper()
	mt := &manualTimer{}
	r := NewRenderer(buf, Options{
		Deck:   flashDeck(),
		Rand:   rand.New(rand.NewSource(1)),
		Reveal: 6 * time.Second,
		newTimer: func(_ time.Duration, fn func()) revealTimer {
			mt.fn = fn
			return mt
		},
	})
	return r, mt
}

// TestRevealHidesAnswerUntilTimer is the core reveal-mode invariant: the first
// frame shows the question (a card title) but NOT its answer body, and the
// answer only appears after the timer fires. Neither step reads stdin, so the
// wrapped agent is never blocked (the renderer never touches input at all).
func TestRevealHidesAnswerUntilTimer(t *testing.T) {
	var buf bytes.Buffer
	r, mt := newManualRenderer(t, &buf)

	r.OnBusy(20 * time.Second)
	first := stripANSI(buf.String())

	// Whatever card was picked, its title must be present and its answer absent.
	// With seed 1 and two cards the first pick is deterministic, but assert
	// generically against both possible answers to stay robust.
	if !strings.Contains(first, "?") {
		t.Fatalf("front frame missing a question:\n%s", first)
	}
	if strings.Contains(first, "Paris") || strings.Contains(first, "Four") {
		t.Fatalf("answer leaked before reveal timer fired:\n%s", first)
	}
	if !strings.Contains(first, "no keypress needed") {
		t.Errorf("front frame missing the non-blocking reveal hint:\n%s", first)
	}

	buf.Reset()
	mt.fire() // simulate the reveal delay elapsing

	revealed := stripANSI(buf.String())
	if !(strings.Contains(revealed, "Paris") || strings.Contains(revealed, "Four")) {
		t.Errorf("answer not shown after reveal timer fired:\n%s", revealed)
	}
	// The reveal redraws in place: it must erase the front frame first.
	if !strings.Contains(buf.String(), "\x1b[1A") || !strings.Contains(buf.String(), "\x1b[2K") {
		t.Error("reveal did not erase the front frame before redrawing")
	}
}

// TestRevealCanceledOnIdle ensures that if the agent returns before the reveal
// fires, OnIdle stops the timer and a late fire is a no-op (no stale answer
// scribbled onto a cleared screen).
func TestRevealCanceledOnIdle(t *testing.T) {
	var buf bytes.Buffer
	r, mt := newManualRenderer(t, &buf)

	r.OnBusy(20 * time.Second)
	r.OnIdle(2 * time.Second)
	if !mt.stopped {
		t.Fatal("OnIdle did not stop the pending reveal timer")
	}

	buf.Reset()
	mt.fire() // a late timer that lost the race must do nothing
	if buf.Len() != 0 {
		t.Errorf("late reveal wrote to a cleared screen:\n%s", buf.String())
	}
	if r.Shown() {
		t.Error("card still marked shown after OnIdle")
	}
}

// TestRevealOnlyOncePerWindow confirms a second OnBusy without an intervening
// OnIdle neither renders another card nor schedules a second reveal.
func TestRevealOnlyOncePerWindow(t *testing.T) {
	var buf bytes.Buffer
	r, mt := newManualRenderer(t, &buf)

	r.OnBusy(20 * time.Second)
	first := buf.String()
	r.OnBusy(25 * time.Second) // no-op
	if buf.String() != first {
		t.Errorf("second OnBusy rendered extra output:\n%s", buf.String()[len(first):])
	}
	// Firing the (single) timer should still reveal exactly one answer frame.
	buf.Reset()
	mt.fire()
	if buf.Len() == 0 {
		t.Error("expected a reveal frame from the one scheduled timer")
	}
}

// TestNonRevealDeckShowsBodyImmediately guards the default (non-flashcard)
// path: with Reveal unset the body is drawn in the first frame, exactly as the
// M4 decks behave, and no timer machinery is engaged.
func TestNonRevealDeckShowsBodyImmediately(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, Options{Deck: flashDeck(), Rand: rand.New(rand.NewSource(1))})
	r.OnBusy(20 * time.Second)
	out := stripANSI(buf.String())
	if !(strings.Contains(out, "Paris") || strings.Contains(out, "Four")) {
		t.Errorf("non-reveal deck should show the body immediately:\n%s", out)
	}
}

// TestSpacedPickerAvoidsRecentWindow verifies the spacing rule: with a window
// of 2 over a 4-card deck, no card returned is one of the previous two (a
// stronger guarantee than merely "no immediate repeat").
func TestSpacedPickerAvoidsRecentWindow(t *testing.T) {
	d := deck.Deck{
		Name: "big",
		Cards: []deck.Card{
			{Title: "A", Text: "a"},
			{Title: "B", Text: "b"},
			{Title: "C", Text: "c"},
			{Title: "D", Text: "d"},
		},
	}
	p := NewSpacedPicker(d, rand.New(rand.NewSource(9)), 2)

	var history []string
	for i := 0; i < 500; i++ {
		got := p.Next().Title
		// The last two picks must not include this one.
		n := len(history)
		if n >= 1 && history[n-1] == got {
			t.Fatalf("iter %d: immediate repeat of %q", i, got)
		}
		if n >= 2 && history[n-2] == got {
			t.Fatalf("iter %d: %q reappeared within the 2-card spacing window", i, got)
		}
		history = append(history, got)
	}
}

// TestSpacedPickerDegradesWhenWindowTooLarge ensures a spacing window larger
// than the deck can't wedge the picker: it still returns a card every time and
// never immediately repeats, even though full spacing is impossible.
func TestSpacedPickerDegradesWhenWindowTooLarge(t *testing.T) {
	d := deck.Deck{
		Name:  "tiny",
		Cards: []deck.Card{{Title: "A", Text: "a"}, {Title: "B", Text: "b"}},
	}
	p := NewSpacedPicker(d, rand.New(rand.NewSource(3)), 10) // window >> deck size
	prev := ""
	for i := 0; i < 100; i++ {
		got := p.Next().Title
		if got == "" {
			t.Fatalf("iter %d: empty card (picker wedged)", i)
		}
		if got == prev {
			t.Fatalf("iter %d: immediate repeat of %q", i, got)
		}
		prev = got
	}
}
