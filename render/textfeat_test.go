package render

import (
	"context"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

type fillRec struct {
	rect  paint.Rect
	color paint.Color
}

type roundRec struct {
	rect   paint.Rect
	radius float32
	color  paint.Color
}

// capturePainter records everything the display list emits. It is used by the
// feature tests below to assert on decoration/background/border geometry.
type capturePainter struct {
	fills  []fillRec
	rounds []roundRec
	runs   []paint.GlyphRun
	images int
}

func (p *capturePainter) Save()               {}
func (p *capturePainter) Restore()            {}
func (p *capturePainter) ClipRect(paint.Rect) {}
func (p *capturePainter) Transform(paint.Affine) {
}
func (p *capturePainter) FillRect(r paint.Rect, c paint.Color) {
	p.fills = append(p.fills, fillRec{rect: r, color: c})
}
func (p *capturePainter) StrokeRect(paint.Rect, paint.Color, float32) {}
func (p *capturePainter) FillRoundRect(r paint.Rect, radius float32, c paint.Color) {
	p.rounds = append(p.rounds, roundRec{rect: r, radius: radius, color: c})
}
func (p *capturePainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) { p.images++ }
func (p *capturePainter) DrawGlyphs(run paint.GlyphRun) {
	run.Glyphs = append([]paint.Glyph(nil), run.Glyphs...)
	p.runs = append(p.runs, run)
}

func featureDoc(t testing.TB, styles []Style, nodes []Node) *Document {
	t.Helper()
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "ch", Root: 0, Nodes: nodes}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func featureLayout(t testing.TB, doc *Document, opts LayoutOptions) *Layout {
	t.Helper()
	l, err := mustEngine(t, 2).Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return l
}

func featureOptions(t testing.TB, width, height float32) LayoutOptions {
	t.Helper()
	return LayoutOptions{
		Viewport: Viewport{Width: width, Height: height},
		Typography: Typography{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff},
		},
		Fonts:   testFontSet(t),
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	}
}

func wordSpacingDoc(t testing.TB, wordSpacing float32) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, WordSpacing: wordSpacing,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: strings.Repeat("word ", 120)},
	}
	return featureDoc(t, styles, nodes)
}

func TestWordSpacingAdvanceAndLineCount(t *testing.T) {
	const spacing = 10
	base := featureLayout(t, wordSpacingDoc(t, 0), featureOptions(t, 420, 600))
	wide := featureLayout(t, wordSpacingDoc(t, spacing), featureOptions(t, 420, 600))

	spaceAdvance := func(l *Layout) float32 {
		p := &posPainter{}
		l.Page(0).Draw(p)
		for _, g := range firstLineGlyphs(p) {
			if g.cluster == 4 { // the space after "word"
				return g.advance
			}
		}
		t.Fatal("space glyph not found on first line")
		return 0
	}
	if got, want := spaceAdvance(wide)-spaceAdvance(base), float32(spacing); !near(got, want, 0.01) {
		t.Fatalf("word-spacing changed the space advance by %v, want %v", got, want)
	}

	countLines := func(l *Layout) int {
		n := 0
		for i := range l.chapters[0].items {
			if l.chapters[0].items[i].kind == itemLine {
				n++
			}
		}
		return n
	}
	if narrowBase, narrowWide := countLines(featureLayout(t, wordSpacingDoc(t, 0), featureOptions(t, 140, 600))),
		countLines(featureLayout(t, wordSpacingDoc(t, spacing), featureOptions(t, 140, 600))); narrowWide <= narrowBase {
		t.Fatalf("word-spacing did not increase line count in a narrow column: %d <= %d", narrowWide, narrowBase)
	}
}

