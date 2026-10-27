package render

import (
	"context"
	"testing"

	"github.com/laix/layx/render/paint"
)

// FuzzLayoutText is the audit §2.3.4 fuzz target: untrusted text must never
// panic the library. It drives the full public surface a host touches when it
// displays a book — layout, paged display lists, hit testing, selection,
// progress — in both paged and scroll mode, and lets seeded crashes from the
// audit's hit matrix stay in the corpus forever.
//
// Errors are legitimate results (a document can be rejected); panics are not.
// The seed corpus is the audit §2.1 matrix: every entry that used to panic in
// go-text's bidi segmentation or in hitTestItems.
func FuzzLayoutText(f *testing.F) {
	for _, seed := range []string{
		"",
		"abc",
		"abc\ndef",
		"\u202e",
		"abc\nالعربية",
		"abc\nภาษาไทย",
		"abc\nשלום",
		"abc\nनमस्ते",
		"العربية",
		"\nالعربية",
		"a\r\nb",
		"a\u2029b",
		"a\u001cb\u0085d",
		"abc\n\ndef",
		"LTR العربية\nעברית 123",
	} {
		f.Add(seed)
	}

	fonts := testFontSet(f)
	eng := mustEngine(f, 2)
	paged := LayoutOptions{
		Viewport: Viewport{Width: 360, Height: 480},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   16,
			LineHeight: 1.4,
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: 16, Right: 16, Bottom: 16, Left: 16},
	}
	scroll := paged
	scroll.Mode = ModeScroll
	scroll.TileHeight = MinTileHeight

	f.Fuzz(func(t *testing.T, text string) {
		// Bound the work per execution: a fuzz input is a few bytes in
		// practice, but a corpus entry must not be able to make the target
		// crawl.
		if len(text) > 8<<10 {
			text = text[:8<<10]
		}
		doc := fuzzDocument(t, text)
		if doc == nil {
			return
		}
		ctx := context.Background()
		painter := &posPainter{}

		// Paged mode: the paths a reader hits while turning pages.
		l, err := eng.Layout(ctx, doc, paged)
		if err != nil || l == nil {
			return
		}
		if n := l.PageCount(); n > 0 {
			for i := 0; i < n && i < 3; i++ {
				p := l.Page(i)
				if p == nil {
					continue
				}
				p.Size()
				p.Draw(painter)
				p.HitTest(paint.Point{X: 20, Y: 20})
				p.HitTest(paint.Point{X: -1, Y: 1e9})
			}
		}
		runes := []rune(text)
		end := Location{Chapter: 0, Node: 1, Offset: len(runes)}
		l.HitTest(paint.Point{X: 10, Y: 10})
		l.HitTest(paint.Point{X: 1e9, Y: -1e9})
		l.Progress(end)
		l.Progress(Location{Chapter: 0, Node: 1, Offset: 0})
		l.Selection(Location{Chapter: 0, Node: 1, Offset: 0}, end)
		l.OffsetOf(end)
		l.PageOf(end)

		// Scroll mode: tiles and content coordinates.
		sl, err := eng.Layout(ctx, doc, scroll)
		if err != nil || sl == nil {
			return
		}
		if n := sl.TileCount(); n > 0 {
			for i := 0; i < n && i < 3; i++ {
				tile := sl.Tile(i)
				if tile == nil {
					continue
				}
				tile.Size()
				tile.Draw(painter)
				tile.Top()
				tile.Height()
			}
		}
		sl.HitTest(paint.Point{X: 10, Y: 10})
		sl.HitTest(paint.Point{X: 1e9, Y: -1e9})
		sl.LocationAt(0)
		sl.LocationAt(sl.ContentHeight())
		sl.OffsetOf(end)
		sl.Progress(end)
		sl.Progress(Location{Chapter: 0, Node: 1, Offset: 0})
		sl.Selection(Location{Chapter: 0, Node: 1, Offset: 0}, end)
	})
}

// fuzzDocument turns fuzz text into the smallest interesting document: one
// block with one text node. It returns nil when the builder rejects the input,
// which is a valid outcome the layout never sees.
func fuzzDocument(t testing.TB, text string) *Document {
	t.Helper()
	b := NewDocumentBuilder().SetStyles([]Style{
		{},
		{FontFamily: "Test Serif", FontSize: 16},
	})
	if err := b.AddChapter(Chapter{ID: "fuzz", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Style: 1, Text: text},
	}}); err != nil {
		return nil
	}
	doc, err := b.Build()
	if err != nil {
		return nil
	}
	return doc
}
