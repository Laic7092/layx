package render

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

func scrollOptions(t testing.TB, mode Mode, tileHeight float32) LayoutOptions {
	t.Helper()
	return LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   16,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:      testFontSet(t),
		Margins:    paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
		Mode:       mode,
		TileHeight: tileHeight,
	}
}

func layoutOptions(t testing.TB, e *Engine, doc *Document, opts LayoutOptions) *Layout {
	t.Helper()
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return l
}

func multiChapterDoc(t testing.TB, chapters int) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16,
			Color: paint.Color{A: 0xff}, MarginTop: 12, MarginBottom: 14,
		},
	}
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 12)
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	for c := 0; c < chapters; c++ {
		nodes := []Node{
			{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
			{Kind: NodeText, Text: text},
		}
		anchors := map[AnchorID]NodeID{AnchorID(100 + c): 1}
		if err := b.AddChapter(Chapter{
			ID: fmt.Sprintf("ch%d", c), Root: 0, Nodes: nodes, Anchors: anchors,
		}); err != nil {
			t.Fatalf("AddChapter: %v", err)
		}
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func closef(a, b, eps float32) bool { return float32(math.Abs(float64(a-b))) <= eps }

// locForLineStart returns a Location pointing at the first rune of item idx.
func locForLineStart(cf *chapterFlow, ci, idx int) (Location, bool) {
	itv := cf.item(idx)
	it := &itv
	if it.kind != itemLine || it.para == nil {
		return Location{}, false
	}
	seg := it.para.segAt(int(it.start))
	if seg == nil {
		return Location{}, false
	}
	off := int(it.start) - int(seg.start)
	if off < 0 {
		off = 0
	}
	return Location{
		Chapter: ci,
		Node:    seg.node,
		Anchor:  cf.nodeAnchor[seg.node],
		Offset:  off,
	}, true
}

func TestScrollModeBasics(t *testing.T) {
	doc := testDocument(t, "")
	e := mustEngine(t, 4)
	base := scrollOptions(t, ModePaged, 0)
	paged := layoutOptions(t, e, doc, base)

	scrollOpts := base
	scrollOpts.Mode = ModeScroll
	scrollOpts.TileHeight = 100
	scroll := layoutOptions(t, e, doc, scrollOpts)

	if scroll.PageCount() != 0 {
		t.Fatalf("scroll PageCount = %d, want 0", scroll.PageCount())
	}
	if p := scroll.Page(0); p != nil {
		t.Fatalf("scroll Page(0) = %p, want nil", p)
	}
	if scroll.ContentHeight() <= 0 {
		t.Fatalf("ContentHeight = %v", scroll.ContentHeight())
	}
	wantTiles := int(math.Ceil(float64(scroll.ContentHeight()) / 100))
	if scroll.TileCount() != wantTiles {
		t.Fatalf("TileCount = %d, want %d", scroll.TileCount(), wantTiles)
	}
	if got := len(scroll.Tiles(0, 3)); got != 3 {
		t.Fatalf("Tiles(0,3) = %d tiles", got)
	}
	if got := len(scroll.Tiles(-5, 2)); got != 2 {
		t.Fatalf("Tiles(-5,2) = %d tiles, want 2", got)
	}
	if got := scroll.Tiles(2, 1); len(got) != 0 {
		t.Fatalf("Tiles(2,1) = %d tiles, want none", len(got))
	}
	if got := scroll.Tiles(0, 10_000); len(got) != scroll.TileCount() {
		t.Fatalf("Tiles clamped to %d, want %d", len(got), scroll.TileCount())
	}
	if scroll.Tile(-1) != nil || scroll.Tile(scroll.TileCount()) != nil {
		t.Fatal("out-of-range Tile did not return nil")
	}

	t0 := scroll.Tile(0)
	if t0 == nil {
		t.Fatal("Tile(0) is nil")
	}
	if again := scroll.Tile(0); again != t0 {
		t.Fatal("repeated Tile(0) returned a different pointer")
	}
	if t0.Top() != 0 || t0.Height() != 100 {
		t.Fatalf("tile 0 top/height = %v/%v, want 0/100", t0.Top(), t0.Height())
	}
	rp := &recordPainter{}
	t0.Draw(rp)
	if rp.saves != 1 || rp.restores != 1 {
		t.Fatalf("tile save/restore = %d/%d", rp.saves, rp.restores)
	}
	if rp.glyphs == 0 {
		t.Fatal("first tile drew no glyphs")
	}

	// Paged layouts do not expose scroll surfaces.
	if paged.ContentHeight() != 0 || paged.TileCount() != 0 {
		t.Fatalf("paged ContentHeight/TileCount = %v/%v, want 0/0",
			paged.ContentHeight(), paged.TileCount())
	}
	if paged.Tile(0) != nil || paged.Tiles(0, 1) != nil {
		t.Fatal("paged layout exposed scroll tiles")
	}
}

func TestScrollTileHeightDefaultsToViewport(t *testing.T) {
	doc := testDocument(t, "")
	opts := scrollOptions(t, ModeScroll, 0)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)
	if l.TileCount() == 0 {
		t.Fatal("no tiles")
	}
	want := int(math.Ceil(float64(l.ContentHeight()) / float64(opts.Viewport.Height)))
	if l.TileCount() != want {
		t.Fatalf("TileCount with default height = %d, want %d", l.TileCount(), want)
	}
	if got := l.Tile(1).Top(); got != opts.Viewport.Height {
		t.Fatalf("tile 1 top = %v, want %v", got, opts.Viewport.Height)
	}
}

func TestScrollChapterContinuity(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	opts := scrollOptions(t, ModeScroll, 120)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)

	if len(l.chapterTop) != doc.ChapterCount() {
		t.Fatalf("chapterTop count = %d", len(l.chapterTop))
	}
	if l.chapterTop[0] != 0 {
		t.Fatalf("first chapter top = %v, want 0", l.chapterTop[0])
	}
	var total float32
	for ci := range l.chapters {
		if l.chapterTop[ci] != total {
			t.Fatalf("chapter %d top = %v, want %v", ci, l.chapterTop[ci], total)
		}
		total += l.chapters[ci].height
	}
	if !closef(l.ContentHeight(), total, 0.001) {
		t.Fatalf("ContentHeight = %v, want %v", l.ContentHeight(), total)
	}

	// Page top margins are viewport insets, not content: the body starts at
	// content y=0 and each chapter keeps only its own block margins.
	first := itemsOf(&l.chapters[0])[0]
	y0, ok := l.OffsetOf(Location{Chapter: 0, Node: first.para.segs[0].node})
	if !ok {
		t.Fatal("OffsetOf first line failed")
	}
	if !closef(y0, first.y, 0.01) {
		t.Fatalf("first line content y = %v, want %v (page Margins.Top must not be re-applied)", y0, first.y)
	}

	for ci := range l.chapters {
		cf := &l.chapters[ci]
		if cf.count() == 0 {
			continue
		}
		loc, ok := locForLineStart(cf, ci, 0)
		if !ok {
			t.Fatalf("chapter %d: no line location", ci)
		}
		got, ok := l.OffsetOf(loc)
		if !ok {
			t.Fatalf("chapter %d: OffsetOf failed", ci)
		}
		want := l.chapterTop[ci] + itemsOf(cf)[0].y
		if !closef(got, want, 0.01) {
			t.Fatalf("chapter %d first line y = %v, want %v", ci, got, want)
		}
	}
}