func TestWordSpacingOnlyAffectsSpaces(t *testing.T) {
	doc := wordSpacingDoc(t, 10)
	l := featureLayout(t, doc, featureOptions(t, 420, 600))
	p := &posPainter{}
	l.Page(0).Draw(p)
	glyphs := firstLineGlyphs(p)
	for i := 1; i < len(glyphs) && i < 4; i++ {
		// Clusters 0..3 are the letters of "word"; only the space may grow.
		if glyphs[i].cluster == glyphs[i-1].cluster+1 && glyphs[i].cluster != 4 &&
			!near(glyphs[i].x-glyphs[i-1].x, glyphs[i-1].advance, 0.01) {
			t.Fatalf("letter advance changed at cluster %d: %v vs %v",
				glyphs[i].cluster, glyphs[i].x-glyphs[i-1].x, glyphs[i-1].advance)
		}
	}
}

var (
	colTop    = paint.Color{R: 0x10, A: 0xff}
	colBottom = paint.Color{R: 0x20, A: 0xff}
	colLeft   = paint.Color{R: 0x30, A: 0xff}
	colRight  = paint.Color{R: 0x40, A: 0xff}
	colBG     = paint.Color{R: 0x50, A: 0xff}
)

func borderDoc(t testing.TB, radius float32) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color:        paint.Color{A: 0xff},
			BorderTop:    Border{Width: 2, Color: colTop},
			BorderRight:  Border{Width: 3, Color: colRight},
			BorderBottom: Border{Width: 4, Color: colBottom},
			BorderLeft:   Border{Width: 5, Color: colLeft},
			Background:   colBG,
			CornerRadius: radius,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 80)},
	}
	return featureDoc(t, styles, nodes)
}

func TestBorderShrinksContentBox(t *testing.T) {
	l := featureLayout(t, borderDoc(t, 0), featureOptions(t, 420, 400))
	it := &l.chapters[0].items[0]
	// 420 - 48 margins - 5 left - 3 right
	if want := float32(364); !near(it.avail, want, 0.01) {
		t.Fatalf("line available width = %v, want %v", it.avail, want)
	}
}

func countColor(fills []fillRec, c paint.Color) int {
	n := 0
	for _, f := range fills {
		if f.color == c {
			n++
		}
	}
	return n
}

func TestBorderPaginationEdges(t *testing.T) {
	l := featureLayout(t, borderDoc(t, 0), featureOptions(t, 420, 300))
	if l.PageCount() < 3 {
		t.Fatalf("need a block spanning at least 3 pages, got %d", l.PageCount())
	}
	draw := func(n int) *capturePainter {
		p := &capturePainter{}
		l.Page(n).Draw(p)
		return p
	}
	first := draw(0)
	if countColor(first.fills, colTop) == 0 {
		t.Fatal("first page is missing the top border")
	}
	if countColor(first.fills, colLeft) == 0 || countColor(first.fills, colRight) == 0 {
		t.Fatal("first page is missing a side border")
	}
	if countColor(first.fills, colBG) == 0 {
		t.Fatal("first page is missing the background")
	}
	if countColor(first.fills, colBottom) != 0 {
		t.Fatal("first page drew the bottom border")
	}

	mid := draw(l.PageCount() / 2)
	if countColor(mid.fills, colTop) != 0 || countColor(mid.fills, colBottom) != 0 {
		t.Fatal("middle page drew a top or bottom border")
	}
	if countColor(mid.fills, colLeft) == 0 || countColor(mid.fills, colRight) == 0 {
		t.Fatal("middle page is missing a side border")
	}
	if countColor(mid.fills, colBG) == 0 {
		t.Fatal("middle page is missing the background")
	}

	last := draw(l.PageCount() - 1)
	if countColor(last.fills, colBottom) == 0 {
		t.Fatal("last page is missing the bottom border")
	}
	if countColor(last.fills, colTop) != 0 {
		t.Fatal("last page drew the top border")
	}
}

