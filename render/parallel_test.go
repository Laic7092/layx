package render

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/laix/layx/render/paint"
)

// wrappedDocument builds one chapter whose entire body is a single block of
// paragraphs. It is the document shape that used to limit a layout to one break
// task (the chapter root has one block child), so it is the regression test for
// paragraph-level granularity.
func wrappedDocument(t testing.TB, paragraphs int) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16,
			LineHeight: 1.5, Color: paint.Color{A: 0xff}, MarginBottom: 8,
		},
	}
	nodes := []Node{
		{Kind: NodeBlock},
		{Kind: NodeBlock, Style: 1},
	}
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 3)
	body := make([]NodeID, 0, paragraphs)
	for i := 0; i < paragraphs; i++ {
		body = append(body, NodeID(len(nodes)))
		nodes = append(nodes, Node{Kind: NodeText, Text: fmt.Sprintf("%d %s", i, text)})
		// A hard break ends the paragraph: contiguous text nodes are one
		// paragraph, so this is what makes the body many units of work.
		body = append(body, NodeID(len(nodes)))
		nodes = append(nodes, Node{Kind: NodeBreak})
	}
	nodes[1].Children = body
	nodes[0].Children = []NodeID{1}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "wrapped", Root: 0, Nodes: nodes}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func wrappedOptions(t testing.TB) LayoutOptions {
	t.Helper()
	return LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   16,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   testFontSet(t),
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	}
}

// TestBreakGranularityIsParagraphs asserts the property that makes the load
// balanced: one chapter with one top-level block of 40 paragraphs still yields
// 40 parallel tasks, not one.
func TestBreakGranularityIsParagraphs(t *testing.T) {
	const paragraphs = 40
	doc := wrappedDocument(t, paragraphs)
	e, err := New(Options{Workers: 8, Measure: true, PageCache: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Layout(context.Background(), doc, wrappedOptions(t)); err != nil {
		t.Fatal(err)
	}
	s := e.Stats()
	if s.Tasks < paragraphs {
		t.Fatalf("Tasks = %d, want >= %d (break tasks are still per block)", s.Tasks, paragraphs)
	}
	if s.Shapes != paragraphs {
		t.Fatalf("Shapes = %d, want %d (one per paragraph)", s.Shapes, paragraphs)
	}
}

// TestFineGrainedBreakIsDeterministic keeps the documented invariant: the layout
// does not depend on the worker count. The wrapped document exercises the new
// plan/break/attach split, including a single block with many paragraphs.
func TestFineGrainedBreakIsDeterministic(t *testing.T) {
	doc := wrappedDocument(t, 40)
	one := testLayout(t, doc, 1)
	many := testLayout(t, doc, 8)
	compareLayouts(t, one, many)

	if len(one.chapters) != len(many.chapters) {
		t.Fatalf("chapter count %d vs %d", len(one.chapters), len(many.chapters))
	}
	for ci := range one.chapters {
		a, b := itemsOf(&one.chapters[ci]), itemsOf(&many.chapters[ci])
		if len(a) != len(b) {
			t.Fatalf("chapter %d item count %d vs %d", ci, len(a), len(b))
		}
		for j := range a {
			x, y := a[j], b[j]
			if x.kind != y.kind || x.start != y.start || x.end != y.end ||
				x.x != y.x || x.y != y.y || x.width != y.width || x.height != y.height {
				t.Fatalf("chapter %d item %d differs: %+v vs %+v", ci, j, x, y)
			}
		}
	}
}

// compareLayouts checks the page map and every built display list.
func compareLayouts(t *testing.T, a, b *Layout) {
	t.Helper()
	if a.PageCount() != b.PageCount() {
		t.Fatalf("page counts differ: %d vs %d", a.PageCount(), b.PageCount())
	}
	for i := 0; i < a.PageCount(); i++ {
		pa, pb := a.Page(i), b.Page(i)
		if !sameDisplayList(pa, pb) {
			t.Fatalf("page %d differs", i)
		}
	}
}

func sameDisplayList(a, b *Page) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.ops) != len(b.ops) || len(a.links) != len(b.links) {
		return false
	}
	for i := range a.ops {
		x, y := &a.ops[i], &b.ops[i]
		if x.kind != y.kind || x.rect != y.rect || x.color != y.color ||
			x.radius != y.radius || x.image != y.image {
			return false
		}
		if len(x.run.Glyphs) != len(y.run.Glyphs) ||
			x.run.X != y.run.X || x.run.Y != y.run.Y || x.run.Size != y.run.Size {
			return false
		}
		for j := range x.run.Glyphs {
			if x.run.Glyphs[j] != y.run.Glyphs[j] {
				return false
			}
		}
	}
	for i := range a.links {
		if a.links[i] != b.links[i] {
			return false
		}
	}
	return true
}