type contentGlyph struct {
	chapter int
	x, y    float32
	id      uint32
	cluster int32
}

func collectPagedGlyphs(l *Layout) []contentGlyph {
	var out []contentGlyph
	for pn := 0; pn < l.PageCount(); pn++ {
		info := &l.pages[pn]
		pp := &posPainter{}
		l.Page(pn).Draw(pp)
		for _, run := range pp.runs {
			for _, g := range run.Glyphs {
				out = append(out, contentGlyph{
					chapter: info.chapter,
					x:       run.X - l.opts.Margins.Left + g.X,
					y:       run.Y - l.opts.Margins.Top + info.top + g.Y,
					id:      g.ID,
					cluster: g.Cluster,
				})
			}
		}
	}
	return out
}

func (l *Layout) chapterForContentY(y float32) int {
	if len(l.chapterTop) == 0 {
		return 0
	}
	j := sort.Search(len(l.chapterTop), func(i int) bool { return l.chapterTop[i] > y }) - 1
	if j < 0 {
		return 0
	}
	return j
}

func collectTileGlyphs(l *Layout) []contentGlyph {
	var out []contentGlyph
	for i := 0; i < l.TileCount(); i++ {
		tile := l.Tile(i)
		if tile == nil {
			panic("nil tile")
		}
		pp := &posPainter{}
		tile.Draw(pp)
		for _, run := range pp.runs {
			for _, g := range run.Glyphs {
				globalY := run.Y + tile.Top() + g.Y
				ci := l.chapterForContentY(globalY)
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

func quant(v float32) int { return int(math.Round(float64(v) * 100)) }

func sortContentGlyphs(g []contentGlyph) {
	sort.Slice(g, func(i, j int) bool {
		a, b := g[i], g[j]
		if a.chapter != b.chapter {
			return a.chapter < b.chapter
		}
		if qa, qb := quant(a.y), quant(b.y); qa != qb {
			return qa < qb
		}
		if qa, qb := quant(a.x), quant(b.x); qa != qb {
			return qa < qb
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.cluster < b.cluster
	})
}

func compareGlyphSets(t *testing.T, what string, paged, scrolled []contentGlyph) {
	t.Helper()
	if len(paged) != len(scrolled) {
		t.Fatalf("%s: glyph count = %d, want %d", what, len(scrolled), len(paged))
	}
	sortContentGlyphs(paged)
	sortContentGlyphs(scrolled)
	for i := range paged {
		a, b := paged[i], scrolled[i]
		if a.chapter != b.chapter || a.id != b.id || a.cluster != b.cluster {
			t.Fatalf("%s: glyph %d = %+v, want %+v", what, i, b, a)
		}
		if !closef(a.x, b.x, 0.011) || !closef(a.y, b.y, 0.011) {
			t.Fatalf("%s: glyph %d position = (%.4f,%.4f), want (%.4f,%.4f)",
				what, i, b.x, b.y, a.x, a.y)
		}
	}
}

func TestScrollGlyphsMatchPaged(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	base := scrollOptions(t, ModePaged, 0)
	paged := layoutOptions(t, mustEngine(t, 2), doc, base)

	opts := base
	opts.Mode = ModeScroll
	opts.TileHeight = 97 // prime-ish, so tiles cut across lines and paragraphs
	scroll := layoutOptions(t, mustEngine(t, 2), doc, opts)

	compareGlyphSets(t, "scroll glyphs", collectPagedGlyphs(paged), collectTileGlyphs(scroll))
}

func TestScrollTileBoundaries(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	base := scrollOptions(t, ModePaged, 0)
	paged := layoutOptions(t, mustEngine(t, 2), doc, base)
	want := collectPagedGlyphs(paged)

	for _, height := range []float32{97, 37, 5} {
		opts := base
		opts.Mode = ModeScroll
		opts.TileHeight = height
		l := layoutOptions(t, mustEngine(t, 2), doc, opts)
		if l.TileCount() == 0 {
			t.Fatalf("height %v: no tiles", height)
		}
		// Every line is assigned to exactly one tile, so the total set of
		// glyphs (count and content coordinates) is unchanged.
		compareGlyphSets(t, fmt.Sprintf("tile height %v", height), append([]contentGlyph(nil), want...), collectTileGlyphs(l))
		for i := 0; i < l.TileCount(); i++ {
			tile := l.Tile(i)
			if tile == nil {
				t.Fatalf("height %v: tile %d is nil", height, i)
			}
			if tile.Height() != height {
				t.Fatalf("height %v: tile %d height = %v", height, i, tile.Height())
			}
			if tile.Top() != float32(i)*height {
				t.Fatalf("height %v: tile %d top = %v", height, i, tile.Top())
			}
		}
	}
}

func TestPositionRoundTripBothModes(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	for _, mode := range []Mode{ModePaged, ModeScroll} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			opts := scrollOptions(t, mode, 113)
			l := layoutOptions(t, mustEngine(t, 2), doc, opts)
			if l.PageCount() == 0 && mode == ModePaged {
				t.Fatal("paged layout has no pages")
			}
			if l.TileCount() == 0 && mode == ModeScroll {
				t.Fatal("scroll layout has no tiles")
			}
			for ci := range l.chapters {
				cf := &l.chapters[ci]
				items := itemsOf(cf)
				for idx := 0; idx < len(items); idx++ {
					if items[idx].kind != itemLine {
						continue
					}
					loc, ok := locForLineStart(cf, ci, idx)
					if !ok {
						continue
					}
					y, ok := l.OffsetOf(loc)
					if !ok {
						t.Fatalf("chapter %d item %d: OffsetOf failed", ci, idx)
					}
					got, ok := l.LocationAt(y)
					if !ok {
						t.Fatalf("chapter %d item %d: LocationAt(%v) failed", ci, idx, y)
					}
					y2, ok := l.OffsetOf(got)
					if !ok {
						t.Fatalf("chapter %d item %d: OffsetOf round trip failed", ci, idx)
					}
					if !closef(y, y2, 0.01) {
						t.Fatalf("chapter %d item %d: y %.6f round-tripped to %.6f", ci, idx, y, y2)
					}
				}
			}
		})
	}
}

func TestScrollPositionStableAfterRelayout(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	opts := scrollOptions(t, ModeScroll, 113)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)

	type sample struct {
		ci   int
		idx  int
		loc  Location
		text string
	}
	var samples []sample
	for ci := range l.chapters {
		cf := &l.chapters[ci]
		for idx := 0; idx < cf.count(); idx += 2 {
			itv := cf.item(idx)
			it := &itv
			if it.kind != itemLine {
				continue
			}
			loc, ok := locForLineStart(cf, ci, idx)
			if !ok {
				continue
			}
			samples = append(samples, sample{
				ci: ci, idx: idx, loc: loc,
				text: string(paraRunes(it.para)[it.start:it.end]),
			})
		}
	}
	if len(samples) < 8 {
		t.Fatalf("only %d samples", len(samples))
	}

	next := opts
	next.Typography.LineHeight = 2.2
	l2, err := l.Relayout(context.Background(), next)
	if err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	if l2.ContentHeight() == l.ContentHeight() {
		t.Fatal("line-height relayout did not change content height")
	}
	for _, s := range samples {
		y, ok := l2.OffsetOf(s.loc)
		if !ok {
			t.Fatalf("sample chapter %d item %d: OffsetOf after relayout failed", s.ci, s.idx)
		}
		cf := &l2.chapters[s.ci]
		idx, ok := cf.itemOf(s.loc)
		if !ok {
			t.Fatalf("sample chapter %d item %d: itemOf after relayout failed", s.ci, s.idx)
		}
		itv := cf.item(idx)
		it := &itv
		if it.kind != itemLine {
			t.Fatalf("sample chapter %d: relayout landed on non-line item", s.ci)
		}
		if got := string(paraRunes(it.para)[it.start:it.end]); got != s.text {
			t.Fatalf("sample chapter %d: relayout landed on line %q, want %q", s.ci, got, s.text)
		}
		want := l2.chapterTop[s.ci] + it.y
		if !closef(y, want, 0.01) {
			t.Fatalf("sample chapter %d: y = %v, want line top %v", s.ci, y, want)
		}
	}
}

func TestScrollHitTest(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	opts := scrollOptions(t, ModeScroll, 113)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)

	// Pick a line in the second chapter and hit the middle of its first glyph.
	cf := &l.chapters[1]
	var idx = -1
	for i := 0; i < cf.count(); i++ {
		if cf.item(i).kind == itemLine {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no line in second chapter")
	}
	itv := cf.item(idx)
	it := &itv
	y := l.chapterTop[1] + it.y + it.height/2
	x := it.x + 1
	loc, ok := l.HitTest(x, y)
	if !ok {
		t.Fatal("HitTest failed")
	}
	if loc.Chapter != 1 {
		t.Fatalf("HitTest chapter = %d, want 1", loc.Chapter)
	}
	if got, ok := l.OffsetOf(loc); !ok || !closef(got, l.chapterTop[1]+it.y, 0.01) {
		t.Fatalf("HitTest location maps to y %v, want %v", got, l.chapterTop[1]+it.y)
	}
	if _, ok := l.HitTest(-5000, -5000); !ok {
		t.Fatal("HitTest far outside content should clamp to the first item")
	}
}

func TestScrollTileLinks(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeImage, Image: 7, Width: 200, Height: 100, Link: "https://example.com/a"},
		{Kind: NodeText, Text: strings.Repeat("link text ", 30), Link: "https://example.com/b"},
	}
	styles := []Style{{}, {FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{A: 0xff}}}
	b := NewDocumentBuilder().SetStyles(styles)
	if err := b.AddChapter(Chapter{ID: "ch", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	opts := scrollOptions(t, ModeScroll, 80)
	opts.Typography.FontFamily = "Test Serif"
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)

	found := map[string]bool{}
	for i := 0; i < l.TileCount(); i++ {
		pp := &recordPainter{}
		tile := l.Tile(i)
		tile.Draw(pp)
		if pp.images == 0 && pp.glyphs == 0 {
			continue
		}
		for _, link := range tile.Links() {
			found[link.URL] = true
			if link.Rect.Width <= 0 || link.Rect.Height <= 0 {
				t.Fatalf("tile %d link has empty rect: %+v", i, link)
			}
		}
	}
	if !found["https://example.com/a"] || !found["https://example.com/b"] {
		t.Fatalf("missing links: %v", found)
	}
}

