package duckdiff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rwrife/idle-hands/internal/deck"
)

// fixedDuck is a tiny stand-in fallback deck so fallback-path tests don't depend
// on the embedded duck deck's exact contents.
func fixedDuck() (deck.Deck, error) {
	return deck.Deck{
		Name:        "duck",
		Description: "fallback",
		Emoji:       "🦆",
		Cards:       []deck.Card{{Title: "Fallback", Text: "static duck question"}},
	}, nil
}

// TestLoadDeckLiveQuestion is the happy path: a repo with staged changes and a
// model that answers produces a one-card live deck carrying the model's line.
func TestLoadDeckLiveQuestion(t *testing.T) {
	res, err := LoadDeck(Options{
		stagedDiff: func(string) (string, bool, error) {
			return "diff --git a/x b/x\n+added line\n", true, nil
		},
		ask: func(_ context.Context, _, _, prompt string) (string, error) {
			if !strings.Contains(prompt, "added line") {
				t.Errorf("prompt missing the diff, got:\n%s", prompt)
			}
			return "Does the new line handle the empty input case?", nil
		},
		fallback: fixedDuck,
	})
	if err != nil {
		t.Fatalf("LoadDeck: %v", err)
	}
	if !res.Live {
		t.Fatalf("Live = false, want true (reason %q)", res.Reason)
	}
	if res.Deck.Name != DeckName {
		t.Errorf("deck name = %q, want %q", res.Deck.Name, DeckName)
	}
	if len(res.Deck.Cards) != 1 {
		t.Fatalf("want exactly 1 card, got %d", len(res.Deck.Cards))
	}
	if got := res.Deck.Cards[0].Text; got != "Does the new line handle the empty input case?" {
		t.Errorf("card text = %q", got)
	}
}

// TestLoadDeckFallbacks covers every expected not-live path: not a repo, no
// staged changes, git error, and Ollama error. All resolve to the static duck
// deck with no error and a Reason set.
func TestLoadDeckFallbacks(t *testing.T) {
	cases := []struct {
		name       string
		diff       func(string) (string, bool, error)
		ask        func(context.Context, string, string, string) (string, error)
		wantReason string
	}{
		{
			name:       "not a repo",
			diff:       func(string) (string, bool, error) { return "", false, nil },
			wantReason: "not a git repo",
		},
		{
			name:       "no staged changes",
			diff:       func(string) (string, bool, error) { return "   \n", true, nil },
			wantReason: "no staged changes",
		},
		{
			name:       "git error",
			diff:       func(string) (string, bool, error) { return "", true, errors.New("boom") },
			wantReason: "git unavailable",
		},
		{
			name:       "ollama error",
			diff:       func(string) (string, bool, error) { return "diff\n+x\n", true, nil },
			ask:        func(context.Context, string, string, string) (string, error) { return "", errors.New("refused") },
			wantReason: "ollama unavailable",
		},
		{
			name:       "model returns blank",
			diff:       func(string) (string, bool, error) { return "diff\n+x\n", true, nil },
			ask:        func(context.Context, string, string, string) (string, error) { return "\n  \n", nil },
			wantReason: "model returned no usable question",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := LoadDeck(Options{stagedDiff: tc.diff, ask: tc.ask, fallback: fixedDuck})
			if err != nil {
				t.Fatalf("LoadDeck: %v", err)
			}
			if res.Live {
				t.Errorf("Live = true, want false")
			}
			if res.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if res.Deck.Name != "duck" {
				t.Errorf("fallback deck name = %q, want the static duck deck", res.Deck.Name)
			}
		})
	}
}

// TestLoadDeckFallbackDeckErrorSurfaces confirms that if even the fallback deck
// can't load, LoadDeck returns an error (rather than an empty deck).
func TestLoadDeckFallbackDeckErrorSurfaces(t *testing.T) {
	_, err := LoadDeck(Options{
		stagedDiff: func(string) (string, bool, error) { return "", false, nil },
		fallback:   func() (deck.Deck, error) { return deck.Deck{}, errors.New("no embedded deck") },
	})
	if err == nil {
		t.Fatal("expected an error when the fallback deck fails to load")
	}
}

