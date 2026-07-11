package card

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// syncBuf is a mutex-guarded bytes.Buffer so a test can safely read what the
// async render goroutine writes without racing on the underlying buffer. The
// renderer serializes its own writes under its lock; this only guards the
// test's concurrent String() reads.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// placeholderDeck is the one-card deck the hook renderer shows while the hook
// runs, before the async result replaces it.
func placeholderDeck() deck.Deck {
	return deck.Deck{
		Name:  "hook",
		Emoji: "🪝",
		Cards: []deck.Card{{Title: "running a hook…", Text: "doing real work"}},
	}
}

// waitFor polls cond up to a second so async goroutines have time to run
// without the test sleeping a fixed, flaky amount.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// TestAsyncReplacesPlaceholder verifies the hook flow: OnBusy shows the
// placeholder, then the async producer's card is drawn in place.
func TestAsyncReplacesPlaceholder(t *testing.T) {
	var buf syncBuf
	done := make(chan struct{})
	r := NewRenderer(&buf, Options{
		Deck: placeholderDeck(),
		Async: func(ctx context.Context) (deck.Card, bool) {
			<-done
			return deck.Card{Title: "✅ fetch", Text: "ok · Fast-forwarded"}, true
		},
	})

	r.OnBusy(3 * time.Second)
	if !strings.Contains(buf.String(), "running a hook") {
		t.Fatalf("placeholder not shown; got:\n%s", buf.String())
	}
	close(done) // let the producer finish
	waitFor(t, func() bool { return strings.Contains(buf.String(), "Fast-forwarded") })
	if !strings.Contains(buf.String(), "✅ fetch") {
		t.Errorf("async card not drawn; got:\n%s", buf.String())
	}
}

// TestAsyncCancelledOnIdle verifies that when OnIdle fires before the producer
// finishes, its ctx is cancelled and a late result is dropped (no stale card
// scribbled over the cleared screen).
func TestAsyncCancelledOnIdle(t *testing.T) {
	var buf syncBuf
	started := make(chan struct{})
	sawCancel := make(chan struct{})
	r := NewRenderer(&buf, Options{
		Deck: placeholderDeck(),
		Async: func(ctx context.Context) (deck.Card, bool) {
			close(started)
			<-ctx.Done() // block until the window is cancelled
			close(sawCancel)
			return deck.Card{Title: "LATE", Text: "should be dropped"}, true
		},
	})

	r.OnBusy(3 * time.Second)
	<-started
	r.OnIdle(3 * time.Second)

	select {
	case <-sawCancel:
	case <-time.After(time.Second):
		t.Fatal("producer ctx was not cancelled on OnIdle")
	}
	// Give the async goroutine a moment to (attempt to) draw, then confirm the
	// stale "LATE" card never made it to screen.
	waitFor(t, func() bool { return strings.Contains(buf.String(), "agent's back") })
	if strings.Contains(buf.String(), "LATE") {
		t.Errorf("stale async card drawn after OnIdle:\n%s", buf.String())
	}
}

// TestAsyncNoShowClearsPlaceholder verifies show=false clears the placeholder
// and draws no replacement card.
func TestAsyncNoShowClearsPlaceholder(t *testing.T) {
	var buf syncBuf
	r := NewRenderer(&buf, Options{
		Deck: placeholderDeck(),
		Async: func(ctx context.Context) (deck.Card, bool) {
			return deck.Card{}, false
		},
	})
	r.OnBusy(2 * time.Second)
	waitFor(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.lastLine == 0
	})
	r.OnIdle(2 * time.Second)
	if strings.Contains(buf.String(), "running a hook") && !strings.Contains(buf.String(), "agent's back") {
		t.Errorf("expected placeholder cleared and idle line; got:\n%s", buf.String())
	}
}
