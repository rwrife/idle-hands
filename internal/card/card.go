// Package card turns a deck into something on screen during a BUSY window: it
// picks exactly one card (never the same one twice in a row), renders it with
// lipgloss into a tidy bordered panel, and — on IDLE — clears that panel and
// prints the "👋 agent's back" line.
//
// The deliberate contract, straight from M4's definition of done, is *one card
// per BUSY window*. internal/card owns the "have we already shown a card for
// this window?" latch so the watch loop can stay dumb: it just calls OnBusy /
// OnIdle and the Renderer does the right thing (including ignoring a second
// OnBusy without an intervening OnIdle, which can't normally happen but keeps
// the rule airtight).
package card

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rwrife/idle-hands/internal/deck"
)

// Picker chooses cards from a deck, avoiding immediate repeats and — when a
// spacing window is configured — deprioritizing any card shown in the last few
// picks. It is not safe for concurrent use; the watch loop drives it from one
// goroutine.
//
// Spacing (issue #7): the default behavior is the M4 rule — never the same card
// twice in a row. A Picker built with a larger history window additionally
// avoids every card in that recent window when it can, so a flashcard deck
// doesn't resurface a card you just saw two waits ago. When the window is as
// large as (or larger than) the deck, avoidance relaxes gracefully to "just not
// the immediately previous one" so the picker can never wedge itself.
type Picker struct {
	deck deck.Deck
	rng  *rand.Rand
	last int // index of the last card returned, or -1 if none yet

	// recent is a ring buffer of the most recently returned indices (most
	// recent last). Its capacity is the spacing window; len 0 disables spacing
	// beyond the immediate-repeat guard.
	recent []int
	window int
}

// NewPicker builds a Picker over d that only avoids immediate repeats. The rng
// is injectable so tests are deterministic; pass nil for a time-seeded default.
func NewPicker(d deck.Deck, rng *rand.Rand) *Picker {
	return NewSpacedPicker(d, rng, 0)
}

// NewSpacedPicker builds a Picker that additionally deprioritizes the last
// `window` distinct cards it returned (in addition to never immediately
// repeating). A window <= 1 behaves exactly like NewPicker. The effective
// window is capped at len(cards)-1 so at least one card is always eligible.
func NewSpacedPicker(d deck.Deck, rng *rand.Rand, window int) *Picker {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if window < 0 {
		window = 0
	}
	// Cap so we never exclude every card; keep at least one eligible.
	if n := len(d.Cards); n > 0 && window > n-1 {
		window = n - 1
	}
	return &Picker{deck: d, rng: rng, last: -1, window: window}
}

// Next returns the next card to show. With two or more cards it never returns
// the same index twice in a row; with a single card it returns that card every
// time (there is nothing else to show). When a spacing window is set it also
// avoids the recent window of cards when at least one card outside it exists.
func (p *Picker) Next() deck.Card {
	n := len(p.deck.Cards)
	switch n {
	case 0:
		return deck.Card{} // defensively empty; decks are validated non-empty
	case 1:
		p.last = 0
		p.remember(0)
		return p.deck.Cards[0]
	}
	i := p.pick(n)
	p.last = i
	p.remember(i)
	return p.deck.Cards[i]
}

// pick chooses an index in [0,n) that isn't the immediately previous one and,
// when possible, isn't in the recent spacing window. It draws at random and
// rejects excluded indices for a bounded number of tries, then falls back to a
// deterministic scan so it always terminates even in adversarial rng runs.
func (p *Picker) pick(n int) int {
	excluded := p.excludedSet(n)
	// Try random draws first to preserve the uniform-ish feel of the M4 picker.
	for tries := 0; tries < 2*n; tries++ {
		i := p.rng.Intn(n)
		if !excluded[i] {
			return i
		}
	}
	// Deterministic fallback: first non-excluded index after a random offset.
	off := p.rng.Intn(n)
	for k := 0; k < n; k++ {
		i := (off + k) % n
		if !excluded[i] {
			return i
		}
	}
	// Everything excluded (shouldn't happen given the window cap); just avoid
	// the immediate repeat.
	i := p.rng.Intn(n)
	if i == p.last {
		i = (i + 1) % n
	}
	return i
}

// excludedSet marks indices the next pick should avoid: always the immediately
// previous card, plus every card in the recent spacing window. It guarantees at
// least one index stays eligible by never marking more than n-1 of them.
func (p *Picker) excludedSet(n int) []bool {
	ex := make([]bool, n)
	marked := 0
	mark := func(i int) {
		if i >= 0 && i < n && !ex[i] && marked < n-1 {
			ex[i] = true
			marked++
		}
	}
	if p.last >= 0 {
		mark(p.last)
	}
	// Walk the recent window most-recent-first so the freshest cards are the
	// ones kept out if we run up against the n-1 cap.
	for k := len(p.recent) - 1; k >= 0; k-- {
		mark(p.recent[k])
	}
	return ex
}

