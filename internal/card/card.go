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
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rwrife/idle-hands/internal/deck"
)

// Picker chooses cards from a deck, avoiding immediate repeats. It is not safe
// for concurrent use; the watch loop drives it from one goroutine.
type Picker struct {
	deck deck.Deck
	rng  *rand.Rand
	last int // index of the last card returned, or -1 if none yet
}

// NewPicker builds a Picker over d. The rng is injectable so tests are
// deterministic; pass nil for a time-seeded default.
func NewPicker(d deck.Deck, rng *rand.Rand) *Picker {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Picker{deck: d, rng: rng, last: -1}
}

// Next returns the next card to show. With two or more cards it never returns
// the same index twice in a row; with a single card it returns that card every
// time (there is nothing else to show).
func (p *Picker) Next() deck.Card {
	n := len(p.deck.Cards)
	switch n {
	case 0:
		return deck.Card{} // defensively empty; decks are validated non-empty
	case 1:
		p.last = 0
		return p.deck.Cards[0]
	}
	i := p.rng.Intn(n)
	if i == p.last {
		// Shift to a neighbor so we never repeat; wraps within [0,n).
		i = (i + 1) % n
	}
	p.last = i
	return p.deck.Cards[i]
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
type Renderer struct {
	w      io.Writer
	deck   deck.Deck
	picker *Picker
	theme  Theme

	// width is the target render width for the card box (content + border).
	width int

	shown    bool // a card is currently on screen for this BUSY window
	lastLine int  // number of lines the rendered card spanned (for erase)
}

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
	return &Renderer{
		w:      w,
		deck:   opts.Deck,
		picker: NewPicker(opts.Deck, opts.Rand),
		theme:  theme,
		width:  width,
	}
}

// OnBusy is called when the detector enters BUSY. It renders exactly one card —
// the first call per window wins; any subsequent OnBusy without an intervening
// OnIdle is a no-op, enforcing the "one card only" rule. idleFor is the quiet
// span the detector reported (shown in the header).
func (r *Renderer) OnBusy(idleFor time.Duration) {
	if r.shown {
		return // already showed this window's one card
	}
	c := r.picker.Next()
	frame := r.render(c, idleFor)
	fmt.Fprint(r.w, "\n"+frame+"\n")
	r.shown = true
	// Count lines so OnIdle can erase precisely. +1 for the leading blank line
	// we emitted, +1 for the trailing newline's blank.
	r.lastLine = strings.Count(frame, "\n") + 1
}

// OnIdle is called when the detector returns to IDLE. It clears the on-screen
// card (best-effort cursor-up + line-erase) and prints the "agent's back" line
// with the reclaimed span. It resets the latch so the next BUSY window shows a
// fresh card.
func (r *Renderer) OnIdle(reclaimed time.Duration) {
	if r.shown {
		fmt.Fprint(r.w, r.clearSeq())
	}
	fmt.Fprintf(r.w, "  %s agent's back — reclaimed %s\n", "👋", reclaimed.Round(time.Second))
	r.shown = false
	r.lastLine = 0
}

// Shown reports whether a card is currently displayed (a card has been rendered
// for the current BUSY window and not yet cleared).
func (r *Renderer) Shown() bool { return r.shown }

// render produces the styled card string (without surrounding blank lines).
//
// Width accounting: r.width is the total box width (border + padding +
// content). The border draws 1 column each side and the theme adds 2 columns
// of horizontal padding each side, so the wrappable content area is
// r.width-6. We wrap the body to exactly that and let the bordered box size
// itself to the content, which keeps the right edge flush and the text from
// wrapping a word early.
func (r *Renderer) render(c deck.Card, idleFor time.Duration) string {
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

	body := r.theme.body.Width(contentWidth).Render(c.Text)

	footer := r.theme.footer.Render("one card · idle-hands")

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
