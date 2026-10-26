package render

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

// locForParaRune returns a Location pointing at absolute paragraph rune pos.
func locForParaRune(cf *chapterFlow, ci, pos int) (Location, bool) {
	items := itemsOf(cf)
	for idx := range items {
		it := &items[idx]
		if it.para == nil || pos < int(it.start) || pos >= int(it.end) {
			continue
		}
		seg := it.para.segAt(pos)
		if seg == nil {
			return Location{}, false
		}
		return Location{
			Chapter: ci,
			Node:    seg.node,
			Anchor:  cf.nodeAnchor[seg.node],
			Offset:  pos - int(seg.start),
		}, true
	}
	return Location{}, false
}

// TestSelectionQuadsMatchFlow pins both coordinate systems of every quad to the
// flow item it covers. It covers the whole book, so the quad sequence is exactly
// the flow item sequence, and nothing about page-local offsets or content
// offsets can drift without breaking it.
func TestSelectionQuadsMatchFlow(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	base := scrollOptions(t, ModePaged, 0)
	e := mustEngine(t, 2)
	layouts := make(map[Mode]*Layout, 2)
	for _, mode := range []Mode{ModePaged, ModeScroll} {
		opts := base
		opts.Mode = mode
		opts.TileHeight = 113
		layouts[mode] = layoutOptions(t, e, doc, opts)
	}
	paged, scroll := layouts[ModePaged], layouts[ModeScroll]
	pageH := contentHeight(base)

	firstCF := &paged.chapters[0]
	lastCF := &paged.chapters[len(paged.chapters)-1]
	from, ok := locForParaRune(firstCF, 0, int(itemsOf(firstCF)[0].start))
	if !ok {
		t.Fatal("no location for the first rune")
	}
	lastIt := itemsOf(lastCF)[lastCF.count()-1]
	last := &lastIt
	to, ok := locForParaRune(lastCF, len(paged.chapters)-1, int(last.end)-1)
	if !ok {
		t.Fatal("no location for the last rune")
	}

	type wantQuad struct {
		ci   int
		item *flowItem
		page int
	}
	var want []wantQuad
	for ci := range paged.chapters {
		cf := &paged.chapters[ci]
		items := itemsOf(cf)
		for idx := range items {
			it := &items[idx]
			if it.kind != itemLine {
				continue
			}
			page := cf.pageOfItem(idx)
			if page < 0 {
				continue
			}
			want = append(want, wantQuad{ci: ci, item: it, page: page})
		}
	}

	pq := paged.Selection(from, to)
	sq := scroll.Selection(from, to)
	if len(pq) != len(want) {
		t.Fatalf("paged quads = %d, want one per flow item (%d)", len(pq), len(want))
	}
	if len(sq) != len(want) {
		t.Fatalf("scroll quads = %d, want one per flow item (%d)", len(sq), len(want))
	}

	for i := range want {
		w := want[i]
		a, b := pq[i], sq[i]
		cf := &paged.chapters[w.ci]
		pageTop := cf.pages[w.page].top
		pageLocal := base.Margins.Top + w.item.y - pageTop
		contentY := float32(cf.firstPage+w.page)*pageH + (w.item.y - pageTop)

		if a.Page != cf.firstPage+w.page {
			t.Fatalf("quad %d: paged Page = %d, want %d", i, a.Page, cf.firstPage+w.page)
		}
		if b.Page != -1 {
			t.Fatalf("quad %d: scroll Page = %d, want -1", i, b.Page)
		}
		// Rect is page-local in both modes, and unchanged by the new fields.
		for _, q := range []SelectionQuad{a, b} {
			if !closef(q.Rect.Y, pageLocal, 0.01) {
				t.Fatalf("quad %d: Rect.Y = %v, want %v", i, q.Rect.Y, pageLocal)
			}
			if !closef(q.Rect.Height, w.item.height, 0.01) {
				t.Fatalf("quad %d: Rect.Height = %v, want %v", i, q.Rect.Height, w.item.height)
			}
			if !closef(q.Content.X, q.Rect.X, 0.01) || q.Content.Width != q.Rect.Width {
				t.Fatalf("quad %d: Content.X/Width = %v/%v, want %v/%v",
					i, q.Content.X, q.Content.Width, q.Rect.X, q.Rect.Width)
			}
			if !closef(q.Content.Height, q.Rect.Height, 0.01) {
				t.Fatalf("quad %d: Content.Height = %v, want %v", i, q.Content.Height, q.Rect.Height)
			}
			// A selection covering a whole line can include its trailing space,
			// which line breaking trimmed from the measured width.
			left := base.Margins.Left + w.item.x
			if q.Rect.X < left-0.5 || q.Rect.X+q.Rect.Width > left+w.item.avail+24 {
				t.Fatalf("quad %d: Rect.X=[%v,%v] outside line box [%v,%v]",
					i, q.Rect.X, q.Rect.X+q.Rect.Width, left, left+w.item.avail)
			}
		}
		if !closef(a.Content.Y, contentY, 0.01) {
			t.Fatalf("quad %d: paged Content.Y = %v, want page slot offset %v", i, a.Content.Y, contentY)
		}
		scrollY := scroll.chapterTop[w.ci] + w.item.y
		if !closef(b.Content.Y, scrollY, 0.01) {
			t.Fatalf("quad %d: scroll Content.Y = %v, want %v", i, b.Content.Y, scrollY)
		}
		if b.Content.Y < 0 || b.Content.Y > scroll.ContentHeight() {
			t.Fatalf("quad %d: scroll Content.Y = %v outside [0,%v]",
				i, b.Content.Y, scroll.ContentHeight())
		}
		// The top edge of the quad lies in the tile a host would draw it on.
		tile := scroll.Tile(int(b.Content.Y / scroll.Tile(0).Height()))
		if tile == nil {
			t.Fatalf("quad %d: no tile for content y %v", i, b.Content.Y)
		}
		if b.Content.Y < tile.Top() || b.Content.Y >= tile.Top()+tile.Height() {
			t.Fatalf("quad %d: content y %v outside tile [%v,%v)",
				i, b.Content.Y, tile.Top(), tile.Top()+tile.Height())
		}
		if i > 0 {
			if a.Content.Y < pq[i-1].Content.Y-0.01 {
				t.Fatalf("quad %d: paged content y went backwards (%v < %v)",
					i, a.Content.Y, pq[i-1].Content.Y)
			}
			if b.Content.Y < sq[i-1].Content.Y-0.01 {
				t.Fatalf("quad %d: scroll content y went backwards (%v < %v)",
					i, b.Content.Y, sq[i-1].Content.Y)
			}
		}
	}
}