// remember records index i as the most recent pick, bounded to the spacing
// window. With a zero window it keeps nothing (the immediate-repeat guard via
// p.last is enough).
func (p *Picker) remember(i int) {
	if p.window <= 0 {
		return
	}
	p.recent = append(p.recent, i)
	if len(p.recent) > p.window {
		p.recent = p.recent[len(p.recent)-p.window:]
	}
}

// Theme controls card styling. The zero value is unusable; use DefaultTheme.
type Theme struct {
	border lipgloss.Style
	title  lipgloss.Style
	body   lipgloss.Style
	footer lipgloss.Style
	header lipgloss.Style
}

// DefaultTheme returns the standard idle-hands card styling: a rounded, muted
// border with a soft accent on the title. Colors are ANSI-256 so they degrade
// gracefully on basic terminals.
func DefaultTheme() Theme {
	const (
		accent = lipgloss.Color("212") // soft magenta/pink
		muted  = lipgloss.Color("245") // grey
		body   = lipgloss.Color("252") // near-white
	)
	return Theme{
		border: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).
			Padding(0, 2),
		header: lipgloss.NewStyle().Foreground(muted).Italic(true),
		title:  lipgloss.NewStyle().Foreground(accent).Bold(true),
		body:   lipgloss.NewStyle().Foreground(body),
		footer: lipgloss.NewStyle().Foreground(muted).Faint(true),
	}
}

// Renderer holds the picker, theme, and the per-window latch, and writes card
// frames to a single io.Writer (typically stderr, so the child's stdout stays
// clean). It tracks how many terminal lines the last card occupied so it can
// erase exactly those lines on clear.
//
// Reveal mode (issue #7): when a reveal delay is configured the card shows only
// its front (title) at first and, a beat later, redraws in place with the body
// ("answer") revealed. The reveal is driven by a timer on its own goroutine and
// never reads stdin, so it can't block or steal input from the wrapped agent —
// keystrokes still flow straight to the child. If the agent comes back before
// the timer fires, OnIdle cancels the pending reveal so a stale answer never
// pops up after the card was cleared.
type Renderer struct {
	w      io.Writer
	deck   deck.Deck
	picker *Picker
	theme  Theme

	// width is the target render width for the card box (content + border).
	width int

	// reveal, when > 0, enables the front-then-answer reveal after this delay.
	reveal time.Duration
	// newTimer builds the reveal timer; injectable so tests fire it
	// deterministically instead of sleeping. nil selects time.AfterFunc.
	newTimer func(time.Duration, func()) revealTimer

	// async, when non-nil, makes OnBusy show a placeholder card immediately and
	// then run async in a goroutine (bounded by a context cancelled on OnIdle)
	// to produce the real card, redrawing it in place. It is how the "hook" deck
	// runs a command during the wait and shows its result. nil disables async
	// behavior (the static and reveal decks). See AsyncCard.
	async AsyncCard

	mu       sync.Mutex // guards the fields below (timer/goroutine run concurrently)
	shown    bool       // a card is currently on screen for this BUSY window
	lastLine int        // number of lines the rendered card spanned (for erase)
	cur      deck.Card  // the card currently displayed (for the reveal redraw)
	curIdle  time.Duration
	revealed bool               // whether cur's answer is already showing
	timer    revealTimer        // pending reveal timer, if any
	cancel   context.CancelFunc // cancels the in-flight async producer, if any
	gen      uint64             // BUSY-window generation, to ignore stale async results
}

// revealTimer is the tiny slice of *time.Timer the renderer needs, so tests can
// substitute a controllable fake.
type revealTimer interface {
	Stop() bool
}

// AsyncCard produces the card to show for a BUSY window, doing whatever work
// the deck needs (e.g. running a hook command) bounded by ctx. ctx is cancelled
// when the agent returns (OnIdle), so a slow producer stops promptly. The
// second return value reports whether a card should be shown at all: false
// means "produced nothing" (e.g. the window was cancelled before the hook
// finished), and the placeholder is simply cleared with no replacement.
type AsyncCard func(ctx context.Context) (card deck.Card, show bool)

