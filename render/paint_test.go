package render

import (
	"context"
	"testing"

	"github.com/laic7092/layx/render/paint"
)

var (
	testInk   = paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff}
	testInk2  = paint.Color{R: 0xe8, G: 0xe8, B: 0xee, A: 0xff}
	testLink  = paint.Color{R: 0x1a, G: 0x5f, B: 0xc4, A: 0xff}
	testLink2 = paint.Color{R: 0x9e, G: 0xc4, B: 0xff, A: 0xff}
)

// runColors returns the colour of every glyph run on page 0.
func runColors(t *testing.T, l *Layout) []paint.Color {
	t.Helper()
	p := l.Page(0)
	if p == nil {
		t.Fatal("layout has no page 0")
	}
	rec := &capturePainter{}
	p.Draw(rec)
	var out []paint.Color
	for _, run := range rec.runs {
		out = append(out, run.Color)
	}
	return out
}

func hasColor(colors []paint.Color, want paint.Color) bool {
	for _, c := range colors {
		if c == want {
			return true
		}
	}
	return false
}

// TestRelayoutRepaintsInk pins the promise behind PaintKey: the ink a page is
// drawn in belongs to the layout's Typography, not to the cached line boxes, so
// a theme change repaints without re-breaking lines.
func TestRelayoutRepaintsInk(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)
	base.Typography.Color = testInk
	e := mustEngine(t, 2)
	first := layoutWith(t, e, doc, base)
	if got := runColors(t, first); !hasColor(got, testInk) {
		t.Fatalf("first layout drew %v, want ink %v", got, testInk)
	}

	next := base
	next.Typography.Color = testInk2
	next.Typography.Background = paint.Color{R: 0x4a, G: 0x4a, B: 0x4d, A: 0xff}
	second := relayoutWith(t, first, next)
	if second.breakKeys[0] != first.breakKeys[0] || second.pageKeys[0] != first.pageKeys[0] {
		t.Fatal("a theme change invalidated a layout key")
	}
	got := runColors(t, second)
	if !hasColor(got, testInk2) {
		t.Fatalf("relayout drew %v, want ink %v", got, testInk2)
	}
	if hasColor(got, testInk) {
		t.Fatalf("relayout kept the old ink: %v", got)
	}
}

// linkDoc is one paragraph with a link and plain text beside it. Style 2 names
// its own colour, the way author CSS does.
func linkDoc(t *testing.T) *Document {
	t.Helper()
	styles := []Style{
		{},
		{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, MarginBottom: 8},
		{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{R: 0xc0, G: 0x30, B: 0x20, A: 0xff}},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1, 2, 3}},
		{Kind: NodeText, Text: "a plain run, "},
		{Kind: NodeText, Style: 2, Text: "an author-coloured link", Link: "https://example.com/x"},
		{Kind: NodeText, Text: " and a reader-coloured link", Link: "https://example.com/y"},
	}
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

func linkOptions(t *testing.T, ink, link paint.Color) LayoutOptions {
	t.Helper()
	opts := keyTestOptions(t)
	opts.Typography.Color = ink
	opts.Typography.LinkColor = link
	return opts
}

// TestLinkColorIsPaintOnly proves a link whose style names no colour is drawn
// in Typography.LinkColor and follows a paint-only Relayout, while an author's
// own link colour is left alone.
func TestLinkColorIsPaintOnly(t *testing.T) {
	doc := linkDoc(t)
	e := mustEngine(t, 2)
	first := layoutWith(t, e, doc, linkOptions(t, testInk, testLink))

	got := runColors(t, first)
	if !hasColor(got, testLink) {
		t.Fatalf("layout drew %v, want link colour %v", got, testLink)
	}
	if !hasColor(got, testInk) {
		t.Fatalf("layout drew %v, want ink %v", got, testInk)
	}
	author := paint.Color{R: 0xc0, G: 0x30, B: 0x20, A: 0xff}
	if !hasColor(got, author) {
		t.Fatalf("layout drew %v, want the author's link colour %v", got, author)
	}

	second := relayoutWith(t, first, linkOptions(t, testInk, testLink2))
	got = runColors(t, second)
	if !hasColor(got, testLink2) {
		t.Fatalf("relayout drew %v, want link colour %v", got, testLink2)
	}
	if hasColor(got, testLink) {
		t.Fatalf("relayout kept the old link colour: %v", got)
	}
	if !hasColor(got, author) {
		t.Fatalf("relayout dropped the author's link colour: %v", got)
	}
}

// TestLinkColorDefaults covers a host that never sets LinkColor: links still
// look like links rather than turning into body text.
func TestLinkColorDefaults(t *testing.T) {
	if got := (Typography{}).withDefaults().LinkColor; got != DefaultLinkColor {
		t.Fatalf("default link colour = %v, want %v", got, DefaultLinkColor)
	}
	doc := linkDoc(t)
	e := mustEngine(t, 2)
	opts := linkOptions(t, testInk, paint.Color{})
	l := layoutWith(t, e, doc, opts)
	if got := runColors(t, l); !hasColor(got, DefaultLinkColor) {
		t.Fatalf("layout drew %v, want default link colour %v", got, DefaultLinkColor)
	}
}

