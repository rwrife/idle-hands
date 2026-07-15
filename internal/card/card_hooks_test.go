package card

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// TestHooksFireOnBusyIdleCycle verifies the OnShow/OnDismiss callbacks fire in
// order across a BUSY→IDLE window: OnShow once when the card is drawn (with the
// deck name and card title), OnDismiss once when it's cleared on IDLE.
func TestHooksFireOnBusyIdleCycle(t *testing.T) {
	var mu sync.Mutex
	type shown struct{ deckName, title string }
	var shows []shown
	var dismisses int

	r := NewRenderer(&bytes.Buffer{}, Options{
		Deck: singleDeck(),
		OnShow: func(d, title string) {
			mu.Lock()
			shows = append(shows, shown{d, title})
			mu.Unlock()
		},
		OnDismiss: func() {
			mu.Lock()
			dismisses++
			mu.Unlock()
		},
	})

	r.OnBusy(25 * time.Second)
	r.OnIdle(25 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(shows) != 1 {
		t.Fatalf("OnShow fired %d times, want 1", len(shows))
	}
	if shows[0].deckName != "solo" || shows[0].title != "Only" {
		t.Errorf("OnShow got %+v, want deck=solo title=Only", shows[0])
	}
	if dismisses != 1 {
		t.Errorf("OnDismiss fired %d times, want 1", dismisses)
	}
}

// TestNoDismissWithoutShow verifies OnDismiss does not fire when no card was
// ever shown for the window (e.g. an IDLE with no preceding BUSY card).
func TestNoDismissWithoutShow(t *testing.T) {
	var dismisses int
	r := NewRenderer(&bytes.Buffer{}, Options{
		Deck:      singleDeck(),
		OnDismiss: func() { dismisses++ },
	})
	// OnIdle with nothing shown: the "agent's back" line still prints, but no
	// card was on screen, so no dismiss event should fire.
	r.OnIdle(5 * time.Second)
	if dismisses != 0 {
		t.Errorf("OnDismiss fired %d times with no card shown, want 0", dismisses)
	}
}

// TestHooksOptionalNoPanic verifies a renderer with nil hooks (the default)
// runs a full cycle without panicking.
func TestHooksOptionalNoPanic(t *testing.T) {
	r := NewRenderer(&bytes.Buffer{}, Options{Deck: deck.Deck{
		Name:  "d",
		Cards: []deck.Card{{Title: "T", Text: "B"}},
	}})
	r.OnBusy(time.Second)
	r.OnIdle(time.Second)
}
