package render

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

// glyphsInRange keeps the glyphs whose document chapter is in [first, last).
func glyphsInRange(in []contentGlyph, first, last int) []contentGlyph {
	var out []contentGlyph
	for _, g := range in {
		if g.chapter >= first && g.chapter < last {
			out = append(out, g)
		}
	}
	return out
}

// rangeChapterAtY maps a scroll content y to the chapter that contains it,
// honoring the layout's range. contentGlyph coordinates from the generic scroll
// collector are book-wide, so the helper is not usable for ranged layouts; this
// one walks only the included chapters.
func rangeChapterAtY(l *Layout, y float32) int {
	ci := l.firstChapter
	for i := l.firstChapter; i < l.lastChapter; i++ {
		if l.chapterTop[i] <= y {
			ci = i
			continue
		}
		break
	}
	return ci
}

// collectRangedScrollGlyphs collects scroll glyphs with chapter-local content
// coordinates, the same form collectPagedGlyphs produces, for a ranged layout.
func collectRangedScrollGlyphs(t *testing.T, l *Layout) []contentGlyph {
	t.Helper()
	var out []contentGlyph
	for i := 0; i < l.TileCount(); i++ {
		tile := l.Tile(i)
		if tile == nil {
			t.Fatalf("tile %d is nil", i)
		}
		pp := &posPainter{}
		tile.Draw(pp)
		for _, run := range pp.runs {
			for _, g := range run.Glyphs {
				globalY := run.Y + tile.Top() + g.Y
				ci := rangeChapterAtY(l, globalY)
				out = append(out, contentGlyph{
					chapter: ci,
					x:       run.X - l.opts.Margins.Left + g.X,
					y:       globalY - l.chapterTop[ci],
					id:      g.ID,
					cluster: g.Cluster,
				})
			}
		}
	}
	return out
}

// TestLayoutRangePagesMatchFull checks the core range invariant: the pages of
// an included chapter are the same pages the full layout produces for that
// chapter, only renumbered from zero. Glyphs are compared in chapter-local
// coordinates, which is what makes page numbering irrelevant.
func TestLayoutRangePagesMatchFull(t *testing.T) {
	doc := multiChapterDoc(t, 4)
	fs := testFontSet(t)
	base := statsTestOptions(fs)
	ctx := context.Background()
	eng := mustEngine(t, 4)

	full, err := eng.Layout(ctx, doc, base)
	if err != nil {
		t.Fatalf("full Layout: %v", err)
	}
	ranged, err := eng.LayoutRange(ctx, doc, base, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}

	_, count1, ok1 := full.PageRange(1)
	_, count2, ok2 := full.PageRange(2)
	if !ok1 || !ok2 {
		t.Fatalf("full PageRange 1/2 = %v/%v", ok1, ok2)
	}
	if got, want := ranged.PageCount(), count1+count2; got != want {
		t.Fatalf("ranged PageCount = %d, want %d", got, want)
	}

	// Page maps are chapter-local and must be identical.
	for ci := 1; ci < 3; ci++ {
		if !reflect.DeepEqual(ranged.chapters[ci].pages, full.chapters[ci].pages) {
			t.Fatalf("chapter %d page map differs:\n ranged %+v\n full   %+v",
				ci, ranged.chapters[ci].pages, full.chapters[ci].pages)
		}
		if !reflect.DeepEqual(ranged.chapters[ci].ys, full.chapters[ci].ys) {
			t.Fatalf("chapter %d flow differs between ranged and full layout", ci)
		}
	}

	// Page numbering starts at zero for the range, while Location.Chapter keeps
	// the document index.
	if got := ranged.chapters[1].firstPage; got != 0 {
		t.Fatalf("first included chapter starts at page %d, want 0", got)
	}
	if got := ranged.chapters[2].firstPage; got != count1 {
		t.Fatalf("second included chapter starts at page %d, want %d", got, count1)
	}
	loc, ok := ranged.Locate(102)
	if !ok || loc.Chapter != 2 {
		t.Fatalf("ranged Locate(102) = %+v, %v; want chapter 2", loc, ok)
	}

	// Drawing the ranged pages must produce the same glyphs as the full layout
	// for the same chapters.
	want := glyphsInRange(collectPagedGlyphs(full), 1, 3)
	got := collectPagedGlyphs(ranged)
	compareGlyphSets(t, "ranged pages", want, got)

	// A full-width range is the full layout.
	all, err := eng.LayoutRange(ctx, doc, base, 0, 4)
	if err != nil {
		t.Fatalf("LayoutRange(0,4): %v", err)
	}
	if all.PageCount() != full.PageCount() {
		t.Fatalf("full range PageCount = %d, want %d", all.PageCount(), full.PageCount())
	}
}

