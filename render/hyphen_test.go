package render

import (
	"context"
	"strings"
	"testing"

	"github.com/laix/layx/render/hyphen/en"
	"github.com/laix/layx/render/paint"
)

var _ Hyphenator = en.New()

func hyphenDoc(t testing.TB, hyphenate bool, text string) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, Hyphenate: hyphenate,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	return featureDoc(t, styles, nodes)
}

func hyphenOptions(t testing.TB, hyphenator Hyphenator, width float32) LayoutOptions {
	t.Helper()
	opts := featureOptions(t, width, 600)
	opts.Hyphenator = hyphenator
	return opts
}

func itemTexts(l *Layout) []string {
	var out []string
	for i := range l.chapters[0].items {
		it := &l.chapters[0].items[i]
		if it.kind == itemLine {
			out = append(out, string(it.para.text[it.start:it.end]))
		}
	}
	return out
}

func TestHyphenationSplitsWord(t *testing.T) {
	h := en.New()
	// 90px viewport - 48px margins = 42px. "hyphen" is 65px and "hy-" is 25px,
	// so only the hyphenated split fits on the first line.
	on := featureLayout(t, hyphenDoc(t, true, "hyphen"), hyphenOptions(t, h, 90))
	lines := itemTexts(on)
	if len(lines) < 2 || lines[0] != "hy" || lines[1] != "phen" {
		t.Fatalf("hyphenated lines = %q, want [hy phen]", lines)
	}
	cf := &on.chapters[0]
	first := cf.items[0]
	if first.end != 2 {
		t.Fatalf("first line ends at rune %d, want 2", first.end)
	}

	// The visible hyphen is synthetic: it is drawn as an extra single-glyph
	// run at the first line's end.
	p := &capturePainter{}
	on.Page(0).Draw(p)
	info := &on.pages[0]
	baseline := on.opts.Margins.Top - info.top + first.y + first.baseline
	found := false
	for _, run := range p.runs {
		if run.Y == baseline && len(run.Glyphs) == 1 && run.Glyphs[0].Cluster == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no synthetic hyphen glyph at the first line end: %d runs", len(p.runs))
	}

	off := featureLayout(t, hyphenDoc(t, false, "hyphen"), hyphenOptions(t, h, 90))
	if got := itemTexts(off); len(got) == 2 && got[0] == "hy" && got[1] == "phen" {
		t.Fatalf("disabled hyphenation produced the hyphenated split %q", got)
	}
}

func TestHyphenationOffMatchesBaseline(t *testing.T) {
	text := strings.Repeat("hyphenation typography representation extraordinary ", 40)
	base := featureLayout(t, hyphenDoc(t, false, text), featureOptions(t, 260, 600))
	// A hyphenator is configured but Style.Hyphenate is false: line boxes must
	// be identical to the old (nil-hyphenator) result.
	off := featureLayout(t, hyphenDoc(t, false, text), hyphenOptions(t, en.New(), 260))
	compareLineBoxes(t, "hyphenation disabled", off, base)
}

