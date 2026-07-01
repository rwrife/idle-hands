// Package srs turns a user's own flashcards into an idle-hands deck, so a BUSY
// window can hand you one card to *learn* instead of one card to stretch. It is
// the loader behind the built-in-feeling "srs" deck: point config at a card
// file (Markdown Q/A blocks or an Anki text export) and each idle window shows
// one flashcard — the question first, the answer revealed a beat later.
//
// Scope, deliberately small (issue #7): this package only parses a card file
// into the existing deck.Card shape (Title = the front/question, Text = the
// back/answer) and packages it as a deck.Deck named "srs". The spacing (don't
// re-show a card you just saw) and the reveal interaction live in internal/card
// and the watch loop, reusing the machinery the other decks already share. That
// keeps the flashcard feature a thin adapter over the deck model rather than a
// parallel universe.
//
// Two input formats are supported, chosen by file extension and confirmed by a
// light content sniff so a mislabeled file still loads:
//
//   - Markdown Q/A (.md/.markdown/.text, or sniffed): blocks of "Q: …" then
//     "A: …" lines. A question or answer may span multiple lines until the next
//     Q:/A: marker or a blank line. Cards may also be separated by a "---" rule.
//   - Anki text export (.txt, or sniffed as TSV): one card per line,
//     front<TAB>back, matching Anki's default "Notes in Plain Text" export.
//     Lines beginning with "#" are Anki metadata (e.g. "#separator:tab") and
//     are skipped, as are blank lines.
//
// Everything here is pure parsing with a single file read and no other side
// effects, and the format detection is exported and injectable, so the package
// is fully testable from strings without touching a real card collection.
package srs

import (
	"bufio"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/rwrife/idle-hands/internal/deck"
)

// DeckName is the reserved name of the flashcard deck. Selecting it in config
// (deck = "srs") makes watch load the user's card source instead of a built-in
// or user TOML deck of cards.
const DeckName = "srs"

// deckEmoji and deckDescription flavor the "srs" deck in `deck` list/preview
// output so it reads like the shipped decks even though its cards come from the
// user's file.
const (
	deckEmoji       = "🧠"
	deckDescription = "Your flashcards, one per wait (spaced)."
)

// Format identifies how a card source file is encoded.
type Format int

const (
	// FormatMarkdown is Q:/A: (or ---separated front/back) Markdown.
	FormatMarkdown Format = iota
	// FormatAnki is a tab-separated Anki text export (front<TAB>back per line).
	FormatAnki
)

// String renders a Format for messages.
func (f Format) String() string {
	switch f {
	case FormatAnki:
		return "anki"
	default:
		return "markdown"
	}
}

// LoadDeck reads the card source at path and returns it as a deck named "srs".
// The format is detected from the extension and a content sniff. A missing path
// is an error (unlike an optional user-deck dir, an SRS source that config
// explicitly points at but that isn't there is a real misconfiguration the user
// should see). A source that parses to zero cards is also an error, since an
// empty flashcard deck can show nothing.
func LoadDeck(path string) (deck.Deck, error) {
	if strings.TrimSpace(path) == "" {
		return deck.Deck{}, fmt.Errorf("no card source configured (set srs_source in ~/.idle-hands/config.toml)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return deck.Deck{}, fmt.Errorf("read card source %q: %w", path, err)
	}
	format := DetectFormat(path, data)
	cards, err := Parse(data, format)
	if err != nil {
		return deck.Deck{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(cards) == 0 {
		return deck.Deck{}, fmt.Errorf("%s: no flashcards found (expected %s content)", path, format)
	}
	return deck.Deck{
		Name:        DeckName,
		Description: deckDescription,
		Emoji:       deckEmoji,
		Cards:       cards,
	}, nil
}

// DetectFormat picks a parser for a card source. The file extension is the
// primary signal (.txt → Anki TSV; .md/.markdown/.text/.markdown → Markdown);
// when the extension is unknown or ambiguous the content is sniffed: a tab on a
// non-comment line strongly implies an Anki export, while a leading "Q:" marker
// implies Markdown. The default is Markdown, the more forgiving format.
func DetectFormat(path string, data []byte) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".tsv":
		return FormatAnki
	case ".md", ".markdown", ".mdown", ".mkd", ".text":
		return FormatMarkdown
	}
	// Unknown extension: sniff the content.
	return sniffFormat(data)
}

// sniffFormat guesses a format from bytes alone. A tab character on a line that
// isn't an Anki comment says "Anki TSV"; an explicit Q:/A: marker says
// "Markdown". Otherwise default to Markdown.
func sniffFormat(data []byte) Format {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return FormatAnki
		}
		if hasQAPrefix(trimmed) {
			return FormatMarkdown
		}
	}
	return FormatMarkdown
}

// Parse decodes card bytes in the given format into deck.Cards. It never
// returns an error for a well-formed-but-empty source (LoadDeck treats "zero
// cards" as the error); it only errors on structurally broken input (e.g. an
// answer with no preceding question).
func Parse(data []byte, format Format) ([]deck.Card, error) {
	switch format {
	case FormatAnki:
		return parseAnki(data)
	default:
		return parseMarkdown(data)
	}
}

