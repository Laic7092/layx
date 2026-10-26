package render

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

func loadTestFont(t testing.TB) FontFace {
	t.Helper()
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		t.Fatalf("read test font: %v", err)
	}
	return FontFace{Family: "Test Serif", Weight: 400, Data: data}
}

func testFontSet(t testing.TB) *FontSet {
	t.Helper()
	fs, err := NewFontSet(loadTestFont(t))
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	return fs
}

func mustEngine(t testing.TB, workers int) *Engine {
	t.Helper()
	e, err := New(Options{Workers: workers, PageCache: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func testDocument(t testing.TB, body string) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily:   "Test Serif",
			FontSize:     16,
			LineHeight:   1.5,
			Color:        paint.Color{A: 0xff},
			MarginBottom: 8,
		},
		{
			FontFamily:   "Test Serif",
			FontSize:     24,
			FontWeight:   700,
			LineHeight:   1.3,
			Color:        paint.Color{A: 0xff},
			MarginBottom: 12,
		},
		{
			FontFamily:      "Test Serif",
			FontSize:        16,
			LineHeight:      1.5,
			Color:           paint.Color{A: 0xff},
			MarginBottom:    8,
			FirstLineIndent: 24,
			TextAlign:       AlignJustify,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock, Style: 0, Children: []NodeID{1, 2, 3}},
		{Kind: NodeBlock, Style: 2, Anchor: 10, Children: []NodeID{4}},
		{Kind: NodeBlock, Style: 1, Anchor: 11, Children: []NodeID{5}},
		{Kind: NodeBlock, Style: 3, Anchor: 12, Children: []NodeID{6}},
		{Kind: NodeText, Style: 0, Text: "Chapter One"},
		{Kind: NodeText, Style: 0, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)},
		{Kind: NodeText, Style: 0, Text: strings.Repeat("A second paragraph shows indentation, justification and line wrapping. ", 30)},
	}
	ch := Chapter{ID: "ch1", Root: 0, Nodes: nodes, Anchors: map[AnchorID]NodeID{
		10: 1,
		11: 2,
		12: 3,
	}}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	if err := b.AddChapter(ch); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_ = body
	return doc
}

func testLayout(t testing.TB, doc *Document, workers int) *Layout {
	t.Helper()
	e := mustEngine(t, workers)
	l, err := e.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   16,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   testFontSet(t),
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return l
}

type recordPainter struct {
	saves     int
	restores  int
	fills     int
	images    int
	glyphRuns int
	glyphs    int
	glyphIDs  []uint32
	fonts     []string
}

func (r *recordPainter) Save()                                                   { r.saves++ }
func (r *recordPainter) Restore()                                                { r.restores++ }
func (r *recordPainter) ClipRect(paint.Rect)                                     {}
func (r *recordPainter) Transform(paint.Affine)                                  {}
func (r *recordPainter) FillRect(paint.Rect, paint.Color)                        { r.fills++ }
func (r *recordPainter) StrokeRect(paint.Rect, paint.Color, float32)             {}
func (r *recordPainter) FillRoundRect(paint.Rect, float32, paint.Color)          {}
func (r *recordPainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) { r.images++ }
func (r *recordPainter) DrawGlyphs(run paint.GlyphRun) {
	r.glyphRuns++
	r.glyphs += len(run.Glyphs)
	for _, g := range run.Glyphs {
		r.glyphIDs = append(r.glyphIDs, g.ID)
	}
	if run.Font != nil {
		r.fonts = append(r.fonts, run.Font.Family)
	}
}

