// Package duckdiff turns the staged git diff you're waiting on into a single
// sharp rubber-duck question, so a BUSY window hands you one thing to *think
// about the change itself* instead of a generic prompt. It is the loader behind
// the built-in-feeling "duckdiff" deck: point config at it (deck = "duckdiff"),
// and each idle window shows one review question generated from `git diff
// --cached` by a local Ollama model.
//
// The hard contract (issue #9) is that this must never get in the way of the
// agent you're waiting on:
//
//   - It reads the staged diff from the current working directory. No repo, or
//     no staged changes, is not an error — it just means there's nothing to
//     duck, so we fall back to the static `duck` deck.
//   - The Ollama call is optional and time-boxed. Ollama down, unreachable, or
//     slow past the timeout all resolve to the same graceful outcome: the
//     static `duck` deck. We never block the watch loop or the wrapped agent on
//     a model.
//   - On success we surface exactly one question card. We ask the model for one
//     line; if it rambles we keep the first meaningful line.
//
// Everything expensive (running git, calling Ollama) is injected through the
// Options, so the package is fully unit-testable from strings with no real repo
// and no model server. LoadDeck is the single entry point the watch loop and
// the `deck` preview both use, so what you preview is what you get at runtime.
package duckdiff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// DeckName is the reserved name of the diff-review deck. Selecting it in config
// (deck = "duckdiff") makes watch build a card from the staged diff via Ollama,
// falling back to the static "duck" deck whenever that isn't possible.
const DeckName = "duckdiff"

// deckEmoji and deckDescription flavor the "duckdiff" deck in `deck`
// list/preview output so it reads like the shipped decks even though its one
// card is generated live from your diff.
const (
	deckEmoji       = "🦆"
	deckDescription = "One review question about your staged diff (local LLM)."
)

// Defaults for the optional Ollama call. They match Ollama's out-of-the-box
// local endpoint so `deck = "duckdiff"` works with zero extra config for anyone
// already running Ollama, and stay well inside a typical agent think window.
const (
	// DefaultModel is the Ollama model asked for the review question. It is a
	// small, commonly-pulled instruct model so a first run is likely to hit
	// something the user already has; any model can be set in config.
	DefaultModel = "llama3.2"
	// DefaultURL is Ollama's default local generate endpoint.
	DefaultURL = "http://localhost:11434/api/generate"
	// DefaultTimeout bounds the whole model round-trip. Past it we fall back to
	// the static duck deck rather than make the user wait on the model.
	DefaultTimeout = 4 * time.Second
	// maxDiffBytes caps how much of the staged diff we send to the model, so a
	// huge refactor doesn't blow up the prompt (or the latency). The head of a
	// diff is where the signal usually is.
	maxDiffBytes = 6000
)

// promptTemplate frames the diff for the model. It asks for exactly one short
// question and forbids preamble, which keeps the single-line extraction honest.
const promptTemplate = `You are a sharp code reviewer helping a developer while they wait for an AI agent.
Read the following staged git diff and ask ONE short, specific rubber-duck review question about it.
Rules: output only the question, one line, no preamble, no numbering, under 140 characters.

Staged diff:
%s

Your one question:`

// Options configure a LoadDeck call. Every side-effecting dependency is
// injectable so the package is testable without a real repo or Ollama; nil
// fields select the real implementations.
type Options struct {
	// Model is the Ollama model to ask. Empty selects DefaultModel.
	Model string
	// URL is the Ollama generate endpoint. Empty selects DefaultURL.
	URL string
	// Timeout bounds the model round-trip. <= 0 selects DefaultTimeout.
	Timeout time.Duration
	// Dir is the working directory whose staged diff is read. Empty means the
	// process's current directory (git's own default).
	Dir string

	// stagedDiff returns the staged diff for Dir and whether the directory is a
	// git repo at all. nil selects the real `git diff --cached` implementation.
	// Injected in tests.
	stagedDiff func(dir string) (diff string, isRepo bool, err error)
	// ask sends prompt to the model and returns its raw completion. nil selects
	// the real Ollama HTTP call. Injected in tests.
	ask func(ctx context.Context, url, model, prompt string) (string, error)
	// fallback returns the deck used whenever a live question can't be made. nil
	// selects the embedded static "duck" deck. Injected in tests.
	fallback func() (deck.Deck, error)
}