// TestConcurrentLayoutsShareTheEngine runs several layouts at once on one
// Engine, the way a reader prefetches while the foreground reflows. Results must
// still match a serial layout.
func TestConcurrentLayoutsShareTheEngine(t *testing.T) {
	doc := wrappedDocument(t, 30)
	opts := wrappedOptions(t)
	e, err := New(Options{Workers: 8, PageCache: 4, TileCache: 4, Measure: true})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	const runs = 8
	out := make([]*Layout, runs)
	errs := make([]error, runs)
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Every call is a cold variant (fresh Engine state), so this is the
			// worst case for cache and budget contention.
			o := opts
			o.Typography.ParagraphGap = float32(i % 3)
			out[i], errs[i] = e.Layout(context.Background(), doc, o)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("layout %d: %v", i, err)
		}
	}
	// run 0 and the reference share options; the others only move paragraph
	// gaps, so page counts may differ but no layout may be corrupt.
	compareLayouts(t, ref, out[0])
	for i := 1; i < runs; i++ {
		if out[i].PageCount() == 0 {
			t.Fatalf("layout %d produced no pages", i)
		}
	}
	if s := e.Stats(); s.Layouts != runs+1 {
		t.Fatalf("Layouts = %d, want %d", s.Layouts, runs+1)
	}
}

func TestPrefetchPagesMatchLazyPages(t *testing.T) {
	doc := testDocument(t, "")
	opts := wrappedOptions(t)
	if opts.Typography.FontFamily != "Test Serif" {
		t.Fatal("test options changed")
	}
	lazy, err := mustEngine(t, 4).Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := mustEngine(t, 8).Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.Prefetch(context.Background(), 0, pre.PageCount()); err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if got, want := len(pre.cache), pre.PageCount(); got != want {
		t.Fatalf("prefetched %d pages, want %d", got, want)
	}
	for i := 0; i < lazy.PageCount(); i++ {
		if !sameDisplayList(lazy.Page(i), pre.Page(i)) {
			t.Fatalf("page %d differs between lazy and prefetched build", i)
		}
	}
}

func TestPrefetchTilesMatchLazyTiles(t *testing.T) {
	doc := testDocument(t, "")
	opts := scrollOptions(t, ModeScroll, 80)
	lazy := layoutOptions(t, mustEngine(t, 4), doc, opts)
	pre := layoutOptions(t, mustEngine(t, 8), doc, opts)
	if lazy.TileCount() == 0 {
		t.Fatal("no tiles")
	}
	if err := pre.Prefetch(context.Background(), 0, pre.TileCount()); err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	for i := 0; i < lazy.TileCount(); i++ {
		a, b := lazy.Tile(i), pre.Tile(i)
		if a == nil || b == nil {
			t.Fatalf("tile %d nil", i)
		}
		if !sameTileOps(a, b) {
			t.Fatalf("tile %d differs between lazy and prefetched build", i)
		}
	}
}

func sameTileOps(a, b *Tile) bool {
	if len(a.ops) != len(b.ops) || len(a.links) != len(b.links) {
		return false
	}
	for i := range a.ops {
		x, y := &a.ops[i], &b.ops[i]
		if x.kind != y.kind || x.rect != y.rect || x.color != y.color || x.image != y.image {
			return false
		}
		if len(x.run.Glyphs) != len(y.run.Glyphs) || x.run.X != y.run.X || x.run.Y != y.run.Y {
			return false
		}
		for j := range x.run.Glyphs {
			if x.run.Glyphs[j] != y.run.Glyphs[j] {
				return false
			}
		}
	}
	for i := range a.links {
		if a.links[i] != b.links[i] {
			return false
		}
	}
	return true
}

// TestPrefetchClampsToCache keeps the prefetch from evicting what it just built.
func TestPrefetchClampsToCache(t *testing.T) {
	doc := testDocument(t, "")
	e, err := New(Options{Workers: 4, PageCache: 2})
	if err != nil {
		t.Fatal(err)
	}
	l, err := e.Layout(context.Background(), doc, wrappedOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Prefetch(context.Background(), 0, l.PageCount()); err != nil {
		t.Fatal(err)
	}
	l.cacheMu.Lock()
	n := len(l.cache)
	l.cacheMu.Unlock()
	if n > 2 {
		t.Fatalf("prefetch kept %d pages, want <= 2", n)
	}
}

func TestStartPrefetchAndCancel(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 4)

	ctx, cancel := context.WithCancel(context.Background())
	p := l.StartPrefetch(ctx, 0, l.PageCount())
	cancel()
	if err := p.Wait(); err == nil {
		t.Fatal("cancelled prefetch returned nil error")
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("Done not closed after Wait")
	}

	p2 := l.StartPrefetch(context.Background(), 0, l.PageCount())
	if err := p2.Wait(); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if l.Page(0) == nil {
		t.Fatal("prefetch did not cache page 0")
	}
	if err := (*Prefetch)(nil).Wait(); err != nil {
		t.Fatalf("nil handle Wait = %v", err)
	}
}

func TestEngineCloseKeepsWorking(t *testing.T) {
	doc := testDocument(t, "")
	opts := wrappedOptions(t)
	e, err := New(Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	second, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout after Close: %v", err)
	}
	compareLayouts(t, first, second)
	e.Close()
	e.Close()
}