// Options configure a Renderer.
type Options struct {
	// Deck is the deck to draw cards from. Required.
	Deck deck.Deck
	// Theme styles the card. Zero value selects DefaultTheme.
	Theme *Theme
	// Width is the card box width in columns. <= 0 selects a sensible default.
	Width int
	// Rand is the source for card selection. nil selects a time-seeded default.
	Rand *rand.Rand
	// Spacing is the number of recently-shown cards to deprioritize (in
	// addition to never immediately repeating). 0 keeps the plain M4 behavior;
	// the flashcard deck sets it so a card isn't re-shown for a few waits.
	Spacing int
	// Reveal, when > 0, shows the card front first and reveals the answer after
	// this delay (used by the flashcard deck). The reveal never blocks or reads
	// stdin. 0 renders title and body together as the other decks do.
	Reveal time.Duration
	// newTimer overrides the reveal timer constructor for tests. nil selects
	// time.AfterFunc. Unexported so it isn't part of the public surface.
	newTimer func(time.Duration, func()) revealTimer
	// Async, when non-nil, drives the "hook" deck: OnBusy shows Deck's picked
	// card as a placeholder, then runs Async in a goroutine (bounded by a
	// context cancelled on OnIdle) and redraws the returned card in place. When
	// set, Deck should be a one-card placeholder deck (e.g. "…running hook").
	Async AsyncCard
}

// defaultWidth is the card box width when none is supplied. Comfortable in an
// 80-column terminal with room for an agent TUI beside the notice.
const defaultWidth = 60

// NewRenderer builds a Renderer writing to w.
func NewRenderer(w io.Writer, opts Options) *Renderer {
	theme := DefaultTheme()
	if opts.Theme != nil {
		theme = *opts.Theme
	}
	width := opts.Width
	if width <= 0 {
		width = defaultWidth
	}
	newTimer := opts.newTimer
	if newTimer == nil {
		newTimer = func(d time.Duration, fn func()) revealTimer {
			return time.AfterFunc(d, fn)
		}
	}
	reveal := opts.Reveal
	if reveal < 0 {
		reveal = 0
	}
	return &Renderer{
		w:        w,
		deck:     opts.Deck,
		picker:   NewSpacedPicker(opts.Deck, opts.Rand, opts.Spacing),
		theme:    theme,
		width:    width,
		reveal:   reveal,
		newTimer: newTimer,
		async:    opts.Async,
	}
}

// OnBusy is called when the detector enters BUSY. It renders exactly one card —
// the first call per window wins; any subsequent OnBusy without an intervening
// OnIdle is a no-op, enforcing the "one card only" rule. idleFor is the quiet
// span the detector reported (shown in the header).
//
// In reveal mode the first frame shows only the front (title); a timer then
// redraws the same card in place with the answer revealed. The timer runs on
// its own goroutine and never reads input, so the wrapped agent is never
// blocked. Without reveal (reveal <= 0, or a card with an empty body) the whole
// card is drawn at once, exactly as the non-flashcard decks do.
func (r *Renderer) OnBusy(idleFor time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shown {
		return // already showed this window's one card
	}
	c := r.picker.Next()
	r.cur = c
	r.curIdle = idleFor
	// Reveal only makes sense when we both have a delay and a body to hide.
	r.revealed = !(r.reveal > 0 && strings.TrimSpace(c.Text) != "")
	r.draw()
	r.shown = true
	r.gen++

	if r.async != nil {
		// Hook deck: the placeholder is on screen; run the producer bounded by a
		// context we cancel on OnIdle, then redraw its result in place.
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		gen := r.gen
		go r.runAsync(ctx, gen)
		return
	}

	if !r.revealed {
		// Schedule the non-blocking answer reveal.
		r.timer = r.newTimer(r.reveal, r.doReveal)
	}
}

// runAsync invokes the async producer (outside the lock, since it may block on
// a subprocess) and, if this BUSY window is still the current one, redraws the
// produced card in place. A card produced for a window that already ended
// (agent came back, gen advanced) is discarded so a late result never scribbles
// over a cleared screen or the next window's card.
func (r *Renderer) runAsync(ctx context.Context, gen uint64) {
	c, show := r.async(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.shown || r.gen != gen {
		return // window ended or moved on; drop the stale result
	}
	fmt.Fprint(r.w, r.clearSeq()) // erase the placeholder frame
	if !show {
		// Producer had nothing to show (e.g. cancelled). Leave the placeholder
		// cleared; OnIdle will print the "agent's back" line.
		r.lastLine = 0
		return
	}
	r.cur = c
	r.revealed = true
	r.draw()
}

// doReveal is the timer callback that flips the current card to its answered
// state and redraws it in place. It is a no-op if the window was already
// cleared (agent came back) or the card is already revealed, so a late timer
// can never scribble over a cleared screen.
func (r *Renderer) doReveal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.shown || r.revealed {
		return
	}
	fmt.Fprint(r.w, r.clearSeq()) // erase the front-only frame
	r.revealed = true
	r.draw()
}

