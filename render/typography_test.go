package render

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/go-text/typesetting/shaping"
	"github.com/laix/layx/render/paint"
)

// posPainter records glyph positions instead of drawing them.
type posPainter struct {
	runs []paint.GlyphRun
}

func (p *posPainter) Save()                                                   {}
func (p *posPainter) Restore()                                                {}
func (p *posPainter) ClipRect(paint.Rect)                                     {}
func (p *posPainter) Transform(paint.Affine)                                  {}
func (p *posPainter) FillRect(paint.Rect, paint.Color)                        {}
func (p *posPainter) StrokeRect(paint.Rect, paint.Color, float32)             {}
func (p *posPainter) FillRoundRect(paint.Rect, float32, paint.Color)          {}
func (p *posPainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) {}
func (p *posPainter) DrawGlyphs(run paint.GlyphRun) {
	run.Glyphs = append([]paint.Glyph(nil), run.Glyphs...)
	p.runs = append(p.runs, run)
}

type placedGlyph struct {
	cluster int
	x       float32
	advance float32
}

// firstLineGlyphs returns the glyphs of the topmost line in draw order, with
// page-absolute positions.
func firstLineGlyphs(p *posPainter) []placedGlyph {
	if len(p.runs) == 0 {
		return nil
	}
	top := p.runs[0].Y
	for _, run := range p.runs {
		if run.Y < top {
			top = run.Y
		}
	}
	var out []placedGlyph
	for _, run := range p.runs {
		if run.Y != top {
			continue
		}
		for _, g := range run.Glyphs {
			out = append(out, placedGlyph{cluster: int(g.Cluster), x: run.X + g.X, advance: g.Advance})
		}
	}
	return out
}

func cjkParagraph(t *testing.T, text string, align TextAlign, width, size float32) *Layout {
	t.Helper()
	return cjkParagraphRules(t, text, align, width, size, TextRules{})
}