func TestLayoutBasic(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 4)
	if l.PageCount() < 2 {
		t.Fatalf("expected multiple pages, got %d", l.PageCount())
	}
	totalLines := 0
	for ci := range l.chapters {
		cf := &l.chapters[ci]
		lastY := float32(-1)
		items := itemsOf(cf)
		for i := range items {
			it := &items[i]
			if it.kind != itemLine {
				continue
			}
			totalLines++
			if it.width > it.avail+0.5 {
				t.Fatalf("line wider than available: %v > %v", it.width, it.avail)
			}
			if it.y < lastY-0.01 {
				t.Fatalf("line y went backwards: %v < %v", it.y, lastY)
			}
			lastY = it.y
			if it.paraLine == 0 && len(it.para.segs) == 0 {
				t.Fatal("line has no segments")
			}
		}
	}
	if totalLines == 0 {
		t.Fatal("no lines laid out")
	}

	loc, ok := l.Locate(11)
	if !ok {
		t.Fatal("Locate(11) failed")
	}
	if loc.Node != 2 || loc.Chapter != 0 {
		t.Fatalf("unexpected location: %+v", loc)
	}
	page, ok := l.PageOf(loc)
	if !ok || page < 0 || page >= l.PageCount() {
		t.Fatalf("PageOf: %d %v", page, ok)
	}

	// Hit testing should find text somewhere on the first page.
	p0 := l.Page(0)
	if p0 == nil {
		t.Fatal("Page(0) nil")
	}
	found := false
	for y := float32(28); y < 580 && !found; y += 8 {
		for x := float32(28); x < 390; x += 8 {
			if _, ok := p0.HitTest(paint.Point{X: x, Y: y}); ok {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("HitTest found nothing on page 0")
	}

	rp := &recordPainter{}
	p0.Draw(rp)
	if rp.saves != 1 || rp.restores != 1 {
		t.Fatalf("save/restore mismatch: %d/%d", rp.saves, rp.restores)
	}
	if rp.glyphRuns == 0 || rp.glyphs == 0 {
		t.Fatalf("page drew no glyphs: runs=%d glyphs=%d", rp.glyphRuns, rp.glyphs)
	}

	from, _ := l.Locate(10)
	to, _ := l.Locate(12)
	quads := l.Selection(from, to)
	if len(quads) == 0 {
		t.Fatal("selection returned no quads")
	}
	for _, q := range quads {
		if q.Page < 0 || q.Page >= l.PageCount() {
			t.Fatalf("selection page out of range: %d", q.Page)
		}
		if q.Rect.Width < 0 || q.Rect.Height < 0 {
			t.Fatalf("negative selection rect: %+v", q.Rect)
		}
	}

	l2, err := l.Relayout(context.Background(), LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   20,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   testFontSet(t),
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	if l2.PageCount() < l.PageCount() {
		t.Fatalf("larger text produced fewer pages: %d < %d", l2.PageCount(), l.PageCount())
	}
}

func TestPaginationBreakBefore(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeBlock, Children: []NodeID{3}},
		{Kind: NodeBlock, Style: 1, Children: []NodeID{4}},
		{Kind: NodeText, Text: "first section"},
		{Kind: NodeText, Text: "second section"},
	}
	styles := []Style{
		{},
		{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{A: 0xff}, BreakBefore: true},
	}
	b := NewDocumentBuilder().SetStyles(styles)
	if err := b.AddChapter(Chapter{ID: "ch", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	l := testLayout(t, doc, 2)
	if l.PageCount() != 2 {
		t.Fatalf("break-before should force 2 pages, got %d", l.PageCount())
	}
	cf := &l.chapters[0]
	if itemsOf(cf)[cf.pages[0].last].breakBefore != true {
		t.Fatal("second page does not start at the break-before item")
	}
}

func TestImageLayoutAndLinks(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeImage, Style: 1, Image: 7, Width: 1000, Height: 500, Link: "https://example.com/a", Anchor: 20},
		{Kind: NodeBlock, Children: []NodeID{3}},
		{Kind: NodeText, Text: strings.Repeat("text after image ", 20)},
	}
	styles := []Style{
		{},
		{FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{A: 0xff}, TextAlign: AlignCenter},
	}
	b := NewDocumentBuilder().SetStyles(styles)
	if err := b.AddChapter(Chapter{
		ID: "ch", Root: 0, Nodes: nodes,
		Anchors: map[AnchorID]NodeID{20: 1},
	}); err != nil {
		t.Fatal(err)
	}
	doc, _ := b.Build()
	l := testLayout(t, doc, 2)
	cf := &l.chapters[0]
	var img *flowItem
	items := itemsOf(cf)
	for i := range items {
		if items[i].kind == itemImage {
			img = &items[i]
			break
		}
	}
	if img == nil {
		t.Fatal("image item missing")
	}
	if img.width != 372 {
		t.Fatalf("image should scale to content width 372, got %v", img.width)
	}
	if img.height < 185.9 || img.height > 186.1 {
		t.Fatalf("image aspect ratio not preserved: %v", img.height)
	}
	p := l.Page(0)
	rp := &recordPainter{}
	p.Draw(rp)
	if rp.images != 1 {
		t.Fatalf("expected one DrawImage, got %d", rp.images)
	}
	links := p.Links()
	if len(links) != 1 || links[0].URL != "https://example.com/a" {
		t.Fatalf("bad links: %+v", links)
	}
	loc, ok := l.Locate(20)
	if !ok {
		t.Fatal("image anchor not found")
	}
	got, ok := l.PageOf(loc)
	if !ok || got != 0 {
		t.Fatalf("image PageOf = %d %v", got, ok)
	}
}