// TestSelectionContentMatchesOffsetOf checks the documented contract: the
// content rect of a single-line selection starts where OffsetOf reports.
func TestSelectionContentMatchesOffsetOf(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	for _, mode := range []Mode{ModePaged, ModeScroll} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			opts := scrollOptions(t, mode, 113)
			l := layoutOptions(t, mustEngine(t, 2), doc, opts)
			checked := 0
			for ci := range l.chapters {
				cf := &l.chapters[ci]
				for idx := 0; idx < cf.count(); idx += 2 {
					from, ok := locForLineStart(cf, ci, idx)
					if !ok {
						continue
					}
					to := from
					to.Offset++
					quads := l.Selection(from, to)
					if len(quads) != 1 {
						continue
					}
					q := quads[0]
					y, ok := l.OffsetOf(from)
					if !ok {
						t.Fatalf("chapter %d item %d: OffsetOf failed", ci, idx)
					}
					if !closef(q.Content.Y, y, 0.01) {
						t.Fatalf("chapter %d item %d: Content.Y = %v, want OffsetOf %v",
							ci, idx, q.Content.Y, y)
					}
					if !closef(q.Content.X, q.Rect.X, 0.01) {
						t.Fatalf("chapter %d item %d: Content.X = %v, want Rect.X %v",
							ci, idx, q.Content.X, q.Rect.X)
					}
					// Content.Y must live on the LocationAt axis, or a host
					// could not turn a quad back into a stable Location.
					at, ok := l.LocationAt(q.Content.Y)
					if !ok {
						t.Fatalf("chapter %d item %d: LocationAt(%v) failed", ci, idx, q.Content.Y)
					}
					if y2, ok := l.OffsetOf(at); !ok || !closef(y2, q.Content.Y, 0.01) {
						t.Fatalf("chapter %d item %d: Content.Y %v round-tripped to %v (%v)",
							ci, idx, q.Content.Y, y2, ok)
					}
					if mode == ModePaged {
						page, ok := l.PageOf(from)
						if !ok || page != q.Page {
							t.Fatalf("chapter %d item %d: Page = %d, want PageOf %d (%v)",
								ci, idx, q.Page, page, ok)
						}
					} else if q.Page != -1 {
						t.Fatalf("chapter %d item %d: scroll Page = %d, want -1", ci, idx, q.Page)
					}
					checked++
				}
			}
			if checked < 8 {
				t.Fatalf("only %d single-line selections checked", checked)
			}
		})
	}
}

