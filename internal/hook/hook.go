// Package hook turns an idle window into real background work. Instead of
// nudging you to do something, a hook runs one short user-configured command
// (git fetch, a single fast test, go vet, a lint pass) the moment a BUSY window
// opens, then shows the result as the card. The wait does something useful and
// still finishes — or is cancelled — before the agent comes back.
//
// This is backlog item #8 (issue #22). It slots into the existing deck/card
// model: a hook is just a card whose content is the captured exit status and
// last line of output of a command, rendered by the same lipgloss card.
//
// The hard contract, mirroring the other "live" decks (duckdiff), is that a
// hook must never get in the way of the agent you're waiting on:
//
//   - Hooks are strictly opt-in. Only commands the user placed in [[hooks]]
//     ever run; nothing is inferred, word-split, or shell-expanded. Cmd is an
//     argv slice executed directly.
//   - Every run is bounded by BOTH the BUSY window (a context the caller
//     cancels on IDLE) and a hard per-hook timeout, whichever fires first. A
//     hook that outlives the wait is killed.
//   - A cancelled hook (agent came back early) produces no card; the caller
//     simply records a normal reclaimed window.
//
// Running the command is injected through Options.Runner so the package is
// fully unit-testable with no real subprocess.
package hook

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rwrife/idle-hands/internal/config"
	"github.com/rwrife/idle-hands/internal/deck"
)

// DeckName is the reserved name of the hook deck. Selecting it in config
// (deck = "hook") makes watch run one registered hook per BUSY window and show
// its result as the card. It has no static cards of its own; the [[hooks]]
// blocks are its deck.
const DeckName = "hook"

// deckEmoji flavors the hook card so it reads like the shipped decks.
const deckEmoji = "🪝"

// DefaultTimeout is the hard ceiling for a single hook when config sets no
// hook_timeout. It mirrors config.DefaultHookTimeout.
const DefaultTimeout = 10 * time.Second

// maxOutputBytes caps how much hook output we keep in memory. We only ever show
// the last non-empty line, so a chatty command can't blow up memory; we keep a
// generous tail so that last line is intact.
const maxOutputBytes = 64 * 1024

// Result is the outcome of running one hook: the resolved card to show plus
// enough structured detail for tests and callers to reason about it.
type Result struct {
	// Card is the rendered-ready card (title = hook name, body = status +
	// last output line). Valid whenever Cancelled is false.
	Card deck.Card
	// Name is the hook that ran.
	Name string
	// Success reports whether the command exited 0.
	Success bool
	// TimedOut reports whether the hard per-hook timeout fired (as opposed to
	// the window-cancel or a normal non-zero exit).
	TimedOut bool
	// Cancelled reports the BUSY window ended before the hook finished. When
	// true, Card is zero and the caller should show no card.
	Cancelled bool
}

// Runner executes a hook command bounded by ctx and returns its combined
// output and exit error (nil on exit 0). It mirrors the small slice of
// os/exec the runner needs so tests can substitute a fake. The default runner
// (DefaultRunner) uses exec.CommandContext.
type Runner func(ctx context.Context, argv []string) (output []byte, err error)

// Options configure a hook Deck.
type Options struct {
	// Specs are the registered hooks (from config). At least one is required;
	// LoadDeck errors on an empty list so deck = "hook" with no [[hooks]] is a
	// clear, actionable error rather than a silently empty deck.
	Specs []config.HookSpec
	// Timeout is the hard per-hook ceiling. <= 0 selects DefaultTimeout.
	Timeout time.Duration
	// Runner runs a hook command. nil selects DefaultRunner.
	Runner Runner
}

// Deck runs one registered hook per BUSY window and renders its result as a
// card. It is the live source behind deck = "hook": unlike a static deck it has
// no fixed cards; each card is produced on demand by Run. It rotates through
// the configured hooks round-robin so, over several waits, each hook gets a
// turn. It is not safe for concurrent use; the watch loop drives it from one
// goroutine (Run may block on the command, but only one Run is in flight).
type Deck struct {
	specs   []config.HookSpec
	timeout time.Duration
	runner  Runner
	next    int // round-robin cursor into specs
}