func TestDeterministicAcrossWorkers(t *testing.T) {
	doc := testDocument(t, "")
	a := testLayout(t, doc, 1)
	b := testLayout(t, doc, 8)
	if a.PageCount() != b.PageCount() {
		t.Fatalf("page counts differ: %d vs %d", a.PageCount(), b.PageCount())
	}
	for i, p := range a.pages {
		q := b.pages[i]
		if p.chapter != q.chapter || p.first != q.first || p.last != q.last || p.top != q.top {
			t.Fatalf("page %d differs: %+v vs %+v", i, p, q)
		}
	}
	for i := range a.chapters {
		if a.chapters[i].count() != b.chapters[i].count() {
			t.Fatalf("chapter %d item count differs", i)
		}
		ai, bi := itemsOf(&a.chapters[i]), itemsOf(&b.chapters[i])
		for j := range ai {
			x, y := ai[j], bi[j]
			if x.kind != y.kind || x.start != y.start || x.end != y.end ||
				x.x != y.x || x.y != y.y || x.width != y.width || x.height != y.height {
				t.Fatalf("chapter %d item %d differs", i, j)
			}
		}
	}
}

func TestPageCache(t *testing.T) {
	doc := testDocument(t, "")
	e, _ := New(Options{Workers: 2, PageCache: 2})
	l, err := e.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff},
		},
		Fonts: testFontSet(t), Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err != nil {
		t.Fatal(err)
	}
	p0 := l.Page(0)
	if l.Page(0) != p0 {
		t.Fatal("cached page returned a different pointer")
	}
	l.Page(1)
	l.Page(2)
	l.cacheMu.Lock()
	n := len(l.cache)
	l.cacheMu.Unlock()
	if n > 2 {
		t.Fatalf("page cache exceeded limit: %d", n)
	}
}

func TestContextCancel(t *testing.T) {
	doc := testDocument(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mustEngine(t, 2).Layout(ctx, doc, LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff},
		},
		Fonts: testFontSet(t), Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestLimits(t *testing.T) {
	doc := testDocument(t, "")
	e, _ := New(Options{Workers: 1, Limits: Limits{MaxTextRunes: 10}})
	_, err := e.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff},
		},
		Fonts: testFontSet(t), Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err == nil {
		t.Fatal("expected limit error")
	}
}