// TestSelectionScrollTiles verifies the scroll-mode workflow a host performs:
// translate Content into the tile display list coordinates and get the same
// intersection the tile already draws.
func TestSelectionScrollTiles(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	opts := scrollOptions(t, ModeScroll, 113)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)

	cf := &l.chapters[1]
	from, ok := locForLineStart(cf, 1, 0)
	if !ok {
		t.Fatal("no line in second chapter")
	}
	to, ok := locForParaRune(cf, 1, int(itemsOf(cf)[0].end)-1)
	if !ok {
		t.Fatal("no rune location in second chapter")
	}
	quads := l.Selection(from, to)
	if len(quads) == 0 {
		t.Fatal("selection returned no quads")
	}
	for _, q := range quads {
		if q.Page != -1 {
			t.Fatalf("scroll quad Page = %d, want -1", q.Page)
		}
		// Content.Y is the quad's top in book content coordinates; subtracting
		// a tile's top gives the tile-local y a painter expects.
		tile := l.Tile(int(q.Content.Y / 113))
		if tile == nil {
			t.Fatalf("no tile at content y %v", q.Content.Y)
		}
		local := paint.Rect{
			X: q.Content.X, Y: q.Content.Y - tile.Top(),
			Width: q.Content.Width, Height: q.Content.Height,
		}
		if local.Y < 0 || local.Y > tile.Height() {
			t.Fatalf("tile-local selection y = %v, want [0,%v]", local.Y, tile.Height())
		}
		// The tile's own display list places the same line at the same y.
		wantY := paint.Rect{X: q.Content.X, Y: local.Y, Width: local.Width, Height: local.Height}
		if wantY.Y+wantY.Height > tile.Height()+local.Height {
			t.Fatalf("selection quad sticks out of the tile: %+v", wantY)
		}
	}
}

func TestChapterPageRanges(t *testing.T) {
	const chapters = 3
	doc := multiChapterDoc(t, chapters)
	base := scrollOptions(t, ModePaged, 0)
	// A large line height makes every chapter span several pages, so a range of
	// exactly one page would not prove much.
	base.Typography.LineHeight = 4
	e := mustEngine(t, 2)
	paged := layoutOptions(t, e, doc, base)

	pages := paged.ChapterPages()
	if len(pages) != chapters {
		t.Fatalf("ChapterPages = %v, want %d entries", pages, chapters)
	}
	next := 0
	multi := false
	for ci := 0; ci < chapters; ci++ {
		first, count, ok := paged.PageRange(ci)
		if !ok {
			t.Fatalf("chapter %d: PageRange reported unavailable", ci)
		}
		if first != pages[ci] {
			t.Fatalf("chapter %d: PageRange first = %d, ChapterPages = %d", ci, first, pages[ci])
		}
		if first != next {
			t.Fatalf("chapter %d: first page = %d, want %d (ranges must tile)", ci, first, next)
		}
		if count < 1 {
			t.Fatalf("chapter %d: page count = %d, want >= 1", ci, count)
		}
		if count > 1 {
			multi = true
		}
		cf := &paged.chapters[ci]
		if cf.count() == 0 {
			t.Fatalf("chapter %d: fixture has no flow", ci)
		}
		firstLoc, ok := locForLineStart(cf, ci, 0)
		if !ok {
			t.Fatalf("chapter %d: no first line location", ci)
		}
		if p, ok := paged.PageOf(firstLoc); !ok || p != first {
			t.Fatalf("chapter %d: first line page = %d (%v), want %d", ci, p, ok, first)
		}
		lastIt := itemsOf(cf)[cf.count()-1]
		lastItem := &lastIt
		lastLoc, ok := locForParaRune(cf, ci, int(lastItem.end)-1)
		if !ok {
			t.Fatalf("chapter %d: no last rune location", ci)
		}
		if p, ok := paged.PageOf(lastLoc); !ok || p != first+count-1 {
			t.Fatalf("chapter %d: last line page = %d (%v), want %d", ci, p, ok, first+count-1)
		}
		next = first + count
	}
	if !multi {
		t.Fatal("fixture did not produce a multi-page chapter")
	}
	if next != paged.PageCount() {
		t.Fatalf("ranges end at %d, PageCount = %d", next, paged.PageCount())
	}
	for _, bad := range []int{-1, chapters, chapters + 7} {
		if _, _, ok := paged.PageRange(bad); ok {
			t.Fatalf("PageRange(%d) reported ok", bad)
		}
	}
	if paged.ChapterPages()[0] != 0 {
		t.Fatalf("chapter 0 starts at page %d, want 0", paged.ChapterPages()[0])
	}

	scrollOpts := base
	scrollOpts.Mode = ModeScroll
	scrollOpts.TileHeight = 113
	scroll := layoutOptions(t, e, doc, scrollOpts)
	if got := scroll.ChapterPages(); got != nil {
		t.Fatalf("scroll ChapterPages = %v, want nil", got)
	}
	for ci := 0; ci < chapters; ci++ {
		if _, _, ok := scroll.PageRange(ci); ok {
			t.Fatalf("scroll PageRange(%d) reported ok", ci)
		}
	}
}

