package hook

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
)

// newDeck builds a hook Deck over specs with an injected runner.
func newDeck(t *testing.T, specs []config.HookSpec, timeout time.Duration, runner Runner) *Deck {
	t.Helper()
	d, err := LoadDeck(Options{Specs: specs, Timeout: timeout, Runner: runner})
	if err != nil {
		t.Fatalf("LoadDeck error = %v", err)
	}
	return d
}

func TestLoadDeckRejectsEmpty(t *testing.T) {
	if _, err := LoadDeck(Options{}); err == nil {
		t.Fatal("LoadDeck with no hooks expected error, got nil")
	}
}

func TestRunSuccess(t *testing.T) {
	runner := func(ctx context.Context, argv []string) ([]byte, error) {
		return []byte("Fast-forwarded to abc123\n"), nil
	}
	d := newDeck(t, []config.HookSpec{{Name: "fetch", Cmd: []string{"git", "fetch"}}}, time.Second, runner)
	res := d.Run(context.Background())
	if res.Cancelled {
		t.Fatal("Run should not be cancelled")
	}
	if !res.Success {
		t.Errorf("Success = false, want true")
	}
	if !strings.HasPrefix(res.Card.Title, "✅") {
		t.Errorf("Card.Title = %q, want ✅ prefix", res.Card.Title)
	}
	if !strings.Contains(res.Card.Text, "Fast-forwarded to abc123") {
		t.Errorf("Card.Text = %q, want last output line", res.Card.Text)
	}
}

func TestRunFailure(t *testing.T) {
	runner := func(ctx context.Context, argv []string) ([]byte, error) {
		return []byte("FAIL: TestFoo\n"), &exec.ExitError{}
	}
	d := newDeck(t, []config.HookSpec{{Name: "test", Cmd: []string{"go", "test"}}}, time.Second, runner)
	res := d.Run(context.Background())
	if res.Success {
		t.Errorf("Success = true, want false")
	}
	if !strings.HasPrefix(res.Card.Title, "❌") {
		t.Errorf("Card.Title = %q, want ❌ prefix", res.Card.Title)
	}
	if !strings.Contains(res.Card.Text, "FAIL: TestFoo") {
		t.Errorf("Card.Text = %q, want failing line", res.Card.Text)
	}
}

func TestRunTimeout(t *testing.T) {
	// Runner respects its context: it blocks until the derived timeout fires,
	// then returns the context error, exactly like exec.CommandContext.
	runner := func(ctx context.Context, argv []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	d := newDeck(t, []config.HookSpec{{Name: "slow", Cmd: []string{"sleep", "99"}}}, 20*time.Millisecond, runner)
	res := d.Run(context.Background())
	if res.Cancelled {
		t.Fatal("timeout should not be reported as Cancelled")
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if res.Success {
		t.Errorf("Success = true, want false on timeout")
	}
	if !strings.Contains(res.Card.Text, "timed out") {
		t.Errorf("Card.Text = %q, want timed out", res.Card.Text)
	}
}

func TestRunCancelledOnEarlyIdle(t *testing.T) {
	// The BUSY window (parent ctx) is cancelled while the hook is still running:
	// the agent came back. Run must report Cancelled and produce no card.
	runner := func(ctx context.Context, argv []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	d := newDeck(t, []config.HookSpec{{Name: "slow", Cmd: []string{"sleep", "99"}}}, time.Hour, runner)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	res := d.Run(ctx)
	if !res.Cancelled {
		t.Fatalf("Cancelled = false, want true; res = %+v", res)
	}
	if res.Card.Title != "" || res.Card.Text != "" {
		t.Errorf("cancelled Run should produce no card, got %+v", res.Card)
	}
}

func TestRunRoundRobin(t *testing.T) {
	var ran []string
	runner := func(ctx context.Context, argv []string) ([]byte, error) {
		ran = append(ran, argv[0])
		return []byte("ok"), nil
	}
	d := newDeck(t, []config.HookSpec{
		{Name: "a", Cmd: []string{"a"}},
		{Name: "b", Cmd: []string{"b"}},
	}, time.Second, runner)
	d.Run(context.Background())
	d.Run(context.Background())
	d.Run(context.Background())
	want := []string{"a", "b", "a"}
	if strings.Join(ran, ",") != strings.Join(want, ",") {
		t.Errorf("round-robin order = %v, want %v", ran, want)
	}
}

func TestLastNonEmptyLineTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	c := renderCard("h", []byte("first\n\n"+long+"\n\n"), nil, false)
	if !strings.HasSuffix(c.Text, "…") {
		t.Errorf("expected truncation ellipsis, got %q", c.Text)
	}
	if strings.Contains(c.Text, "first") {
		t.Errorf("should show last line, not first: %q", c.Text)
	}
}

func TestExitDetailExitError(t *testing.T) {
	// A generic error still renders something; exit codes come through for real
	// *exec.ExitError values.
	if got := exitDetail(errors.New("boom")); got != "boom" {
		t.Errorf("exitDetail(generic) = %q, want boom", got)
	}
}