// draw renders the current card (front-only or revealed per r.revealed), writes
// it framed by blank lines, and records the line span for a later erase. It
// assumes r.mu is held.
func (r *Renderer) draw() {
	frame := r.render(r.cur, r.curIdle, r.revealed)
	fmt.Fprint(r.w, "\n"+frame+"\n")
	// Count lines so a clear can erase precisely. +1 for the leading blank line
	// we emitted, +1 for the trailing newline's blank.
	r.lastLine = strings.Count(frame, "\n") + 1
}

// OnIdle is called when the detector returns to IDLE. It cancels any pending
// reveal, clears the on-screen card (best-effort cursor-up + line-erase) and
// prints the "agent's back" line with the reclaimed span. It resets the latch
// so the next BUSY window shows a fresh card.
func (r *Renderer) OnIdle(reclaimed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop() // don't let a pending reveal fire after we clear
		r.timer = nil
	}
	if r.cancel != nil {
		r.cancel() // stop any in-flight async producer (hook command)
		r.cancel = nil
	}
	r.gen++ // invalidate any async result still in flight for the old window
	if r.shown {
		fmt.Fprint(r.w, r.clearSeq())
	}
	fmt.Fprintf(r.w, "  %s agent's back — reclaimed %s\n", "👋", reclaimed.Round(time.Second))
	r.shown = false
	r.revealed = false
	r.lastLine = 0
}

// Shown reports whether a card is currently displayed (a card has been rendered
// for the current BUSY window and not yet cleared).
func (r *Renderer) Shown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shown
}

// render produces the styled card string (without surrounding blank lines).
//
// When revealed is false (reveal mode, answer not yet shown) the body is
// replaced by a muted "…answer in a moment" line so the card is a genuine
// question prompt; when true (or in the non-reveal decks) the real body is
// shown. The footer likewise reflects whether this is a flashcard.
//
// Width accounting: r.width is the total box width (border + padding +
// content). The border draws 1 column each side and the theme adds 2 columns
// of horizontal padding each side, so the wrappable content area is
// r.width-6. We wrap the body to exactly that and let the bordered box size
// itself to the content, which keeps the right edge flush and the text from
// wrapping a word early.
func (r *Renderer) render(c deck.Card, idleFor time.Duration, revealed bool) string {
	contentWidth := r.width - 6 // 2 border cols + 4 padding cols
	if contentWidth < 12 {
		contentWidth = 12
	}

	header := r.theme.header.Render(fmt.Sprintf("…agent is thinking (%s)", idleFor.Round(time.Second)))

	emoji := r.deck.Emoji
	if emoji != "" {
		emoji += "  "
	}
	title := r.theme.title.Width(contentWidth).Render(emoji + c.Title)

	// In reveal mode the body is withheld until the timer fires; show a gentle
	// placeholder so the card reads as a question, not a truncated answer.
	bodyText := c.Text
	revealMode := r.reveal > 0 && strings.TrimSpace(c.Text) != ""
	if revealMode && !revealed {
		bodyText = fmt.Sprintf("…answer in %s (no keypress needed)", r.reveal.Round(time.Second))
	}
	body := r.theme.body.Width(contentWidth).Render(bodyText)

	footerText := "one card · idle-hands"
	if revealMode {
		if revealed {
			footerText = "flashcard · answer · idle-hands"
		} else {
			footerText = "flashcard · recall it · idle-hands"
		}
	}
	footer := r.theme.footer.Render(footerText)

	content := strings.Join([]string{header, "", title, body, "", footer}, "\n")
	// Fix the box to the full width so every card is the same size regardless
	// of how short the body is.
	return r.theme.border.Width(contentWidth).Render(content)
}

// clearSeq returns the terminal escape sequence that moves the cursor up over
// the previously rendered card and erases each line, leaving the cursor where
// the card began. It is best-effort: on terminals/log files that don't honor
// ANSI it simply prints a couple of blank lines instead of corrupting state.
func (r *Renderer) clearSeq() string {
	if r.lastLine <= 0 {
		return ""
	}
	var b strings.Builder
	// Move up over each rendered line and clear it.
	for i := 0; i < r.lastLine; i++ {
		b.WriteString("\033[1A") // cursor up one line
		b.WriteString("\033[2K") // erase entire line
	}
	b.WriteString("\r") // return to column 0
	return b.String()
}