func TestChapterContentTop(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	base := scrollOptions(t, ModePaged, 0)
	base.Typography.FontSize = 24
	base.Typography.LineHeight = 2

	for _, mode := range []Mode{ModePaged, ModeScroll} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			opts := base
			opts.Mode = mode
			opts.TileHeight = 113
			l := layoutOptions(t, mustEngine(t, 2), doc, opts)
			pageH := contentHeight(opts)

			var prev float32
			for ci := range l.chapters {
				top, ok := l.ChapterContentTop(ci)
				if !ok {
					t.Fatalf("chapter %d: ChapterContentTop unavailable", ci)
				}
				if ci == 0 && top != 0 {
					t.Fatalf("chapter 0 top = %v, want 0", top)
				}
				if ci > 0 && top <= prev {
					t.Fatalf("chapter %d top = %v, want > %v", ci, top, prev)
				}
				prev = top
				if mode == ModeScroll {
					if !closef(top, l.chapterTop[ci], 0.001) {
						t.Fatalf("chapter %d top = %v, want chapterTop %v", ci, top, l.chapterTop[ci])
					}
				} else {
					first, _, ok := l.PageRange(ci)
					if !ok {
						t.Fatalf("chapter %d: PageRange unavailable", ci)
					}
					if !closef(top, float32(first)*pageH, 0.001) {
						t.Fatalf("chapter %d top = %v, want %v", ci, top, float32(first)*pageH)
					}
				}
				// The chapter's first line is at or after its content top: the
				// root block margin belongs to the chapter.
				cf := &l.chapters[ci]
				loc, ok := locForLineStart(cf, ci, 0)
				if !ok {
					t.Fatalf("chapter %d: no line location", ci)
				}
				y, ok := l.OffsetOf(loc)
				if !ok {
					t.Fatalf("chapter %d: OffsetOf failed", ci)
				}
				if y < top-0.01 {
					t.Fatalf("chapter %d first line y = %v, want >= content top %v", ci, y, top)
				}
			}
			if _, ok := l.ChapterContentTop(-1); ok {
				t.Fatal("ChapterContentTop(-1) reported ok")
			}
			if _, ok := l.ChapterContentTop(len(l.chapters)); ok {
				t.Fatal("ChapterContentTop out of range reported ok")
			}
		})
	}
}

// progressTotal is the denominator Progress uses for a layout: page slots for
// paged layouts, the continuous stream for scroll layouts.
func progressTotal(l *Layout, opts LayoutOptions) float32 {
	if l.opts.Mode == ModeScroll {
		return l.ContentHeight()
	}
	return float32(l.PageCount()) * contentHeight(opts)
}