func TestDocumentValidation(t *testing.T) {
	valid := func(nodes []Node, root NodeID, anchors map[AnchorID]NodeID) error {
		b := NewDocumentBuilder().SetStyles([]Style{{}, {FontSize: 12}})
		return b.AddChapter(Chapter{ID: "c", Root: root, Nodes: nodes, Anchors: anchors})
	}
	if err := valid([]Node{{Kind: NodeBlock, Children: []NodeID{1}}, {Kind: NodeText, Text: "x"}}, 0, nil); err != nil {
		t.Fatalf("valid doc rejected: %v", err)
	}
	cases := []struct {
		name    string
		nodes   []Node
		root    NodeID
		anchors map[AnchorID]NodeID
	}{
		{"empty", nil, 0, nil},
		{"root-out-of-range", []Node{{Kind: NodeBlock}}, 3, nil},
		{"child-out-of-range", []Node{{Kind: NodeBlock, Children: []NodeID{9}}}, 0, nil},
		{"cycle", []Node{{Kind: NodeBlock, Children: []NodeID{1}}, {Kind: NodeBlock, Children: []NodeID{0}}}, 0, nil},
		{"shared-child", []Node{{Kind: NodeBlock, Children: []NodeID{1, 1}}, {Kind: NodeText, Text: "x"}}, 0, nil},
		{"bad-style", []Node{{Kind: NodeBlock, Style: 99}}, 0, nil},
		{"bad-anchor", []Node{{Kind: NodeBlock}}, 0, map[AnchorID]NodeID{1: 5}},
		{"leaf-children", []Node{{Kind: NodeBlock, Children: []NodeID{1}}, {Kind: NodeText, Children: []NodeID{0}}}, 0, nil},
		{"root-leaf", []Node{{Kind: NodeText, Text: "x"}}, 0, nil},
	}
	for _, tc := range cases {
		if err := valid(tc.nodes, tc.root, tc.anchors); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestSelectionShrinksToRuneRange(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 2)
	from := Location{Chapter: 0, Node: 5}
	to := from
	to.Offset = 10
	q := l.Selection(from, to)
	if len(q) != 1 {
		t.Fatalf("expected one quad for a same-line range, got %d", len(q))
	}
	if q[0].Rect.Width <= 0 || q[0].Rect.Width >= 372 {
		t.Fatalf("unexpected selection width: %v", q[0].Rect.Width)
	}
}

// TestGoldenLayout pins the layout of the fixture with testdata/DejaVuSerif.ttf.
// It is intentionally exact: line breaking is part of the public result.
func TestGoldenLayout(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 4)
	if l.PageCount() != 5 {
		t.Fatalf("pages = %d, want 5", l.PageCount())
	}
	lines := 0
	for _, it := range itemsOf(&l.chapters[0]) {
		if it.kind == itemLine {
			lines++
		}
	}
	if lines != 94 {
		t.Fatalf("lines = %d, want 94", lines)
	}
	wantRanges := [][2]int{{0, 22}, {22, 44}, {44, 67}, {67, 90}, {90, 94}}
	for i, want := range wantRanges {
		got := l.pages[i]
		if got.first != want[0] || got.last != want[1] {
			t.Fatalf("page %d range = [%d,%d), want [%d,%d)", i, got.first, got.last, want[0], want[1])
		}
	}
	flowItems := itemsOf(&l.chapters[0])
	first := flowItems[0]
	last := flowItems[len(flowItems)-1]
	if got := string(paraRunes(first.para)[first.start:first.end]); got != "Chapter One" {
		t.Fatalf("first line = %q", got)
	}
	if got := string(paraRunes(last.para)[last.start:last.end]); got != "and line wrapping. " {
		t.Fatalf("last line = %q", got)
	}
}