func TestNestedBackgroundsSurvivePageBreaks(t *testing.T) {
	colChild := paint.Color{G: 0x90, A: 0xff}
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color:        paint.Color{A: 0xff},
			BorderTop:    Border{Width: 2, Color: colTop},
			BorderRight:  Border{Width: 3, Color: colRight},
			BorderBottom: Border{Width: 4, Color: colBottom},
			BorderLeft:   Border{Width: 5, Color: colLeft},
			Background:   colBG,
		},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, Background: colChild,
		},
	}
	// A tall parent box holding short child boxes: on every page after the
	// first, some child ends above the page top while the parent still covers
	// it, which is the case that used to drop the parent's background.
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
	}
	var kids []NodeID
	for i := 0; i < 12; i++ {
		text := NodeID(len(nodes))
		nodes = append(nodes, Node{
			Kind: NodeText,
			Text: strings.Repeat("A child block with its own background. ", 12),
		})
		block := NodeID(len(nodes))
		nodes = append(nodes, Node{Kind: NodeBlock, Style: 2, Children: []NodeID{text}})
		kids = append(kids, block)
	}
	nodes[0].Children = kids
	doc := featureDoc(t, styles, nodes)
	opts := featureOptions(t, 420, 300)
	l := featureLayout(t, doc, opts)
	if l.PageCount() < 3 {
		t.Fatalf("need a box spanning at least 3 pages, got %d", l.PageCount())
	}
	for n := 0; n < l.PageCount(); n++ {
		p := &capturePainter{}
		l.Page(n).Draw(p)
		if countColor(p.fills, colBG) == 0 {
			t.Fatalf("page %d is missing the parent background", n)
		}
		if countColor(p.fills, colLeft) == 0 || countColor(p.fills, colRight) == 0 {
			t.Fatalf("page %d is missing a parent side border", n)
		}
		if countColor(p.fills, colChild) == 0 {
			t.Fatalf("page %d is missing a child background", n)
		}
	}

	// The same clip walk drives scroll tiles.
	opts.Mode = ModeScroll
	opts.TileHeight = 120
	scroll := featureLayout(t, doc, opts)
	for n := 0; n < scroll.TileCount(); n++ {
		p := &capturePainter{}
		scroll.Tile(n).Draw(p)
		if countColor(p.fills, colBG) == 0 {
			t.Fatalf("tile %d is missing the parent background", n)
		}
	}
}

func TestBackgroundCornerRadiusUsesRoundRect(t *testing.T) {
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, Background: colBG, CornerRadius: 8,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: "rounded background"},
	}
	l := featureLayout(t, featureDoc(t, styles, nodes), featureOptions(t, 420, 300))
	p := &capturePainter{}
	l.Page(0).Draw(p)
	if len(p.rounds) != 1 {
		t.Fatalf("round backgrounds = %d, want 1", len(p.rounds))
	}
	if !near(p.rounds[0].radius, 8, 0.001) {
		t.Fatalf("radius = %v, want 8", p.rounds[0].radius)
	}
	if len(p.fills) != 0 {
		t.Fatalf("rounded background also emitted %d plain fills", len(p.fills))
	}
}

func decorationDoc(t testing.TB, kind TextDecoration, small, large bool) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, TextDecoration: kind,
		},
		{
			FontFamily: "Test Serif", FontSize: 12, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, TextDecoration: kind,
		},
		{
			FontFamily: "Test Serif", FontSize: 24, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, TextDecoration: kind,
		},
	}
	var children []NodeID
	var nodes []Node
	nodes = append(nodes, Node{Kind: NodeBlock, Style: 1})
	if small {
		nodes = append(nodes, Node{Kind: NodeText, Style: 2, Text: "small text "})
		children = append(children, NodeID(len(nodes)-1))
	}
	if large {
		nodes = append(nodes, Node{Kind: NodeText, Style: 3, Text: "large text "})
		children = append(children, NodeID(len(nodes)-1))
	}
	if !small && !large {
		nodes = append(nodes, Node{Kind: NodeText, Text: strings.Repeat("decoration text ", 60)})
		children = append(children, 1)
	}
	nodes[0].Children = children
	return featureDoc(t, styles, nodes)
}

func TestTextDecorationGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind TextDecoration
		down bool
	}{
		{"underline", TextDecorationUnderline, true},
		{"line-through", TextDecorationLineThrough, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := featureLayout(t, decorationDoc(t, tc.kind, false, false), featureOptions(t, 420, 600))
			info := &l.pages[0]
			cf := &l.chapters[info.chapter]
			lines := 0
			for i := info.first; i < info.last; i++ {
				if cf.items[i].kind == itemLine {
					lines++
				}
			}
			p := &capturePainter{}
			l.Page(0).Draw(p)
			if len(p.fills) != lines {
				t.Fatalf("decoration fills = %d, lines = %d (one fill per decorated run)", len(p.fills), lines)
			}
			if len(p.runs) == 0 {
				t.Fatal("no glyph runs")
			}
			// Collect each line's baseline so a fill can be checked against the
			// line it belongs to, not just the first line on the page.
			var baselines []float32
			for i := info.first; i < info.last; i++ {
				it := &cf.items[i]
				if it.kind == itemLine {
					baselines = append(baselines, l.opts.Margins.Top-info.top+it.y+it.baseline)
				}
			}
			for _, f := range p.fills {
				baseline := baselines[0]
				for _, b := range baselines {
					if absF32(f.rect.Y-b) < absF32(f.rect.Y-baseline) {
						baseline = b
					}
				}
				if tc.down && f.rect.Y <= baseline {
					t.Fatalf("%s rect Y = %v, want below baseline %v", tc.name, f.rect.Y, baseline)
				}
				if !tc.down && f.rect.Y >= baseline {
					t.Fatalf("%s rect Y = %v, want above baseline %v", tc.name, f.rect.Y, baseline)
				}
				if f.rect.Width <= 0 || f.rect.Height <= 0 {
					t.Fatalf("empty decoration rect: %+v", f.rect)
				}
			}
		})
	}
}

func TestTextDecorationPerRunMetrics(t *testing.T) {
	l := featureLayout(t, decorationDoc(t, TextDecorationUnderline, true, true), featureOptions(t, 420, 600))
	p := &capturePainter{}
	l.Page(0).Draw(p)
	if len(p.fills) != 2 {
		t.Fatalf("fills = %d, want one per run (2)", len(p.fills))
	}
	// The 24px run's underline sits farther below the shared baseline than the
	// 12px run's. Fills are emitted in run order, i.e. small then large.
	if p.fills[1].rect.Y <= p.fills[0].rect.Y {
		t.Fatalf("large run underline Y = %v, small = %v; want the larger run lower",
			p.fills[1].rect.Y, p.fills[0].rect.Y)
	}
}

func boldDoc(t testing.TB) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, FontWeight: 700,
			LineHeight: 1.5, Color: paint.Color{A: 0xff},
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: "bold request with a regular face"},
	}
	return featureDoc(t, styles, nodes)
}

func TestEmulateBoldFlag(t *testing.T) {
	face := loadTestFont(t)
	onlyRegular, err := NewFontSet(face)
	if err != nil {
		t.Fatal(err)
	}
	withBold, err := NewFontSet(face, FontFace{
		Family: face.Family, Weight: 700, Data: face.Data, Index: face.Index,
	})
	if err != nil {
		t.Fatal(err)
	}

	check := func(fs *FontSet, want bool) {
		t.Helper()
		opts := featureOptions(t, 420, 300)
		opts.Fonts = fs
		l := featureLayout(t, boldDoc(t), opts)
		p := &capturePainter{}
		l.Page(0).Draw(p)
		if len(p.runs) == 0 {
			t.Fatal("no glyph runs")
		}
		for _, run := range p.runs {
			if run.EmulateBold != want {
				t.Fatalf("EmulateBold = %v, want %v for %s", run.EmulateBold, want, run.Font.Family)
			}
		}
	}
	check(onlyRegular, true)
	check(withBold, false)
}

// keyTestStyleDoc returns a document whose single block style is controlled by
// mutate, so each feature field can be toggled independently.
func keyTestStyleDoc(t testing.TB, mutate func(*Style)) *Document {
	t.Helper()
	st := Style{
		FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
		Color: paint.Color{A: 0xff}, MarginBottom: 8,
	}
	mutate(&st)
	styles := []Style{{}, st}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20)},
	}
	return featureDoc(t, styles, nodes)
}