// Result reports how LoadDeck resolved, so callers (the watch loop, the `deck`
// preview) can tell the user whether they're seeing a live question or the
// static fallback and why. The Deck is always usable regardless of Reason.
type Result struct {
	// Deck is the deck to show: a one-card live deck on success, otherwise the
	// static duck deck. Never empty on a nil error.
	Deck deck.Deck
	// Live is true when Deck holds a freshly generated question; false when it
	// is the static duck fallback.
	Live bool
	// Reason is a short human phrase explaining the outcome (e.g. "not a git
	// repo", "no staged changes", "ollama unavailable"). Empty on a live hit.
	Reason string
}

// LoadDeck builds the diff-review deck. It never returns an error for the
// expected "can't make a live question" cases (no repo, no staged changes,
// Ollama down or slow); those resolve to the static duck deck with a Reason set.
// It only returns an error if even the fallback deck can't be loaded, which for
// the embedded duck deck is a build-time bug rather than a runtime condition.
func LoadDeck(opts Options) (Result, error) {
	getDiff := opts.stagedDiff
	if getDiff == nil {
		getDiff = gitStagedDiff
	}
	fallback := opts.fallback
	if fallback == nil {
		fallback = builtinDuck
	}

	fallbackResult := func(reason string) (Result, error) {
		d, err := fallback()
		if err != nil {
			return Result{}, fmt.Errorf("duckdiff fallback deck: %w", err)
		}
		return Result{Deck: d, Live: false, Reason: reason}, nil
	}

	diff, isRepo, err := getDiff(opts.Dir)
	switch {
	case err != nil:
		return fallbackResult("git unavailable")
	case !isRepo:
		return fallbackResult("not a git repo")
	case strings.TrimSpace(diff) == "":
		return fallbackResult("no staged changes")
	}

	ask := opts.ask
	if ask == nil {
		ask = ollamaAsk
	}
	model := firstNonEmpty(opts.Model, DefaultModel)
	url := firstNonEmpty(opts.URL, DefaultURL)
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	prompt := fmt.Sprintf(promptTemplate, truncateDiff(diff, maxDiffBytes))
	raw, err := ask(ctx, url, model, prompt)
	if err != nil {
		return fallbackResult("ollama unavailable")
	}
	question := firstQuestionLine(raw)
	if question == "" {
		return fallbackResult("model returned no usable question")
	}

	return Result{
		Deck: deck.Deck{
			Name:        DeckName,
			Description: deckDescription,
			Emoji:       deckEmoji,
			Cards: []deck.Card{{
				Title: "Duck the diff",
				Text:  question,
			}},
		},
		Live: true,
	}, nil
}

// gitStagedDiff runs `git diff --cached` in dir and returns the staged diff. It
// distinguishes "not a git repo" (isRepo=false, no error) from a real failure
// to run git, so LoadDeck can fall back quietly in the common not-a-repo case
// without treating it as an error. An empty dir uses the process cwd.
func gitStagedDiff(dir string) (string, bool, error) {
	if !gitIsRepo(dir) {
		return "", false, nil
	}
	// --no-color so the model sees a clean diff; -U3 keeps a little context.
	out, err := runGit(dir, "diff", "--cached", "--no-color", "-U3")
	if err != nil {
		return "", true, err
	}
	return out, true, nil
}