func TestDocumentIsImmutable(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: "hello"},
	}
	children := nodes[0].Children
	b := NewDocumentBuilder().SetStyles([]Style{{}, {FontFamily: "Test Serif", FontSize: 16}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	children[0] = 0 // mutate the caller's backing array
	nodes[1].Text = "goodbye"
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := doc.Chapter(0)
	if ch.Nodes[1].Text != "hello" {
		t.Fatalf("node text changed after build: %q", ch.Nodes[1].Text)
	}
	if ch.Nodes[0].Children[0] != 1 {
		t.Fatalf("children slice aliased the caller: %v", ch.Nodes[0].Children)
	}
}

func TestBuildRevalidatesStyles(t *testing.T) {
	nodes := []Node{{Kind: NodeBlock, Children: []NodeID{1}}, {Kind: NodeText, Text: "x", Style: 1}}
	b := NewDocumentBuilder().SetStyles([]Style{{}, {FontSize: 16}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	b.SetStyles([]Style{{}})
	if _, err := b.Build(); err == nil {
		t.Fatal("expected Build to reject a style removed after AddChapter")
	}
}

func TestAnchorOnlyLocation(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 2)
	loc := Location{Chapter: 0, Node: InvalidNode, Anchor: 11}
	p, ok := l.PageOf(loc)
	if !ok {
		t.Fatal("PageOf failed for anchor-only location")
	}
	to := Location{Chapter: 0, Node: InvalidNode, Anchor: 12}
	if p2, ok := l.PageOf(to); !ok || p2 < p {
		t.Fatalf("anchor 12 page = %d %v (anchor 11 page %d)", p2, ok, p)
	}
	if q := l.Selection(loc, to); len(q) == 0 {
		t.Fatal("anchor-only selection returned no quads")
	}
}

// TestPageGlyphBaselines guards against display lists that forget to offset a
// line's baseline by the line's position inside the flow (which used to draw
// every line of a paragraph on top of the first one).
func TestPageGlyphBaselines(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 2)

	maxBaselines := 0
	for pn := 0; pn < l.PageCount(); pn++ {
		p := l.Page(pn)
		if p == nil {
			t.Fatalf("page %d is nil", pn)
		}
		info := &l.pages[pn]
		cf := &l.chapters[info.chapter]

		var want []float32
		for i := info.first; i < info.last && i < cf.count(); i++ {
			itv := cf.item(i)
			it := &itv
			if it.kind != itemLine {
				continue
			}
			want = append(want, l.opts.Margins.Top-info.top+it.y+it.baseline)
		}

		seen := make(map[float32]bool)
		for _, op := range p.ops {
			if op.kind != opGlyphs {
				continue
			}
			y := op.run.Y
			found := false
			for _, w := range want {
				if d := y - w; d < 0.5 && d > -0.5 {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("page %d: glyph run baseline %.2f does not match any line baseline", pn, y)
			}
			seen[float32(int(y*2+0.5))/2] = true
		}
		if len(seen) > maxBaselines {
			maxBaselines = len(seen)
		}
	}
	if maxBaselines < 2 {
		t.Fatalf("expected multiple baselines across pages, got %d", maxBaselines)
	}
}

// TestPaginateZeroWidowsOrphans pins the corrected zero semantics: a zero
// Widows/Orphans in TextRules means "no protection", and a non-zero value
// still moves a paragraph that would leave too few lines at the top of a page.
func TestPaginateWidowsOrphans(t *testing.T) {
	const pageHeight = 115
	items := make([]flowItem, 0, 16)
	for i := 0; i < 10; i++ {
		items = append(items, flowItem{
			kind: itemImage, x: 0, y: float32(i * 10), width: 10, height: 10,
		})
	}
	p := &paragraph{runeLen: 20}
	for li := 0; li < 6; li++ {
		items = append(items, flowItem{
			kind: itemLine, para: p, start: int32(li), end: int32(li + 1),
			width: 10, avail: 10, height: 10, y: float32(100 + li*10),
			paraLine: int32(li), paraLines: 6,
		})
	}

	// The zero value is the CSS initial value: two orphan lines are required,
	// so the paragraph starts on the next page instead of one line stranded at
	// the bottom.
	pages := paginateN(context.Background(), len(items), func(i int) pageItem {
		return pageItemOf(&items[i])
	}, pageHeight, Typography{})
	if len(pages) < 2 {
		t.Fatalf("expected at least two pages, got %d", len(pages))
	}
	if pages[0].last != 10 {
		t.Fatalf("zero orphans (spec default 2): first page last item = %d, want 10", pages[0].last)
	}

	// One is the smallest value CSS allows and it fills the page: this is how
	// a host asks for no protection, since zero is not legal CSS.
	typo := Typography{Rules: TextRules{Widows: 1, Orphans: 1}}
	pages = paginateN(context.Background(), len(items), func(i int) pageItem {
		return pageItemOf(&items[i])
	}, pageHeight, typo)
	if len(pages) < 2 {
		t.Fatalf("expected at least two pages, got %d", len(pages))
	}
	if pages[0].last != 11 {
		t.Fatalf("orphans=1: first page last item = %d, want 11", pages[0].last)
	}
}
