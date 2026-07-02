package srs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseMarkdownQA covers the happy path of Q:/A: parsing, including a
// multi-line answer, a "---" separator between cards, case-insensitive markers,
// and preamble text before the first Q: being ignored.
func TestParseMarkdownQA(t *testing.T) {
	src := `Some intro notes that are not a card.

Q: What is the capital of France?
A: Paris.

q: How many bits in a byte?
a: Eight
bits, usually.

---

Q: Multi-line
question here?
A: And a
multi-line answer.
`
	cards, err := Parse([]byte(src), FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3: %+v", len(cards), cards)
	}
	if cards[0].Title != "What is the capital of France?" || cards[0].Text != "Paris." {
		t.Errorf("card 0 = %+v", cards[0])
	}
	if cards[1].Title != "How many bits in a byte?" || cards[1].Text != "Eight bits, usually." {
		t.Errorf("card 1 = %+v", cards[1])
	}
	if cards[2].Title != "Multi-line question here?" || cards[2].Text != "And a multi-line answer." {
		t.Errorf("card 2 = %+v", cards[2])
	}
}

// TestParseMarkdownSkipsIncomplete drops a trailing Q: with no answer rather
// than emitting a half card.
func TestParseMarkdownSkipsIncomplete(t *testing.T) {
	src := "Q: complete?\nA: yes\n\nQ: dangling question with no answer\n"
	cards, err := Parse([]byte(src), FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1: %+v", len(cards), cards)
	}
	if cards[0].Title != "complete?" {
		t.Errorf("card 0 = %+v", cards[0])
	}
}

// TestParseMarkdownAnswerBeforeQuestion is a structural error: an A: with no
// preceding Q: almost certainly means a malformed or misidentified file.
func TestParseMarkdownAnswerBeforeQuestion(t *testing.T) {
	_, err := Parse([]byte("A: orphan answer\n"), FormatMarkdown)
	if err == nil {
		t.Fatal("expected error for answer before question, got nil")
	}
}

// TestParseAnkiTSV covers the Anki plain-text export path: tab-separated
// front/back, skipped "#" metadata directives, skipped blank and note-less
// lines, extra trailing fields ignored, and light HTML cleanup.
func TestParseAnkiTSV(t *testing.T) {
	src := "#separator:tab\n" +
		"#html:true\n" +
		"Capital of France?\tParis\ttag1 tag2\n" +
		"\n" +
		"2 + 2 = ?\t<b>Four</b>&nbsp;(4)\n" +
		"line with no tab is skipped\n" +
		"\tempty front is skipped\n" +
		"empty back is skipped\t\n"
	cards, err := Parse([]byte(src), FormatAnki)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	if cards[0].Title != "Capital of France?" || cards[0].Text != "Paris" {
		t.Errorf("card 0 = %+v", cards[0])
	}
	// <b> stripped, &nbsp; unescaped to a normal space, whitespace collapsed.
	if cards[1].Title != "2 + 2 = ?" || cards[1].Text != "Four (4)" {
		t.Errorf("card 1 = %+v", cards[1])
	}
}

// TestDetectFormat checks extension-first detection with a content-sniff
// fallback for unknown extensions.
func TestDetectFormat(t *testing.T) {
	cases := []struct {
		name string
		path string
		data string
		want Format
	}{
		{"txt is anki", "cards.txt", "a\tb\n", FormatAnki},
		{"tsv is anki", "cards.tsv", "a\tb\n", FormatAnki},
		{"md is markdown", "cards.md", "Q: x\nA: y\n", FormatMarkdown},
		{"markdown ext", "cards.markdown", "Q: x\nA: y\n", FormatMarkdown},
		{"unknown ext, tab sniffs anki", "cards.dat", "front\tback\n", FormatAnki},
		{"unknown ext, Q: sniffs markdown", "cards.dat", "Q: x\nA: y\n", FormatMarkdown},
		{"unknown ext, plain defaults markdown", "cards.dat", "just words\n", FormatMarkdown},
		{"anki comment not mistaken for tab sniff", "cards.dat", "#separator:tab\nQ: x\nA: y\n", FormatMarkdown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectFormat(tc.path, []byte(tc.data)); got != tc.want {
				t.Errorf("DetectFormat(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestLoadDeckMarkdownFile is the end-to-end file path: write a Markdown file,
// load it, and confirm the deck name/emoji and cards.
func TestLoadDeckMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cards.md")
	if err := os.WriteFile(path, []byte("Q: a?\nA: b\nQ: c?\nA: d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDeck(path)
	if err != nil {
		t.Fatalf("LoadDeck: %v", err)
	}
	if d.Name != DeckName {
		t.Errorf("deck name = %q, want %q", d.Name, DeckName)
	}
	if d.Emoji == "" || d.Description == "" {
		t.Errorf("deck missing flavor: %+v", d)
	}
	if len(d.Cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(d.Cards))
	}
}

// TestLoadDeckErrors covers the misconfiguration cases callers must see:
// empty path, missing file, and a file that parses to zero cards.
func TestLoadDeckErrors(t *testing.T) {
	if _, err := LoadDeck(""); err == nil {
		t.Error("empty path should error")
	}
	if _, err := LoadDeck(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Error("missing file should error")
	}

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("just notes, no Q/A here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDeck(empty); err == nil {
		t.Error("zero-card file should error")
	}
}