func TestTileCacheEviction(t *testing.T) {
	doc := testDocument(t, "")
	e, err := New(Options{Workers: 2, PageCache: 2, TileCache: 2, LineCache: 64})
	if err != nil {
		t.Fatal(err)
	}
	opts := scrollOptions(t, ModeScroll, 80)
	l := layoutOptions(t, e, doc, opts)

	t0 := l.Tile(0)
	if t0 == nil {
		t.Fatal("Tile(0) nil")
	}
	l.Tile(1)
	l.Tile(2)
	l.tileMu.Lock()
	n := len(l.tileCache)
	l.tileMu.Unlock()
	if n > 2 {
		t.Fatalf("tile cache size = %d, want <= 2", n)
	}
	t0b := l.Tile(0)
	if t0b == t0 {
		t.Fatal("evicted tile was not rebuilt")
	}
	// A rebuilt tile must produce the same display list.
	a := &posPainter{}
	b := &posPainter{}
	t0.Draw(a)
	t0b.Draw(b)
	if len(a.runs) != len(b.runs) {
		t.Fatalf("rebuilt tile run count = %d, want %d", len(b.runs), len(a.runs))
	}
	for i := range a.runs {
		if len(a.runs[i].Glyphs) != len(b.runs[i].Glyphs) ||
			a.runs[i].X != b.runs[i].X || a.runs[i].Y != b.runs[i].Y {
			t.Fatalf("rebuilt tile run %d differs", i)
		}
		for j := range a.runs[i].Glyphs {
			ga, gb := a.runs[i].Glyphs[j], b.runs[i].Glyphs[j]
			if ga.ID != gb.ID || ga.X != gb.X || ga.Y != gb.Y || ga.Advance != gb.Advance {
				t.Fatalf("rebuilt tile glyph %d/%d differs: %+v vs %+v", i, j, gb, ga)
			}
		}
	}
	if lines := l.lineCache.size(); lines > 64 {
		t.Fatalf("line cache size = %d, want <= 64", lines)
	}
}