func TestProgress(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	for _, mode := range []Mode{ModePaged, ModeScroll} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			opts := scrollOptions(t, mode, 113)
			l := layoutOptions(t, mustEngine(t, 2), doc, opts)
			total := progressTotal(l, opts)
			if total <= 0 {
				t.Fatalf("total = %v, want positive", total)
			}

			prev := float32(-1)
			lines := 0
			for ci := range l.chapters {
				cf := &l.chapters[ci]
				for idx := 0; idx < cf.count(); idx++ {
					loc, ok := locForLineStart(cf, ci, idx)
					if !ok {
						continue
					}
					p, ok := l.Progress(loc)
					if !ok {
						t.Fatalf("chapter %d item %d: Progress failed", ci, idx)
					}
					if p < 0 || p > 1 {
						t.Fatalf("chapter %d item %d: Progress = %v, want [0,1]", ci, idx, p)
					}
					if p < prev {
						t.Fatalf("chapter %d item %d: Progress went backwards (%v < %v)",
							ci, idx, p, prev)
					}
					prev = p
					y, ok := l.OffsetOf(loc)
					if !ok {
						t.Fatalf("chapter %d item %d: OffsetOf failed", ci, idx)
					}
					if want := y / total; !closef(p, want, 1e-5) {
						t.Fatalf("chapter %d item %d: Progress = %v, want content y/total = %v",
							ci, idx, p, want)
					}
					lines++
				}
			}
			if lines < 30 {
				t.Fatalf("only %d lines checked", lines)
			}
			if prev >= 1 {
				t.Fatalf("last line progress = %v, want < 1 (a location is a top edge)", prev)
			}

			// The same location keeps its progress when the tile height changes:
			// progress must not depend on scroll or tile geometry.
			loc, ok := locForLineStart(&l.chapters[1], 1, 0)
			if !ok {
				t.Fatal("no sample location")
			}
			want, ok := l.Progress(loc)
			if !ok {
				t.Fatal("Progress failed for sample location")
			}
			for _, tileHeight := range []float32{37, 512, 4000} {
				resized := opts
				resized.TileHeight = tileHeight
				l2, err := l.Relayout(context.Background(), resized)
				if err != nil {
					t.Fatalf("Relayout: %v", err)
				}
				got, ok := l2.Progress(loc)
				if !ok || got != want {
					t.Fatalf("tile height %v: Progress = %v (%v), want %v",
						tileHeight, got, ok, want)
				}
			}

			// An anchor-only location works, and matches the node location.
			anchor := Location{Chapter: 1, Anchor: AnchorID(101)}
			pa, ok := l.Progress(anchor)
			if !ok {
				t.Fatal("Progress failed for anchor-only location")
			}
			if y, ok := l.OffsetOf(anchor); !ok || !closef(pa, y/total, 1e-5) {
				t.Fatalf("anchor progress = %v, OffsetOf = %v (%v)", pa, y, ok)
			}

			if _, ok := l.Progress(Location{Chapter: 99}); ok {
				t.Fatal("Progress accepted an out-of-range chapter")
			}
			if _, ok := l.Progress(Location{Chapter: 0, Node: InvalidNode}); ok {
				t.Fatal("Progress accepted an unresolvable location")
			}
		})
	}
}

func TestProgressPagedTracksPages(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	opts := scrollOptions(t, ModePaged, 0)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)
	pages := l.PageCount()
	if pages < 3 {
		t.Fatalf("fixture has %d pages, want >= 3", pages)
	}
	for pn := 0; pn < pages; pn++ {
		info := &l.pages[pn]
		cf := &l.chapters[info.chapter]
		loc, ok := locForLineStart(cf, info.chapter, info.first)
		if !ok {
			t.Fatalf("page %d: no first line location", pn)
		}
		p, ok := l.Progress(loc)
		if !ok {
			t.Fatalf("page %d: Progress failed", pn)
		}
		lo := float32(pn) / float32(pages)
		hi := float32(pn+1) / float32(pages)
		if p < lo-1e-5 || p >= hi {
			t.Fatalf("page %d: Progress = %v, want [%v,%v)", pn, p, lo, hi)
		}
	}
}

func TestProgressScrollFollowsContentHeight(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	opts := scrollOptions(t, ModeScroll, 113)
	l := layoutOptions(t, mustEngine(t, 2), doc, opts)
	// The chapter tops are exact progress values in scroll mode.
	for ci := range l.chapters {
		top, ok := l.ChapterContentTop(ci)
		if !ok {
			t.Fatalf("chapter %d: ChapterContentTop failed", ci)
		}
		cf := &l.chapters[ci]
		loc, ok := locForLineStart(cf, ci, 0)
		if !ok {
			t.Fatalf("chapter %d: no line location", ci)
		}
		p, ok := l.Progress(loc)
		if !ok {
			t.Fatalf("chapter %d: Progress failed", ci)
		}
		y, _ := l.OffsetOf(loc)
		if !closef(p, y/l.ContentHeight(), 1e-5) {
			t.Fatalf("chapter %d: Progress = %v, want %v", ci, p, y/l.ContentHeight())
		}
		if p < top/l.ContentHeight()-1e-5 {
			t.Fatalf("chapter %d: Progress %v starts before chapter top %v", ci, p, top)
		}
	}
}