func TestFeatureBreakKeys(t *testing.T) {
	base := keyTestOptions(t)
	valid, err := base.validate()
	if err != nil {
		t.Fatal(err)
	}
	// Keys are only comparable within one variant chain, so all documents are
	// hashed with the same keyer instead of comparing separate Layout calls.
	var keyer *keyer
	keys := func(mutate func(*Style)) (keyHash, keyHash) {
		if keyer == nil {
			keyer = newVariantCache(1).newKeyer()
		}
		doc := keyTestStyleDoc(t, mutate)
		ch := &doc.chapters[0]
		bk := chapterBreakKey(keyer, doc, ch, valid, valid.Typography)
		pk := pageKey(keyer, bk, valid.Typography, contentHeight(valid))
		return bk, pk
	}
	origBreak, origPage := keys(func(*Style) {})

	cases := []struct {
		name   string
		mutate func(*Style)
		breaks bool
	}{
		{"word-spacing", func(s *Style) { s.WordSpacing = 10 }, true},
		{"border-top-width", func(s *Style) { s.BorderTop.Width = 2 }, true},
		{"border-right-width", func(s *Style) { s.BorderRight.Width = 2 }, true},
		{"border-bottom-width", func(s *Style) { s.BorderBottom.Width = 2 }, true},
		{"border-left-width", func(s *Style) { s.BorderLeft.Width = 2 }, true},
		{"border-color", func(s *Style) { s.BorderTop = Border{Width: 0, Color: colTop} }, false},
		{"background", func(s *Style) { s.Background = colBG }, false},
		{"corner-radius", func(s *Style) { s.CornerRadius = 6 }, false},
		{"text-decoration", func(s *Style) { s.TextDecoration = TextDecorationUnderline }, false},
		{"decoration-color", func(s *Style) { s.TextDecorationColor = colLeft }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextBreak, nextPage := keys(tc.mutate)
			if changed := nextBreak != origBreak; changed != tc.breaks {
				t.Fatalf("BreakKey changed = %v, want %v", changed, tc.breaks)
			}
			if changed := nextPage != origPage; changed != tc.breaks {
				t.Fatalf("PageKey changed = %v, want %v", changed, tc.breaks)
			}
		})
	}
}

func TestWordSpacingTypographyIsKeyed(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)
	orig := featureLayout(t, doc, base)
	next := base
	next.Typography.WordSpacing = 6
	// Relayout keeps the same variant seeds, so the keys are comparable.
	other := relayoutWith(t, orig, next)
	if orig.breakKeys[0] == other.breakKeys[0] {
		t.Fatal("Typography.WordSpacing did not change BreakKey")
	}
}

func TestBorderAndBackgroundInScrollTiles(t *testing.T) {
	opts := featureOptions(t, 420, 300)
	opts.Mode = ModeScroll
	opts.TileHeight = 120
	l := featureLayout(t, borderDoc(t, 0), opts)
	count := l.TileCount()
	if count < 3 {
		t.Fatalf("need at least 3 tiles, got %d", count)
	}
	draw := func(n int) *capturePainter {
		p := &capturePainter{}
		l.Tile(n).Draw(p)
		return p
	}
	first := draw(0)
	if countColor(first.fills, colTop) == 0 {
		t.Fatal("first tile is missing the top border")
	}
	if countColor(first.fills, colBG) == 0 {
		t.Fatal("first tile is missing the background")
	}
	mid := draw(count / 2)
	if countColor(mid.fills, colTop) != 0 || countColor(mid.fills, colBottom) != 0 {
		t.Fatal("middle tile drew a top or bottom border")
	}
	if countColor(mid.fills, colLeft) == 0 || countColor(mid.fills, colRight) == 0 {
		t.Fatal("middle tile is missing a side border")
	}
	last := draw(count - 1)
	if countColor(last.fills, colBottom) == 0 {
		t.Fatal("last tile is missing the bottom border")
	}
	if countColor(last.fills, colTop) != 0 {
		t.Fatal("last tile drew the top border")
	}
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