func TestLineCacheAvoidsReshaping(t *testing.T) {
	doc := testDocument(t, "")
	e, err := New(Options{Workers: 2, PageCache: 2, TileCache: 1, LineCache: 256})
	if err != nil {
		t.Fatal(err)
	}
	opts := scrollOptions(t, ModeScroll, 80)
	l := layoutOptions(t, e, doc, opts)

	before := e.shapes.Load()
	t0 := l.Tile(0)
	if t0 == nil {
		t.Fatal("Tile(0) nil")
	}
	if got := e.shapes.Load() - before; got == 0 {
		t.Fatal("cold tile did not shape anything")
	}

	// Evict tile 0 from the tile cache. All of its lines still fit in the line
	// cache, so rebuilding it must not call the shaper again.
	l.Tile(1)
	l.Tile(2)
	before = e.shapes.Load()
	again := l.Tile(0)
	if got := e.shapes.Load() - before; got != 0 {
		t.Fatalf("rebuilt tile shaped %d runs, want 0 (line cache miss)", got)
	}
	if again == nil {
		t.Fatal("rebuilt Tile(0) is nil")
	}
}

func TestLineCacheBounded(t *testing.T) {
	doc := testDocument(t, "")
	e, err := New(Options{Workers: 2, TileCache: 8, LineCache: 2})
	if err != nil {
		t.Fatal(err)
	}
	opts := scrollOptions(t, ModeScroll, 60)
	l := layoutOptions(t, e, doc, opts)
	for i := 0; i < l.TileCount() && i < 10; i++ {
		l.Tile(i)
	}
	if n := l.lineCache.size(); n > 2 {
		t.Fatalf("line cache size = %d, want <= 2", n)
	}
}

