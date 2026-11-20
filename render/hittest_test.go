package render

import (
	"context"
	"testing"

	"github.com/laic7092/layx/render/paint"
)

// TestHitTestUnifiedPaged checks that Layout.HitTest and Page.HitTest agree on
// the same point once the page-local coordinate is converted to a content
// coordinate. Both take a paint.Point; there is no separate scalar overload.
func TestHitTestUnifiedPaged(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 4)
	if l.PageCount() == 0 {
		t.Fatal("layout has no pages")
	}
	contentH := l.PageContentHeight()
	checked := 0
	for p := 0; p < l.PageCount() && checked < 8; p++ {
		page := l.Page(p)
		if page == nil {
			t.Fatalf("Page(%d) = nil", p)
		}
		for y := float32(30); y < l.Size().Height-30 && checked < 8; y += 17 {
			for x := float32(30); x < l.Size().Width-30 && checked < 8; x += 23 {
				pageLoc, pageOK := page.HitTest(paint.Point{X: x, Y: y})
				if !pageOK {
					continue
				}
				// Page.HitTest's point is page-local: Layout.HitTest wants
				// the same point converted to content coordinates, i.e. x
				// without the left margin and y in the page-slot space.
				contentX := x - l.Margins().Left
				contentY := float32(p)*contentH + (y - l.Margins().Top)
				layoutLoc, layoutOK := l.HitTest(paint.Point{X: contentX, Y: contentY})
				if !layoutOK {
					t.Fatalf("page %d HitTest(%v,%v) = %+v but Layout.HitTest missed", p, x, y, pageLoc)
				}
				if layoutLoc != pageLoc {
					t.Fatalf("page %d: Layout.HitTest = %+v, Page.HitTest = %+v", p, layoutLoc, pageLoc)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no page hit in the test document")
	}

	// Points outside the viewport clamp instead of panicking.
	if _, ok := l.HitTest(paint.Point{X: -5000, Y: -5000}); !ok {
		t.Fatal("Layout.HitTest far outside content should clamp to the first item")
	}
}

// TestHitTestUnifiedScroll covers the scroll-mode branch of the unified
// signature: the point is a content coordinate, y=0 being the top of the flow.
func TestHitTestUnifiedScroll(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	opts := scrollOptions(t, ModeScroll, 97)
	l := layoutOptions(t, mustEngine(t, 4), doc, opts)

	if l.TileCount() == 0 {
		t.Fatal("scroll layout has no tiles")
	}
	loc, ok := l.LocationAt(1)
	if !ok {
		t.Fatal("LocationAt(1) failed")
	}
	hit, ok := l.HitTest(paint.Point{X: 40, Y: 1})
	if !ok {
		t.Fatal("Layout.HitTest missed the first line")
	}
	if hit.Chapter != loc.Chapter {
		t.Fatalf("HitTest chapter = %d, LocationAt chapter = %d", hit.Chapter, loc.Chapter)
	}
	if _, ok := l.HitTest(paint.Point{X: -5000, Y: -5000}); !ok {
		t.Fatal("Layout.HitTest far outside content should clamp to the first item")
	}

	// The scroll content coordinate of a known location maps back to it.
	y, ok := l.OffsetOf(hit)
	if !ok {
		t.Fatal("OffsetOf(hit) failed")
	}
	if again, ok := l.HitTest(paint.Point{X: 40, Y: y + 1}); !ok || again.Chapter != hit.Chapter {
		t.Fatalf("HitTest(OffsetOf) = %+v, %v; want chapter %d", again, ok, hit.Chapter)
	}
}

// TestHitTestEmptyText covers the empty-flow case in both modes: an empty text
// node makes both hit tests miss, never index unitOf[0].
func TestHitTestEmptyText(t *testing.T) {
	b := NewDocumentBuilder().SetStyles([]Style{
		{FontFamily: "Test Serif", FontSize: 18},
		{},
	})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Style: 0, Text: ""},
	}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fs := testFontSet(t)
	pagedOpts := LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   18,
		},
		Fonts: fs,
	}
	eng := mustEngine(t, 2)
	l, err := eng.Layout(context.Background(), doc, pagedOpts)
	if err != nil {
		t.Fatalf("paged Layout: %v", err)
	}
	if page := l.Page(0); page == nil {
		t.Fatal("empty chapter should still have a blank page")
	} else if loc, ok := page.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Page.HitTest = %+v, %v; want zero, false", loc, ok)
	}
	if loc, ok := l.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Layout.HitTest = %+v, %v; want zero, false", loc, ok)
	}

	scrollOpts := pagedOpts
	scrollOpts.Mode = ModeScroll
	scrollOpts.TileHeight = 80
	sl, err := eng.Layout(context.Background(), doc, scrollOpts)
	if err != nil {
		t.Fatalf("scroll Layout: %v", err)
	}
	if loc, ok := sl.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("scroll Layout.HitTest = %+v, %v; want zero, false", loc, ok)
	}
}

// TestHitTestRangeFirstChapter covers the range case: y=0 belongs to the first
// included chapter, not to chapter 0 of the document.
func TestHitTestRangeFirstChapter(t *testing.T) {
	doc := multiChapterDoc(t, 4)
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	opts.Mode = ModeScroll
	opts.TileHeight = 97
	eng := mustEngine(t, 2)

	ranged, err := eng.LayoutRange(context.Background(), doc, opts, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}
	loc, ok := ranged.HitTest(paint.Point{X: 30, Y: 1})
	if !ok {
		t.Fatal("ranged scroll HitTest missed")
	}
	if loc.Chapter != 1 {
		t.Fatalf("ranged scroll HitTest chapter = %d, want 1", loc.Chapter)
	}
}