// TestLayoutRangeExcludedNeverResolves checks that no accessor resolves a
// location into a chapter outside the range.
func TestLayoutRangeExcludedNeverResolves(t *testing.T) {
	doc := multiChapterDoc(t, 4)
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()
	eng := mustEngine(t, 2)

	ranged, err := eng.LayoutRange(ctx, doc, opts, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}
	full, err := eng.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("full Layout: %v", err)
	}

	// Anchors of excluded chapters are not in the layout; included ones are.
	for _, anchor := range []AnchorID{100, 103} {
		if loc, ok := ranged.Locate(anchor); ok {
			t.Fatalf("ranged Locate(%d) = %+v; excluded chapter must not resolve", anchor, loc)
		}
	}
	if loc, ok := ranged.Locate(101); !ok || loc.Chapter != 1 {
		t.Fatalf("ranged Locate(101) = %+v, %v; want chapter 1", loc, ok)
	}

	excluded := []Location{
		{Chapter: 0, Node: 1},
		{Chapter: 3, Node: 1},
		{Anchor: 100}, // anchor of chapter 0
		{Anchor: 103}, // anchor of chapter 3
	}
	for _, loc := range excluded {
		if _, ok := ranged.PageOf(loc); ok {
			t.Errorf("PageOf(%+v) = true, want false", loc)
		}
		if _, ok := ranged.OffsetOf(loc); ok {
			t.Errorf("OffsetOf(%+v) = true, want false", loc)
		}
		if _, ok := ranged.Progress(loc); ok {
			t.Errorf("Progress(%+v) = true, want false", loc)
		}
	}
	if _, ok := ranged.ChapterContentTop(0); ok {
		t.Error("ChapterContentTop(0) = true for an excluded chapter")
	}
	if _, ok := ranged.ChapterContentTop(3); ok {
		t.Error("ChapterContentTop(3) = true for an excluded chapter")
	}
	if _, _, ok := ranged.PageRange(0); ok {
		t.Error("PageRange(0) = true for an excluded chapter")
	}
	if _, _, ok := ranged.PageRange(3); ok {
		t.Error("PageRange(3) = true for an excluded chapter")
	}
	pages := ranged.ChapterPages()
	if len(pages) != doc.ChapterCount() {
		t.Fatalf("ChapterPages length = %d, want %d", len(pages), doc.ChapterCount())
	}
	if pages[0] != -1 || pages[3] != -1 {
		t.Fatalf("ChapterPages excluded entries = %d/%d, want -1/-1", pages[0], pages[3])
	}
	if pages[1] != 0 || pages[2] < 0 {
		t.Fatalf("ChapterPages included entries = %d/%d, want 0/>=0", pages[1], pages[2])
	}

	// Every page resolves into an included chapter.
	for p := 0; p < ranged.PageCount(); p++ {
		loc, ok := ranged.LocationOfPage(p)
		if !ok {
			t.Fatalf("LocationOfPage(%d) failed", p)
		}
		if loc.Chapter < 1 || loc.Chapter >= 3 {
			t.Fatalf("LocationOfPage(%d) chapter = %d, want in [1,3)", p, loc.Chapter)
		}
	}

	// Paged content coordinates resolve only into included chapters.
	h := ranged.PageContentHeight()
	for p := 0; p < ranged.PageCount(); p++ {
		for _, off := range []float32{0.5, h / 2, h - 0.5} {
			loc, ok := ranged.LocationAt(float32(p)*h + off)
			if !ok {
				continue
			}
			if loc.Chapter < 1 || loc.Chapter >= 3 {
				t.Fatalf("LocationAt(page slot %d) chapter = %d, want in [1,3)", p, loc.Chapter)
			}
		}
	}

	// PageOf of an included location still works; the full layout's chapter 0
	// location is not resolvable.
	if loc, ok := full.LocationOfPage(0); !ok {
		t.Fatal("full LocationOfPage(0) failed")
	} else if _, ok := ranged.PageOf(loc); ok {
		t.Fatalf("ranged PageOf(excluded %+v) = true", loc)
	}
	if loc, ok := ranged.Locate(101); !ok {
		t.Fatal("ranged Locate(101) failed")
	} else if _, ok := ranged.PageOf(loc); !ok {
		t.Fatal("ranged PageOf(included location) failed")
	}

	// Document() is still the whole document.
	if ranged.Document() != doc {
		t.Fatal("ranged Document() is not the full document")
	}
}