// tallImageDoc is one block of text, an image taller than a page, and more text.
func tallImageDoc(t *testing.T) *Document {
	t.Helper()
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2, 3}},
		{Kind: NodeText, Text: strings.Repeat("before the image ", 40)},
		{Kind: NodeImage, Image: 3, Width: 200, Height: 900},
		{Kind: NodeText, Text: strings.Repeat("after the image ", 40)},
	}
	b := NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{ID: "img", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestSelectionTallImageAndProgress covers the two edge cases an image taller
// than a page creates: its selection quad still carries a content rect, and
// progress stays inside [0,1] and monotone even though the item overflows its
// page slot.
func TestSelectionTallImageAndProgress(t *testing.T) {
	doc := tallImageDoc(t)
	const imageNode = NodeID(2)
	from := Location{Chapter: 0, Node: 1}
	to := Location{Chapter: 0, Node: 3, Offset: 100}
	base := scrollOptions(t, ModePaged, 0)
	pageH := contentHeight(base)

	for _, mode := range []Mode{ModePaged, ModeScroll} {
		t.Run(fmt.Sprintf("mode=%d", mode), func(t *testing.T) {
			opts := base
			opts.Mode = mode
			opts.TileHeight = 113
			l := layoutOptions(t, mustEngine(t, 2), doc, opts)
			imageLoc := Location{Chapter: 0, Node: imageNode}
			wantY, ok := l.OffsetOf(imageLoc)
			if !ok {
				t.Fatal("OffsetOf(image) failed")
			}

			var imageQuads []SelectionQuad
			for _, q := range l.Selection(from, to) {
				if q.Rect.Height > 800 {
					imageQuads = append(imageQuads, q)
				}
			}
			if len(imageQuads) != 1 {
				t.Fatalf("image quads = %d, want 1", len(imageQuads))
			}
			q := imageQuads[0]
			if !closef(q.Content.Y, wantY, 0.01) || !closef(q.Content.Height, 900, 0.01) {
				t.Fatalf("image quad Content = %+v, want Y %v Height 900", q.Content, wantY)
			}
			if !closef(q.Content.X, q.Rect.X, 0.01) {
				t.Fatalf("image quad Content.X = %v, want Rect.X %v", q.Content.X, q.Rect.X)
			}
			if mode == ModeScroll {
				if q.Page != -1 {
					t.Fatalf("scroll image quad Page = %d, want -1", q.Page)
				}
			} else {
				page, ok := l.PageOf(imageLoc)
				if !ok || page != q.Page {
					t.Fatalf("paged image quad Page = %d, want PageOf %d (%v)", q.Page, page, ok)
				}
				wantContentY := float32(q.Page)*pageH + (q.Rect.Y - base.Margins.Top)
				if !closef(q.Content.Y, wantContentY, 0.01) {
					t.Fatalf("image quad Content.Y = %v, want %v", q.Content.Y, wantContentY)
				}
			}

			prev := float32(-1)
			seen := 0
			for ci := range l.chapters {
				cf := &l.chapters[ci]
				items := itemsOf(cf)
				for idx := 0; idx < len(items); idx++ {
					it := &items[idx]
					loc := Location{Chapter: ci, Node: it.node}
					if it.kind == itemLine {
						lineLoc, ok := locForLineStart(cf, ci, idx)
						if !ok {
							continue
						}
						loc = lineLoc
					}
					p, ok := l.Progress(loc)
					if !ok {
						t.Fatalf("chapter %d item %d: Progress failed", ci, idx)
					}
					if p < 0 || p > 1 {
						t.Fatalf("chapter %d item %d: Progress = %v, want [0,1]", ci, idx, p)
					}
					if p < prev {
						t.Fatalf("chapter %d item %d: Progress went backwards (%v < %v)",
							ci, idx, p, prev)
					}
					prev = p
					seen++
				}
			}
			if seen < 10 {
				t.Fatalf("only %d items checked", seen)
			}
		})
	}
}