// parseAnki reads Anki's default plain-text export: one note per line, fields
// separated by a tab, the first two fields being front and back. Extra fields
// (tags, etc.) are ignored. Comment lines ("#separator:tab", "#html:true") and
// blank lines are skipped. A non-comment line with no tab is skipped with the
// rest of the card intact rather than failing the whole file, because a stray
// note-less line shouldn't nuke a user's whole deck; but a line whose front or
// back is empty after trimming is skipped too (nothing to show).
//
// Anki exports may HTML-escape field content and embed simple markup; we
// unescape entities and strip a few common inline tags so a card reads as plain
// text in the terminal. This is intentionally light: full HTML rendering is out
// of scope.
func parseAnki(data []byte) ([]deck.Card, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var cards []deck.Card
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue // blank or Anki metadata directive
		}
		if !strings.Contains(line, "\t") {
			continue // not a TSV note row; ignore rather than fail the file
		}
		fields := strings.Split(line, "\t")
		front := cleanField(fields[0])
		back := cleanField(fields[1])
		if front == "" || back == "" {
			continue
		}
		cards = append(cards, deck.Card{Title: front, Text: back})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan anki export: %w", err)
	}
	return cards, nil
}

// parseMarkdown reads Q:/A: flashcards. The grammar is intentionally small and
// unsurprising:
//
//	Q: <question, possibly continued on following lines>
//	A: <answer, possibly continued on following lines>
//
// A new "Q:" marker (or a "---" horizontal rule, or end of input) closes the
// current card. Continuation lines are joined with spaces. A card needs both a
// question and an answer to be emitted; a dangling "Q:" with no "A:" is skipped
// (incomplete), but an "A:" that appears before any "Q:" is a structural error
// worth surfacing, since it almost certainly means the file is malformed or in
// the wrong format.
func parseMarkdown(data []byte) ([]deck.Card, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var (
		cards   []deck.Card
		front   []string
		back    []string
		inBack  bool // currently accumulating the answer
		started bool // have we seen a Q: for the current card
	)

	flush := func() {
		q := strings.TrimSpace(strings.Join(front, " "))
		a := strings.TrimSpace(strings.Join(back, " "))
		if q != "" && a != "" {
			cards = append(cards, deck.Card{Title: q, Text: a})
		}
		front = front[:0]
		back = back[:0]
		inBack = false
		started = false
	}

	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)

		switch {
		case isRule(line):
			// Horizontal rule separates cards.
			flush()
			continue
		case hasPrefixFold(line, "Q:"):
			// A new question starts a new card; flush any complete previous one.
			if started {
				flush()
			}
			started = true
			inBack = false
			front = append(front, strings.TrimSpace(line[len("Q:"):]))
			continue
		case hasPrefixFold(line, "A:"):
			if !started {
				return nil, fmt.Errorf("answer ('A:') before any question ('Q:'); is this a Markdown Q/A file?")
			}
			inBack = true
			back = append(back, strings.TrimSpace(line[len("A:"):]))
			continue
		case line == "":
			// Blank line: a soft separator inside a card is allowed (keeps
			// multi-line answers readable) but a blank line between a finished
			// card and the next Q: is fine too — flushing on the next Q:
			// handles that. So a blank line simply doesn't append content.
			continue
		default:
			// Continuation of whichever side we're currently on.
			if !started {
				// Free text before the first Q: is treated as preamble/notes
				// and ignored rather than misread as a card.
				continue
			}
			if inBack {
				back = append(back, line)
			} else {
				front = append(front, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan markdown cards: %w", err)
	}
	flush() // emit the final card if complete
	return cards, nil
}

// hasQAPrefix reports whether a trimmed line opens a Q: or A: marker (used by
// the sniffer).
func hasQAPrefix(trimmed string) bool {
	return hasPrefixFold(trimmed, "Q:") || hasPrefixFold(trimmed, "A:")
}

// hasPrefixFold is strings.HasPrefix but case-insensitive on the ASCII prefix,
// so "q:" and "Q:" are both accepted.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

// isRule reports whether a trimmed line is a Markdown horizontal rule ("---",
// "***", or "___", three or more), used to separate cards.
func isRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, ch := range []byte{'-', '*', '_'} {
		if strings.Count(line, string(ch)) == len(line) {
			return true
		}
	}
	return false
}

// cleanField normalizes a single Anki field into terminal-friendly plain text:
// unescape HTML entities, drop a few common inline tags, collapse whitespace,
// and trim. It is deliberately shallow — enough to make typical exported cards
// legible, not a real HTML renderer.
func cleanField(s string) string {
	s = html.UnescapeString(s)
	s = stripInlineTags(s)
	s = strings.ReplaceAll(s, "\r", " ")
	// Anki sometimes uses <br> for line breaks inside a field; after tag
	// stripping those become spaces via whitespace collapse below.
	return strings.Join(strings.Fields(s), " ")
}

// stripInlineTags removes a minimal set of HTML-ish tags an Anki export may
// carry (<br>, <b>, <i>, <div>, <span>, and their closers) without pulling in
// an HTML parser. Unknown tags are left as-is so nothing surprising is deleted;
// this only targets the handful Anki emits by default.
func stripInlineTags(s string) string {
	replacer := strings.NewReplacer(
		"<br>", " ", "<br/>", " ", "<br />", " ",
		"<b>", "", "</b>", "",
		"<i>", "", "</i>", "",
		"<u>", "", "</u>", "",
		"<div>", " ", "</div>", " ",
		"<span>", "", "</span>", "",
	)
	return replacer.Replace(s)
}