func TestHyphenationBreakKey(t *testing.T) {
	base := keyTestOptions(t)
	valid, err := base.validate()
	if err != nil {
		t.Fatal(err)
	}
	keyer := newVariantCache(1).newKeyer()
	keys := func(st Style, h Hyphenator, name string) keyHash {
		doc := featureDoc(t, []Style{{}, st}, []Node{
			{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
			{Kind: NodeText, Text: "hyphenation"},
		})
		o := valid
		o.Hyphenator = h
		if name != "" {
			o.Hyphenator = namedHyphenator{name: name}
		}
		return chapterBreakKey(keyer, doc, &doc.chapters[0], o, o.Typography)
	}
	st := Style{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5}
	off := keys(st, nil, "")
	withFlag := keys(Style{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Hyphenate: true}, en.New(), "")
	if off == withFlag {
		t.Fatal("Hyphenate / hyphenator did not change BreakKey")
	}
	other := keys(Style{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Hyphenate: true}, nil, "other-en")
	if other == withFlag {
		t.Fatal("Hyphenator.Name() did not change BreakKey")
	}
}

type namedHyphenator struct{ name string }

func (h namedHyphenator) Breaks([]rune) []int { return nil }
func (h namedHyphenator) Name() string        { return h.name }

// TestHyphenationRelayoutNoShaping proves the dictionary name is part of the
// break key even when the text never needs a hyphen: changing it must re-break
// through Relayout instead of silently reusing the old line boxes.
func TestHyphenationRelayoutNoShaping(t *testing.T) {
	doc := hyphenDoc(t, true, "a simple sentence without long words")
	base := hyphenOptions(t, namedHyphenator{name: "en-a"}, 420)
	e := mustEngine(t, 2)
	l, err := e.Layout(context.Background(), doc, base)
	if err != nil {
		t.Fatal(err)
	}
	next := base
	next.Hyphenator = namedHyphenator{name: "en-b"}
	before := e.shapes.Load()
	other, err := l.Relayout(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if other.breakKeys[0] == l.breakKeys[0] {
		t.Fatal("dictionary name change kept BreakKey")
	}
	if e.shapes.Load() == before {
		t.Fatal("dictionary name change reused cached breaks")
	}
}

// TestHyphenationUnusedPointsDoNotWidenLine checks that potential break points
// inside a word that fits on one line do not leave invisible hyphen width in
// the stored line box.
func TestHyphenationUnusedPointsDoNotWidenLine(t *testing.T) {
	l := featureLayout(t, hyphenDoc(t, true, "hyphenation"), hyphenOptions(t, en.New(), 180))
	if got := itemTexts(l); len(got) != 1 || got[0] != "hyphenation" {
		t.Fatalf("lines = %q, want the whole word", got)
	}
	it := &l.chapters[0].items[0]
	p := &capturePainter{}
	l.Page(0).Draw(p)
	var drawn float32
	info := &l.pages[0]
	baseline := l.opts.Margins.Top - info.top + it.y + it.baseline
	for _, run := range p.runs {
		if run.Y != baseline {
			continue
		}
		for _, g := range run.Glyphs {
			drawn += g.Advance
		}
	}
	if !near(drawn, it.width, 0.01) {
		t.Fatalf("drawn advance = %v, line width = %v; unused hyphenation points leaked width", drawn, it.width)
	}
}

// TestHyphenationNarrowWidthsKeepRuneRanges walks the width range that forces
// the measured-width fallback and checks that hyphenation never loses or
// duplicates text.
func TestHyphenationNarrowWidthsKeepRuneRanges(t *testing.T) {
	const text = "hyphenation typography representation"
	for width := float32(50); width <= 220; width += 5 {
		l := featureLayout(t, hyphenDoc(t, true, text), hyphenOptions(t, en.New(), width))
		cf := &l.chapters[0]
		var (
			next  int32
			got   []rune
			lines int
		)
		for i := range cf.items {
			it := &cf.items[i]
			if it.kind != itemLine {
				continue
			}
			lines++
			if it.start != next {
				t.Fatalf("width %v: line %d starts at %d, want %d", width, lines, it.start, next)
			}
			if it.end < it.start {
				t.Fatalf("width %v: line %d has negative range", width, lines)
			}
			got = append(got, it.para.text[it.start:it.end]...)
			next = it.end
			if it.para.hyphenAt(it.end) && !strings.Contains("hyphenation typography representation", string(it.para.text[it.end:it.end+1])) {
				t.Fatalf("width %v: hyphen at end %d is not inside a word", width, it.end)
			}
		}
		if next != int32(len([]rune(text))) || string(got) != text {
			t.Fatalf("width %v: ranges cover %q, want %q", width, string(got), text)
		}
	}
}