// gitIsRepo reports whether dir is inside a git working tree. It shells out to
// `git rev-parse --is-inside-work-tree`, treating any non-success as "not a
// repo" so a missing git binary degrades to the fallback rather than an error.
func gitIsRepo(dir string) bool {
	out, err := runGit(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

// runGit runs git with args in dir (cwd when empty) and returns stdout. A
// non-zero exit is an error carrying trimmed stderr for context.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// ollamaRequest is the minimal Ollama /api/generate request body. Stream is
// false so we get one JSON object back instead of a token stream — simpler to
// bound and parse for a single short question.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaResponse is the slice of Ollama's response we use: the completed text.
type ollamaResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

// ollamaAsk POSTs prompt to a local Ollama generate endpoint and returns the
// model's completion. The context bounds the whole call; a cancel/timeout,
// connection refusal, non-200, or API error all surface as an error so LoadDeck
// falls back to the static deck. The HTTP client has no timeout of its own —
// the context is the single source of truth for the deadline.
func ollamaAsk(ctx context.Context, url, model, prompt string) (string, error) {
	body, err := json.Marshal(ollamaRequest{Model: model, Prompt: prompt, Stream: false})
	if err != nil {
		return "", fmt.Errorf("encode ollama request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var or ollamaResponse
	if err := json.Unmarshal(data, &or); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if strings.TrimSpace(or.Error) != "" {
		return "", fmt.Errorf("ollama error: %s", or.Error)
	}
	return or.Response, nil
}

// builtinDuck returns the embedded static "duck" deck used as the fallback. It
// is the same deck a user gets with deck = "duck", so the fallback is a genuine
// rubber-duck prompt rather than a dead end.
func builtinDuck() (deck.Deck, error) {
	return deck.Builtin("duck")
}

// firstQuestionLine extracts one clean question from a model completion. Models
// sometimes wrap the answer in preamble, markdown, headings, or numbering
// despite the instructions, so cleaning happens per line and we prefer the
// first line that actually reads as a question (ends with "?"). If no line ends
// with "?" we fall back to the first cleaned non-empty line, so a question
// phrased without a literal "?" still surfaces. An empty result means the model
// gave us nothing usable and the caller should fall back.
func firstQuestionLine(raw string) string {
	var first string
	for _, line := range strings.Split(raw, "\n") {
		s := cleanQuestionLine(line)
		if s == "" {
			continue
		}
		if first == "" {
			first = s // remember the first usable line as a fallback
		}
		if strings.HasSuffix(s, "?") {
			return clampLen(s, 200)
		}
	}
	return clampLen(first, 200)
}

// cleanQuestionLine strips the common decoration a model may put on a single
// output line — markdown quote/heading/bullet markers, a leading list
// enumerator, and wrapping quotes/backticks — and trims it. It returns "" for a
// line that is only decoration (e.g. a bare "###").
func cleanQuestionLine(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "> ")   // markdown quote
	s = strings.TrimLeft(s, "-*# \t") // bullets / headings
	s = stripLeadingEnumerator(s)     // "1." / "1)" prefixes
	s = strings.Trim(s, "\"'`")       // wrapping quotes/backticks
	return strings.TrimSpace(s)
}

// stripLeadingEnumerator drops a leading "12." or "12)" list marker (and the
// following space) if present, so a numbered answer becomes a bare question.
func stripLeadingEnumerator(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return s
	}
	if s[i] == '.' || s[i] == ')' {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// truncateDiff returns at most max bytes of diff, appending a marker when it had
// to cut so the model knows the diff continued. It cuts on a line boundary when
// one is nearby so the model isn't handed a half line.
func truncateDiff(diff string, max int) string {
	if len(diff) <= max {
		return diff
	}
	cut := diff[:max]
	if nl := strings.LastIndexByte(cut, '\n'); nl > max/2 {
		cut = cut[:nl]
	}
	return cut + "\n… (diff truncated)"
}

// clampLen bounds s to max runes, appending an ellipsis when it trims, so a
// stray long line from the model can't produce an unwieldy card.
func clampLen(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

// firstNonEmpty returns a if it is non-empty (after trimming), else b. Used to
// apply defaults for the model name and URL.
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