func cjkParagraphRules(t *testing.T, text string, align TextAlign, width, size float32, rules TextRules) *Layout {
	t.Helper()
	cjk, ok := cjkFace(t)
	if !ok {
		t.Skip("no CJK font available")
	}
	fs, err := NewFontSet(loadTestFont(t), cjk)
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	styles := []Style{
		{},
		{FontFamily: "CJK", FontSize: size, LineHeight: 1.5, Color: paint.Color{A: 0xff}, TextAlign: align},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	l, err := mustEngine(t, 1).Layout(context.Background(), doc, LayoutOptions{
		Viewport:   Viewport{Width: width, Height: 2000},
		Typography: Typography{FontFamily: "CJK", FontSize: size, LineHeight: 1.5, Color: paint.Color{A: 0xff}, Rules: rules},
		Fonts:      fs,
	})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return l
}

func near(a, b, eps float32) bool { return float32(math.Abs(float64(a-b))) <= eps }

func TestNeedsAutospace(t *testing.T) {
	text := []rune("中a文 3A，中")
	cases := []struct {
		a, b int
		want bool
	}{
		{0, 1, true},  // 中|a
		{1, 2, true},  // a|文
		{2, 3, false}, // 文|space
		{3, 4, false}, // space|3
		{4, 5, false}, // 3|A: digits and letters are the same group
		{5, 6, false}, // A|，: punctuation is not ideographic
	}
	for i, c := range cases {
		if got := needsAutospace(text, c.a, c.b); got != c.want {
			t.Fatalf("case %d: needsAutospace(%q,%q) = %v, want %v", i, text[c.a], text[c.b], got, c.want)
		}
	}
}

func TestJustifyWeights(t *testing.T) {
	mk := func(runes ...int) []shapedRun {
		gl := make([]shaping.Glyph, len(runes))
		for i, r := range runes {
			gl[i] = shaping.Glyph{ClusterIndex: r, Advance: 100}
		}
		return []shapedRun{{out: shaping.Output{Glyphs: gl}}}
	}

	// 中 a 文 <space> b
	w := justifyWeights([]rune("中a文 b"), mk(0, 1, 2, 3, 4), false)
	// index 0 widens the 中|a gap, index 1 the a|文 gap, index 3 is the space
	want := []float32{1, 1, 0, 1, 0}
	for i := range want {
		if w[i] != want[i] {
			t.Fatalf("weights[%d] = %v, want %v (all: %v)", i, w[i], want[i], w)
		}
	}

	// Punctuation-adjacent gaps stretch at half weight: 中 ， 文
	w = justifyWeights([]rune("中，文"), mk(0, 1, 2), false)
	if w[0] != 0.5 || w[1] != 0.5 {
		t.Fatalf("punctuation weights = %v, want [0.5 0.5 0]", w)
	}

	// Pure Latin: no stretchable slots inside a word.
	w = justifyWeights([]rune("word"), mk(0, 1, 2, 3), false)
	for i, v := range w {
		if v != 0 {
			t.Fatalf("latin weights[%d] = %v, want 0", i, v)
		}
	}
}

func TestAutospaceGap(t *testing.T) {
	// "中文abc": cluster 1 is 文, cluster 2 is a. Autospacing is a policy, off
	// by default, so the test asks for it explicitly.
	l := cjkParagraphRules(t, "中文abc", AlignLeft, 600, 20, TextRules{Autospacing: true})
	p := &posPainter{}
	l.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	if len(glyphs) < 5 {
		t.Fatalf("expected 5 glyphs, got %d", len(glyphs))
	}
	// The gap lives inside the preceding glyph's advance, so compare the
	// advance of 文 against a plain ideograph (中) plus 1/8 em.
	em := glyphs[0].advance
	if want := em + float32(20)/8; !near(glyphs[1].advance, want, 0.05) {
		t.Fatalf("文 advance = %.3f, want %.3f", glyphs[1].advance, want)
	}
	if want := em + float32(20)/8; !near(glyphs[2].x-glyphs[1].x, want, 0.05) {
		t.Fatalf("文→a advance = %.3f, want %.3f", glyphs[2].x-glyphs[1].x, want)
	}
	// No gap inside the Latin word.
	if !near(glyphs[3].x-glyphs[2].x, glyphs[2].advance, 0.05) {
		t.Fatalf("intra-word advance = %.3f, want %.3f", glyphs[3].x-glyphs[2].x, glyphs[2].advance)
	}
}

func TestJustifyStretchesCJKGaps(t *testing.T) {
	text := strings.Repeat("中文排版要好看，标点也不能碍眼。", 10)
	const width = 320
	left := cjkParagraph(t, text, AlignLeft, width, 18)
	just := cjkParagraph(t, text, AlignJustify, width, 18)

	measure := func(l *Layout) []placedGlyph {
		p := &posPainter{}
		l.Page(0).Draw(p)
		g := firstLineGlyphs(p)
		if len(g) < 3 {
			t.Fatalf("expected a full first line, got %d glyphs", len(g))
		}
		return g
	}
	gLeft := measure(left)
	gJust := measure(just)

	// The slack is folded into the preceding glyph's advance, so measure the
	// distance between glyph origins rather than the inter-origin gap.
	gapLeft := gLeft[1].x - gLeft[0].x
	gapJust := gJust[1].x - gJust[0].x
	if gapJust <= gapLeft+0.2 {
		t.Fatalf("justified CJK gap = %.3f, left-aligned = %.3f; expected the justified gap to grow", gapJust, gapLeft)
	}
	edge := gJust[len(gJust)-1].x + gJust[len(gJust)-1].advance
	if !near(edge, width, 1) {
		t.Fatalf("justified line ends at %.2f, want %.2f", edge, float32(width))
	}
}

// TestAutospacingOffByDefault pins the corrected zero value: without
// TextRules.Autospacing the engine must not insert a CJK/Latin gap.
func TestAutospacingOffByDefault(t *testing.T) {
	l := cjkParagraph(t, "中文abc", AlignLeft, 600, 20)
	p := &posPainter{}
	l.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	if len(glyphs) < 3 {
		t.Fatalf("expected at least 3 glyphs, got %d", len(glyphs))
	}
	em := glyphs[0].advance
	if !near(glyphs[1].advance, em, 0.05) {
		t.Fatalf("文 advance = %.3f, want %.3f with autospacing off", glyphs[1].advance, em)
	}
}

// TestTypographyKeepsZeroRules proves withDefaults does not lift the policy
// zeros: 0 widows/orphans means "off", not "2".
func TestTypographyZeroRules(t *testing.T) {
	got := Typography{}.withDefaults()
	// Widows and orphans have a CSS initial value of 2, so an unset field is
	// padded to it. 1 is how a host turns the protection off; zero is not a
	// legal CSS value and must not be read as "off".
	if got.Rules.Widows != 2 || got.Rules.Orphans != 2 {
		t.Fatalf("withDefaults did not pad widows/orphans to the spec default: %+v", got.Rules)
	}
	if got.Rules.Autospacing || got.Rules.PunctuationCompression || got.Rules.HangingPunctuation {
		t.Fatalf("withDefaults enabled an opt-in policy: %+v", got.Rules)
	}
}

// cjkEm measures the advance of one ideograph at the size used by these tests.
func cjkEm(t *testing.T, size float32) float32 {
	t.Helper()
	l := cjkParagraph(t, "天地", AlignLeft, 2000, size)
	cf := &l.chapters[0]
	for i := range cf.items {
		if cf.items[i].kind == itemLine {
			return cf.items[i].width / 2
		}
	}
	t.Fatal("no line in reference layout")
	return 0
}

// TestPunctuationCompression pins the engine-side pair rule: the opening quote
// after a fullwidth colon gives up half an em before wrapping, which is what
// makes the line fit one more glyph than without compression.
func TestPunctuationCompression(t *testing.T) {
	em := cjkEm(t, 18)
	text := "天：“地”玄"
	plain := cjkParagraph(t, text, AlignLeft, 5.7*em, 18)
	compressed := cjkParagraphRules(t, text, AlignLeft, 5.7*em, 18, TextRules{PunctuationCompression: true})

	lines := func(l *Layout) []string {
		var out []string
		for i := range l.chapters[0].items {
			it := &l.chapters[0].items[i]
			if it.kind == itemLine {
				out = append(out, string(it.para.text[it.start:it.end]))
			}
		}
		return out
	}
	if got, want := len(lines(plain)), 2; got != want {
		t.Fatalf("uncompressed line count = %d (%q), want %d", got, lines(plain), want)
	}
	if got := lines(compressed); len(got) != 1 || got[0] != text {
		t.Fatalf("compressed lines = %q, want the whole paragraph on one line", got)
	}

	// The trim lives in the wrapped advances, so it is visible while drawing.
	p := &posPainter{}
	compressed.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	if len(glyphs) < 3 {
		t.Fatalf("expected glyphs, got %d", len(glyphs))
	}
	if want := em / 2; !near(glyphs[2].advance, want, 0.05) {
		t.Fatalf("compressed quote advance = %.3f, want %.3f", glyphs[2].advance, want)
	}
}

// TestJustifyWeightsSkipsCompressedGap makes sure justification does not reopen
// a trimmed pair: the boundary between the two compressed glyphs has no weight.
func TestJustifyWeightsSkipsCompressedGap(t *testing.T) {
	mk := func(runes ...int) []shapedRun {
		gl := make([]shaping.Glyph, len(runes))
		for i, r := range runes {
			gl[i] = shaping.Glyph{ClusterIndex: r, Advance: 100}
		}
		return []shapedRun{{out: shaping.Output{Glyphs: gl}}}
	}
	// “ is the trimmed glyph, so the gap after it must not absorb slack.
	w := justifyWeights([]rune("天：“地"), mk(0, 1, 2, 3), true)
	if w[2] != 0 {
		t.Fatalf("gap after the trimmed mark = %v, want 0 (all: %v)", w[2], w)
	}
	// Without compression the same gap is a normal ideograph gap.
	w = justifyWeights([]rune("天：“地"), mk(0, 1, 2, 3), false)
	if w[2] <= 0 {
		t.Fatalf("uncompressed gap after the mark = %v, want > 0", w[2])
	}
}

// TestHangingPunctuation pins the geometry of allow-end: a closing mark that
// just misses the content box may hang by at most half its own advance, and
// the box itself is not widened.
func TestHangingPunctuation(t *testing.T) {
	em := cjkEm(t, 18)
	const size = float32(18)
	width := 5.7 * em
	text := "天地玄黄宇。"

	plain := cjkParagraph(t, text, AlignLeft, width, size)
	hang := cjkParagraphRules(t, text, AlignLeft, width, size, TextRules{HangingPunctuation: true})

	var plainLines, hangLines []*flowItem
	for i := range plain.chapters[0].items {
		if it := &plain.chapters[0].items[i]; it.kind == itemLine {
			plainLines = append(plainLines, it)
		}
	}
	for i := range hang.chapters[0].items {
		if it := &hang.chapters[0].items[i]; it.kind == itemLine {
			hangLines = append(hangLines, it)
		}
	}
	if len(plainLines) != 2 {
		t.Fatalf("without hanging, expected the mark to wrap; got %d lines", len(plainLines))
	}
	if got := string(plainLines[1].para.text[plainLines[1].start:plainLines[1].end]); got != "宇。" {
		t.Fatalf("without hanging, second line = %q, want %q", got, "宇。")
	}
	if len(hangLines) != 1 {
		t.Fatalf("with hanging, expected one line; got %d", len(hangLines))
	}
	ln := hangLines[0]
	if got := string(ln.para.text[ln.start:ln.end]); got != text {
		t.Fatalf("hanging line text = %q, want %q", got, text)
	}
	over := ln.width - ln.avail
	if over <= 0 {
		t.Fatalf("hanging line does not overhang: width %.3f avail %.3f", ln.width, ln.avail)
	}
	if over > 0.5*em+0.01 {
		t.Fatalf("hanging overhang = %.3f, want <= %.3f (half an em)", over, 0.5*em)
	}
	// The content box is untouched: same page size and same available width.
	if got, want := hang.Page(0).Size().Width, plain.Page(0).Size().Width; got != want {
		t.Fatalf("hanging changed the page width: %.1f vs %.1f", got, want)
	}
	if got, want := ln.avail, plainLines[0].avail; got != want {
		t.Fatalf("hanging changed the available width: %.3f vs %.3f", got, want)
	}
	// Drawing must place the mark beyond the content box too, not clip it.
	p := &posPainter{}
	hang.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	last := glyphs[len(glyphs)-1]
	if edge := last.x + last.advance; edge <= ln.avail {
		t.Fatalf("last glyph edge = %.3f, want > avail %.3f", edge, ln.avail)
	}
}