func TestScrollEmptyChapter(t *testing.T) {
	nodes := []Node{{Kind: NodeBlock}}
	b := NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{ID: "empty", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	opts := scrollOptions(t, ModeScroll, 100)
	l := layoutOptions(t, mustEngine(t, 1), doc, opts)
	if l.ContentHeight() != 0 || l.TileCount() != 0 {
		t.Fatalf("empty chapter content/tiles = %v/%v", l.ContentHeight(), l.TileCount())
	}
	if l.Tile(0) != nil {
		t.Fatal("empty chapter has a tile")
	}
	if _, ok := l.LocationAt(0); ok {
		t.Fatal("LocationAt on empty chapter succeeded")
	}
}

func TestLayoutOptionValidation(t *testing.T) {
	doc := testDocument(t, "")
	base := scrollOptions(t, ModePaged, 0)
	e := mustEngine(t, 1)

	bad := base
	bad.Mode = Mode(9)
	if _, err := e.Layout(context.Background(), doc, bad); err == nil {
		t.Fatal("unknown mode accepted")
	}

	bad = base
	bad.TileHeight = -1
	if _, err := e.Layout(context.Background(), doc, bad); err == nil {
		t.Fatal("negative tile height accepted")
	}

	if _, err := New(Options{TileCache: -1}); err == nil {
		t.Fatal("negative tile cache accepted")
	}
	if _, err := New(Options{LineCache: -1}); err == nil {
		t.Fatal("negative line cache accepted")
	}
}

func TestScrollSharesPagedFlow(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	base := scrollOptions(t, ModePaged, 0)
	e := mustEngine(t, 2)
	paged := layoutOptions(t, e, doc, base)

	opts := base
	opts.Mode = ModeScroll
	opts.TileHeight = 113
	before := e.shapes.Load()
	scroll, err := paged.Relayout(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.shapes.Load() - before; got != 0 {
		t.Fatalf("mode-only relayout shaped %d runs, want 0", got)
	}
	if len(scroll.chapters) != len(paged.chapters) {
		t.Fatal("chapter count changed")
	}
	for ci := range paged.chapters {
		a := &paged.chapters[ci]
		b := &scroll.chapters[ci]
		if a.count() != b.count() {
			t.Fatalf("chapter %d item count = %d, want %d", ci, b.count(), a.count())
		}
		ai, bi := itemsOf(a), itemsOf(b)
		for i := range ai {
			x, y := ai[i], bi[i]
			if x.kind != y.kind || x.start != y.start || x.end != y.end ||
				x.x != y.x || x.y != y.y || x.width != y.width || x.height != y.height {
				t.Fatalf("chapter %d item %d differs between modes", ci, i)
			}
		}
	}
	if scroll.PageCount() != 0 || paged.PageCount() == 0 {
		t.Fatalf("mode page counts = scroll %d, paged %d", scroll.PageCount(), paged.PageCount())
	}
}