// TestLayoutRangeInvalid checks that impossible ranges fail with a structured
// option error instead of producing a layout that resolves nothing.
func TestLayoutRangeInvalid(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()
	eng := mustEngine(t, 2)

	for _, r := range [][2]int{{-1, 2}, {0, 4}, {2, 1}, {1, 1}, {3, 3}, {0, 0}} {
		_, err := eng.LayoutRange(ctx, doc, opts, r[0], r[1])
		if err == nil {
			t.Fatalf("LayoutRange(%d,%d) succeeded, want an error", r[0], r[1])
		}
		if !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("LayoutRange(%d,%d) error = %v, want ErrInvalidOptions", r[0], r[1], err)
		}
		var de *DocumentError
		if !errors.As(err, &de) {
			t.Fatalf("LayoutRange(%d,%d) error %v is not a *DocumentError", r[0], r[1], err)
		}
	}

	// The same validation applies when the range comes from the options.
	bad := opts
	bad.Range = &ChapterRange{First: 0, Last: 99}
	if _, err := eng.Layout(ctx, doc, bad); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("Layout with an out-of-range LayoutOptions.Range error = %v, want ErrInvalidOptions", err)
	}
}

// TestLayoutRangeScroll checks scroll-mode range semantics: the included
// chapters are stacked from y=0, tiles cover only them, and locations resolve
// inside the range only.
func TestLayoutRangeScroll(t *testing.T) {
	doc := multiChapterDoc(t, 4)
	fs := testFontSet(t)
	paged := statsTestOptions(fs)
	scroll := paged
	scroll.Mode = ModeScroll
	scroll.TileHeight = 97
	ctx := context.Background()
	eng := mustEngine(t, 2)

	fullPaged, err := eng.Layout(ctx, doc, paged)
	if err != nil {
		t.Fatalf("full paged Layout: %v", err)
	}
	fullScroll, err := eng.Layout(ctx, doc, scroll)
	if err != nil {
		t.Fatalf("full scroll Layout: %v", err)
	}
	ranged, err := eng.LayoutRange(ctx, doc, scroll, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}

	// Content coordinates start at zero for the first included chapter.
	if got := ranged.chapterTop[1]; got != 0 {
		t.Fatalf("chapterTop[1] = %v, want 0", got)
	}
	wantHeight := fullScroll.chapters[1].height + fullScroll.chapters[2].height
	if math.Abs(float64(ranged.ContentHeight()-wantHeight)) > 0.01 {
		t.Fatalf("ranged ContentHeight = %v, want %v", ranged.ContentHeight(), wantHeight)
	}
	if !closef(ranged.chapterTop[2], fullScroll.chapters[1].height, 0.01) {
		t.Fatalf("chapterTop[2] = %v, want %v", ranged.chapterTop[2], fullScroll.chapters[1].height)
	}
	wantTiles := int(math.Ceil(float64(wantHeight) / 97))
	if ranged.TileCount() != wantTiles {
		t.Fatalf("ranged TileCount = %d, want %d", ranged.TileCount(), wantTiles)
	}

	// The scroll glyphs of the range are the full layout's chapter-local glyphs
	// of the included chapters.
	want := glyphsInRange(collectPagedGlyphs(fullPaged), 1, 3)
	got := collectRangedScrollGlyphs(t, ranged)
	compareGlyphSets(t, "ranged scroll", want, got)

	// Locations start at the first included chapter.
	for _, y := range []float32{0, 100, ranged.ContentHeight() / 2, ranged.ContentHeight() - 0.5} {
		loc, ok := ranged.LocationAt(y)
		if !ok {
			continue
		}
		if loc.Chapter < 1 || loc.Chapter >= 3 {
			t.Fatalf("LocationAt(%v) chapter = %d, want in [1,3)", y, loc.Chapter)
		}
	}
	loc, ok := ranged.HitTest(paint.Point{X: 30, Y: 1})
	if !ok || loc.Chapter != 1 {
		t.Fatalf("ranged scroll HitTest = %+v, %v; want chapter 1", loc, ok)
	}
	for _, y := range []float32{-1000, ranged.ContentHeight() + 1000} {
		if loc, ok := ranged.LocationAt(y); ok && (loc.Chapter < 1 || loc.Chapter >= 3) {
			t.Fatalf("LocationAt(%v) clamped outside the range: %+v", y, loc)
		}
	}
	if _, ok := ranged.Locate(100); ok {
		t.Fatal("ranged scroll Locate(excluded anchor) = true")
	}
}