// TestLoadDeckTimeout verifies the model call is bounded by the configured
// timeout: an ask that outlives it resolves to the static duck deck. The ask
// honors ctx cancellation the way the real HTTP call does.
func TestLoadDeckTimeout(t *testing.T) {
	res, err := LoadDeck(Options{
		Timeout:    30 * time.Millisecond,
		stagedDiff: func(string) (string, bool, error) { return "diff\n+x\n", true, nil },
		ask: func(ctx context.Context, _, _, _ string) (string, error) {
			select {
			case <-time.After(2 * time.Second):
				return "too late", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		fallback: fixedDuck,
	})
	if err != nil {
		t.Fatalf("LoadDeck: %v", err)
	}
	if res.Live {
		t.Errorf("Live = true, want false (timeout should fall back)")
	}
	if res.Reason != "ollama unavailable" {
		t.Errorf("Reason = %q, want ollama unavailable", res.Reason)
	}
}

// TestLoadDeckPassesConfiguredModelAndURL confirms the configured model/url flow
// through to ask, and defaults apply when they are empty.
func TestLoadDeckPassesConfiguredModelAndURL(t *testing.T) {
	var gotURL, gotModel string
	_, err := LoadDeck(Options{
		Model:      "codellama",
		URL:        "http://example.test/gen",
		stagedDiff: func(string) (string, bool, error) { return "diff\n+x\n", true, nil },
		ask: func(_ context.Context, url, model, _ string) (string, error) {
			gotURL, gotModel = url, model
			return "q?", nil
		},
		fallback: fixedDuck,
	})
	if err != nil {
		t.Fatalf("LoadDeck: %v", err)
	}
	if gotURL != "http://example.test/gen" || gotModel != "codellama" {
		t.Errorf("passed url=%q model=%q, want the configured values", gotURL, gotModel)
	}

	// Empty values select defaults.
	_, err = LoadDeck(Options{
		stagedDiff: func(string) (string, bool, error) { return "diff\n+x\n", true, nil },
		ask: func(_ context.Context, url, model, _ string) (string, error) {
			gotURL, gotModel = url, model
			return "q?", nil
		},
		fallback: fixedDuck,
	})
	if err != nil {
		t.Fatalf("LoadDeck: %v", err)
	}
	if gotURL != DefaultURL || gotModel != DefaultModel {
		t.Errorf("defaulted url=%q model=%q, want %q / %q", gotURL, gotModel, DefaultURL, DefaultModel)
	}
}

// TestFirstQuestionLine checks the messy-model-output cleanup: preamble lines,
// numbering, bullets, quotes, and blank leaders are all handled.
func TestFirstQuestionLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"What breaks if x is nil?", "What breaks if x is nil?"},
		{"\n\n  Is the lock released on the error path?  ", "Is the lock released on the error path?"},
		{"1. Did you add a test for the empty slice?", "Did you add a test for the empty slice?"},
		{"2) Why not reuse the existing helper?", "Why not reuse the existing helper?"},
		{"- Does this need a mutex?", "Does this need a mutex?"},
		{"> Is the error wrapped?", "Is the error wrapped?"},
		{"\"Are you sure about the off-by-one?\"", "Are you sure about the off-by-one?"},
		{"### heading\nReal question here?", "Real question here?"},
		{"", ""},
		{"   \n\t\n", ""},
	}
	for _, tc := range cases {
		if got := firstQuestionLine(tc.in); got != tc.want {
			t.Errorf("firstQuestionLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFirstQuestionLineClamps ensures an absurdly long model line is bounded.
func TestFirstQuestionLineClamps(t *testing.T) {
	long := "Q " + strings.Repeat("very ", 100) + "long?"
	got := firstQuestionLine(long)
	if len([]rune(got)) > 201 { // 200 + ellipsis
		t.Errorf("length = %d, want clamped to ~200", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis on the clamped line, got %q", got)
	}
}

// TestTruncateDiff bounds the diff and marks the cut.
func TestTruncateDiff(t *testing.T) {
	small := "line1\nline2\n"
	if got := truncateDiff(small, 1000); got != small {
		t.Errorf("small diff changed: %q", got)
	}

	big := strings.Repeat("some diff line here\n", 1000)
	got := truncateDiff(big, 200)
	if len(got) > 200+len("\n… (diff truncated)") {
		t.Errorf("truncated length = %d, want <= ~200 + marker", len(got))
	}
	if !strings.Contains(got, "diff truncated") {
		t.Errorf("expected a truncation marker, got tail: %q", got[len(got)-40:])
	}
}

// TestOllamaAskRoundTrip exercises the real HTTP path against a stub server so
// the request shape and response parsing are covered without a live Ollama.
func TestOllamaAskRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		fmt.Fprint(w, `{"response":"Is the nil check needed?","done":true}`)
	}))
	defer srv.Close()

	out, err := ollamaAsk(context.Background(), srv.URL, "m", "prompt")
	if err != nil {
		t.Fatalf("ollamaAsk: %v", err)
	}
	if strings.TrimSpace(out) != "Is the nil check needed?" {
		t.Errorf("response = %q", out)
	}
}

// TestOllamaAskErrorStatus turns a non-200 into an error so LoadDeck falls back.
func TestOllamaAskErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	if _, err := ollamaAsk(context.Background(), srv.URL, "m", "p"); err == nil {
		t.Fatal("expected an error on 500, got nil")
	}
}

// TestOllamaAskAPIError surfaces an error field in a 200 body as an error.
func TestOllamaAskAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"error":"model \"nope\" not found"}`)
	}))
	defer srv.Close()

	if _, err := ollamaAsk(context.Background(), srv.URL, "nope", "p"); err == nil {
		t.Fatal("expected an error when the API returns an error field")
	}
}

// TestOllamaAskContextCancel confirms a cancelled context aborts the call.
func TestOllamaAskContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(w, `{"response":"late"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ollamaAsk(ctx, srv.URL, "m", "p"); err == nil {
		t.Fatal("expected a context-deadline error, got nil")
	}
}

// TestBuiltinDuckFallbackLoads confirms the real embedded fallback deck loads,
// so the default (nil fallback) path is genuinely usable.
func TestBuiltinDuckFallbackLoads(t *testing.T) {
	d, err := builtinDuck()
	if err != nil {
		t.Fatalf("builtinDuck: %v", err)
	}
	if d.Name != "duck" || len(d.Cards) == 0 {
		t.Errorf("unexpected fallback deck: %+v", d)
	}
}