// TestScrollTilesRepaintLinkColour covers the scroll path, which builds its
// tiles from the same line boxes.
func TestScrollTilesRepaintLinkColour(t *testing.T) {
	doc := linkDoc(t)
	e := mustEngine(t, 2)
	opts := linkOptions(t, testInk, testLink)
	opts.Mode = ModeScroll
	first := layoutWith(t, e, doc, opts)
	tile := first.Tile(0)
	if tile == nil {
		t.Fatal("no tile 0")
	}
	rec := &capturePainter{}
	tile.Draw(rec)
	var found bool
	for _, run := range rec.runs {
		if run.Color == testLink {
			found = true
		}
	}
	if !found {
		t.Fatalf("tile drew %v, want link colour %v", rec.runs, testLink)
	}

	opts.Typography.LinkColor = testLink2
	second, err := first.Relayout(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	tile = second.Tile(0)
	rec = &capturePainter{}
	tile.Draw(rec)
	found = false
	for _, run := range rec.runs {
		if run.Color == testLink2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("relaid tile did not repaint links")
	}
}

// TestWithColorsRepaintsTheSameLayout covers the theme switch itself: another
// paper keeps the document, every page and every line, and only the pages drawn
// afterwards are painted again — no shaping, no new keys.
func TestWithColorsRepaintsTheSameLayout(t *testing.T) {
	doc := linkDoc(t)
	e := mustEngine(t, 2)
	first := layoutWith(t, e, doc, linkOptions(t, testInk, testLink))
	if got := runColors(t, first); !hasColor(got, testInk) || !hasColor(got, testLink) {
		t.Fatalf("first layout drew %v, want ink %v and link %v", got, testInk, testLink)
	}

	shaped := e.shapes.Load()
	paper := paint.Color{R: 0x4a, G: 0x4a, B: 0x4d, A: 0xff}
	second := first.WithColors(testInk2, paper, testLink2)
	if got := e.shapes.Load() - shaped; got != 0 {
		t.Fatalf("a theme change shaped %d runs, want 0", got)
	}
	if second.Document() != doc || second.PageCount() != first.PageCount() {
		t.Fatal("a theme change moved the book")
	}
	if second.breakKeys[0] != first.breakKeys[0] || second.pageKeys[0] != first.pageKeys[0] {
		t.Fatal("a theme change derived new layout keys")
	}
	if second.paintKey == first.paintKey {
		t.Fatal("PaintKey did not change for a theme change")
	}
	if got, want := second.Typography().Color, testInk2; got != want {
		t.Fatalf("recoloured ink = %v, want %v", got, want)
	}
	got := runColors(t, second)
	if !hasColor(got, testInk2) || !hasColor(got, testLink2) {
		t.Fatalf("recoloured layout drew %v, want ink %v and link %v", got, testInk2, testLink2)
	}
	if hasColor(got, testInk) || hasColor(got, testLink) {
		t.Fatalf("recoloured layout kept the old colours: %v", got)
	}
	// The layout the host replaced still prints in its own colours.
	if got := runColors(t, first); !hasColor(got, testInk) {
		t.Fatalf("the old layout was recoloured too: %v", got)
	}
}

// TestWithColorsRepaintsScrollTiles is the same for the scrolling column, whose
// tiles are cached against the layout as well.
func TestWithColorsRepaintsScrollTiles(t *testing.T) {
	doc := linkDoc(t)
	e := mustEngine(t, 2)
	opts := linkOptions(t, testInk, testLink)
	opts.Mode = ModeScroll
	first := layoutWith(t, e, doc, opts)
	second := first.WithColors(testInk2, first.Typography().Background, testLink2)

	tileColors := func(l *Layout) []paint.Color {
		tile := l.Tile(0)
		if tile == nil {
			t.Fatal("no tile 0")
		}
		rec := &capturePainter{}
		tile.Draw(rec)
		var out []paint.Color
		for _, run := range rec.runs {
			out = append(out, run.Color)
		}
		return out
	}
	if got := tileColors(second); !hasColor(got, testLink2) {
		t.Fatalf("recoloured tile drew %v, want link %v", got, testLink2)
	}
	if got := tileColors(first); !hasColor(got, testLink) {
		t.Fatalf("the old tile was recoloured too: %v", got)
	}
	if second.ContentHeight() != first.ContentHeight() || second.TileCount() != first.TileCount() {
		t.Fatal("a theme change moved the column")
	}
}

// BenchmarkWithColors5M is the theme-switch target: unlike Relayout it does not
// walk the book at all, so the cost must not follow the book's size.
func BenchmarkWithColors5M(b *testing.B) {
	doc, fs := cjkBenchDoc(b, 5_000_000)
	e, err := New(Options{Workers: 8})
	if err != nil {
		b.Fatal(err)
	}
	l, err := e.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ink := testInk
		if i%2 == 0 {
			ink = testInk2
		}
		if next := l.WithColors(ink, testInk, testLink); next.PageCount() == 0 {
			b.Fatal("no pages")
		}
	}
}