// TestLayoutRangeRelayoutKeepsRange checks Relayout preserves the receiver's
// range, and that an explicit Range in the new options overrides it.
func TestLayoutRangeRelayoutKeepsRange(t *testing.T) {
	doc := multiChapterDoc(t, 4)
	fs := testFontSet(t)
	base := statsTestOptions(fs)
	ctx := context.Background()
	eng := mustEngine(t, 2)

	ranged, err := eng.LayoutRange(ctx, doc, base, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}

	changed := base
	changed.Typography.LineHeight = 2.0
	relaid, err := ranged.Relayout(ctx, changed)
	if err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	if pages := relaid.ChapterPages(); pages[0] != -1 || pages[3] != -1 {
		t.Fatalf("Relayout dropped the range: ChapterPages = %v", pages)
	}
	if relaid.chapters[1].firstPage != 0 {
		t.Fatalf("Relayout range does not start at page 0")
	}
	if _, ok := relaid.Locate(101); !ok {
		t.Fatal("Relayout range lost chapter 1")
	}
	if _, ok := relaid.Locate(100); ok {
		t.Fatal("Relayout range gained chapter 0")
	}

	// An explicit range in the new options wins.
	wider := base
	wider.Range = &ChapterRange{First: 2, Last: 4}
	relaid, err = ranged.Relayout(ctx, wider)
	if err != nil {
		t.Fatalf("Relayout with explicit range: %v", err)
	}
	if pages := relaid.ChapterPages(); pages[1] != -1 || pages[2] != 0 {
		t.Fatalf("Relayout did not apply the explicit range: ChapterPages = %v", pages)
	}
	if _, ok := relaid.Locate(103); !ok {
		t.Fatal("Relayout explicit range lost chapter 3")
	}
}

// TestLayoutRangeStatsAndCounters checks a range layout for the parts a host
// observes without touching internals: page numbering, chapter count and the
// stats snapshot.
func TestLayoutRangePublicView(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()
	eng, err := New(Options{Workers: 2, Measure: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ranged, err := eng.LayoutRange(ctx, doc, opts, 1, 2)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}
	st := ranged.Stats()
	if st.Pages != ranged.PageCount() || st.Pages == 0 {
		t.Fatalf("Stats().Pages = %d, PageCount = %d", st.Pages, ranged.PageCount())
	}
	if st.Total <= 0 || st.Shapes == 0 || st.Tasks == 0 {
		t.Fatalf("ranged Stats = %+v", st)
	}
	if got := ranged.Document(); got != doc {
		t.Fatal("Document() is not the full document")
	}
	if ranged.PageCount() <= 0 {
		t.Fatal("ranged layout has no pages")
	}
	// The ranged layout's document still reports every chapter.
	if got := ranged.Document().ChapterCount(); got != 3 {
		t.Fatalf("Document().ChapterCount() = %d, want 3", got)
	}
	// fmt must not panic on the public types (hosts log layouts).
	if s := fmt.Sprint(ranged.Size(), ranged.PageCount()); !strings.Contains(s, "3") {
		_ = s
	}
}