// LoadDeck builds a hook Deck from opts. It validates that at least one hook is
// registered (deck = "hook" with no [[hooks]] is an error) and that each hook
// has a name and a non-empty command; config already enforces this, but
// re-checking keeps the deck self-contained and gives a clear error if it is
// ever constructed directly.
func LoadDeck(opts Options) (*Deck, error) {
	if len(opts.Specs) == 0 {
		return nil, errors.New("no hooks configured: add at least one [[hooks]] block to use deck = \"hook\"")
	}
	for i, s := range opts.Specs {
		if strings.TrimSpace(s.Name) == "" {
			return nil, fmt.Errorf("hook %d has no name", i)
		}
		if len(s.Cmd) == 0 || strings.TrimSpace(s.Cmd[0]) == "" {
			return nil, fmt.Errorf("hook %q has no command", s.Name)
		}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = DefaultRunner
	}
	return &Deck{
		specs:   append([]config.HookSpec(nil), opts.Specs...),
		timeout: timeout,
		runner:  runner,
	}, nil
}

// Names returns the configured hook names in order (for `deck` preview and
// errors).
func (d *Deck) Names() []string {
	out := make([]string, len(d.specs))
	for i, s := range d.specs {
		out[i] = s.Name
	}
	return out
}

// Emoji returns the hook deck's glyph, so the card renderer flavors it like the
// static decks.
func (d *Deck) Emoji() string { return deckEmoji }

// Run executes the next hook (round-robin) bounded by BOTH the caller's ctx
// (the BUSY window: cancel it on IDLE) and the hard per-hook timeout, whichever
// fires first. It returns a Result whose Card is ready to render.
//
// Cancellation semantics: if ctx is cancelled before the command finishes, Run
// returns Cancelled=true with a zero Card so the caller shows nothing (the
// agent is already back). A timeout, by contrast, is a real outcome the user
// asked to see, so it renders a ❌ "(timed out)" card.
func (d *Deck) Run(ctx context.Context) Result {
	spec := d.specs[d.next%len(d.specs)]
	d.next++

	// Bound by the hard timeout in addition to the window ctx. A derived
	// context means whichever cancels first stops the command.
	runCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	out, err := d.runner(runCtx, append([]string(nil), spec.Cmd...))

	// The window was cancelled: agent came back before the hook finished. Show
	// nothing; the caller records a plain reclaimed window.
	if ctx.Err() != nil {
		return Result{Name: spec.Name, Cancelled: true}
	}

	timedOut := runCtx.Err() == context.DeadlineExceeded
	success := err == nil && !timedOut

	return Result{
		Card:     renderCard(spec.Name, out, err, timedOut),
		Name:     spec.Name,
		Success:  success,
		TimedOut: timedOut,
	}
}

// renderCard turns a hook's outcome into a Card: the title is the hook name
// with a success/failure indicator, the body is the last non-empty line of
// output (truncated), plus a short status line so an empty-output command still
// says something.
func renderCard(name string, out []byte, runErr error, timedOut bool) deck.Card {
	ok := runErr == nil && !timedOut
	indicator := "✅"
	if !ok {
		indicator = "❌"
	}
	title := fmt.Sprintf("%s %s", indicator, name)

	last := lastNonEmptyLine(out)
	var status string
	switch {
	case timedOut:
		status = "timed out"
	case runErr != nil:
		status = "failed (" + exitDetail(runErr) + ")"
	default:
		status = "ok"
	}

	body := status
	if last != "" {
		body = status + " · " + truncate(last, 120)
	}
	return deck.Card{Title: title, Text: body}
}

// lastNonEmptyLine returns the last non-blank line of out, trimmed. Empty when
// there is no output. This is what a user most wants to see from a quick
// command (the summary line, the failing assertion, the fetched ref).
func lastNonEmptyLine(out []byte) string {
	// Only scan a bounded tail so huge output stays cheap.
	if len(out) > maxOutputBytes {
		out = out[len(out)-maxOutputBytes:]
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// truncate shortens s to at most n runes, appending an ellipsis when it cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// exitDetail renders a compact reason from a run error, preferring the exit
// code when the command ran and exited non-zero.
func exitDetail(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ProcessState != nil {
		return fmt.Sprintf("exit %d", ee.ExitCode())
	}
	return err.Error()
}

// DefaultRunner runs argv under ctx with exec.CommandContext, returning the
// combined stdout+stderr. It is the production Runner; tests inject their own.
func DefaultRunner(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	return cmd.CombinedOutput()
}
