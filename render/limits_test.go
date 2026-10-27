package render

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// limitError asserts that err is a limit failure and returns the structured
// error so tests can check the measured/limit numbers.
func limitError(t *testing.T, err error, what string) *DocumentError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: got nil error, want ErrLimitExceeded", what)
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("%s: error = %v, want ErrLimitExceeded", what, err)
	}
	var de *DocumentError
	if !errors.As(err, &de) {
		t.Fatalf("%s: error %v is not a *DocumentError", what, err)
	}
	if de.Kind != ErrLimitExceeded {
		t.Fatalf("%s: DocumentError.Kind = %v, want ErrLimitExceeded", what, de.Kind)
	}
	return de
}

// flowLineCount counts the line boxes of a laid-out layout, so a test can set
// MaxLines to exactly the document's natural line count.
func flowLineCount(l *Layout) int {
	n := 0
	for ci := range l.chapters {
		if !l.inRange(ci) {
			continue
		}
		for ui := range l.chapters[ci].units {
			u := l.chapters[ci].units[ui].unit
			if u.kind == breakLines {
				n += len(u.lines)
			}
		}
	}
	return n
}

// TestLimitMaxPages checks that MaxPages fails a paged layout with a structured
// error, while the exact natural page count is accepted. A pathological margin
// can otherwise turn a modest document into a page map of unbounded size.
func TestLimitMaxPages(t *testing.T) {
	doc := testDocument(t, "")
	opts := statsTestOptions(testFontSet(t))
	ctx := context.Background()

	full, err := mustEngine(t, 4).Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	pages := full.PageCount()
	if pages < 2 {
		t.Fatalf("test document needs at least 2 pages, got %d", pages)
	}

	// Exactly the natural page count is allowed.
	atLimit, err := New(Options{Workers: 2, Limits: Limits{MaxPages: pages}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l, err := atLimit.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout at MaxPages=%d: %v", pages, err)
	}
	if l.PageCount() != pages {
		t.Fatalf("PageCount = %d, want %d", l.PageCount(), pages)
	}

	// One page less must fail with the measured count and the limit.
	over, err := New(Options{Workers: 2, Limits: Limits{MaxPages: pages - 1}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = over.Layout(ctx, doc, opts)
	de := limitError(t, err, "MaxPages")
	if !strings.Contains(de.Detail, "> limit "+fmt.Sprint(pages-1)) {
		t.Fatalf("Detail = %q, want the limit %d in it", de.Detail, pages-1)
	}
	if !strings.Contains(de.Detail, "pages") {
		t.Fatalf("Detail = %q, want the measured quantity named", de.Detail)
	}
}

// TestLimitMaxLines checks MaxLines at the exact boundary: the natural line
// count is accepted and one line less fails with a structured error.
func TestLimitMaxLines(t *testing.T) {
	doc := testDocument(t, "")
	opts := statsTestOptions(testFontSet(t))
	ctx := context.Background()

	full, err := mustEngine(t, 4).Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	lines := flowLineCount(full)
	if lines < 2 {
		t.Fatalf("test document needs at least 2 line boxes, got %d", lines)
	}

	atLimit, err := New(Options{Workers: 2, Limits: Limits{MaxLines: lines}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := atLimit.Layout(ctx, doc, opts); err != nil {
		t.Fatalf("Layout at MaxLines=%d: %v", lines, err)
	}

	over, err := New(Options{Workers: 2, Limits: Limits{MaxLines: lines - 1}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = over.Layout(ctx, doc, opts)
	de := limitError(t, err, "MaxLines")
	if !strings.Contains(de.Detail, "> limit "+fmt.Sprint(lines-1)) {
		t.Fatalf("Detail = %q, want the limit %d in it", de.Detail, lines-1)
	}
}

// TestLimitMaxLinesCachedRelayout proves cached break chapters count towards
// MaxLines. A first ranged layout only breaks chapter 0; the Relayout widens the
// range, so chapter 1 is fresh while chapter 0's line boxes come from the break
// cache. The limit sits between the two totals: without counting the cached
// chapter the second call would pass.
func TestLimitMaxLinesCachedRelayout(t *testing.T) {
	doc := multiChapterDoc(t, 2)
	opts := statsTestOptions(testFontSet(t))
	ctx := context.Background()

	full, err := mustEngine(t, 2).Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	firstLines := len(full.chapters[0].ys)
	allLines := firstLines + len(full.chapters[1].ys)
	if firstLines < 2 || allLines <= firstLines+1 {
		t.Fatalf("need a multi-line first chapter and a larger total: first=%d all=%d", firstLines, allLines)
	}

	// MaxLines allows the first chapter but not both.
	eng, err := New(Options{Workers: 2, Limits: Limits{MaxLines: firstLines + 1}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, err := eng.LayoutRange(ctx, doc, opts, 0, 1)
	if err != nil {
		t.Fatalf("LayoutRange [0,1): %v", err)
	}
	if got := flowLineCount(first); got != firstLines {
		t.Fatalf("ranged layout has %d lines, want %d", got, firstLines)
	}

	widen := opts
	widen.Range = &ChapterRange{First: 0, Last: 2}
	if _, err := first.Relayout(ctx, widen); err == nil {
		t.Fatal("Relayout over the full document with cached chapter 0 lines did not enforce MaxLines")
	} else {
		limitError(t, err, "MaxLines on cached relayout")
	}
}

// TestLimitMaxTiles checks MaxTiles in scroll mode: initScroll rejects a tile
// count over the limit and accepts the exact count.
func TestLimitMaxTiles(t *testing.T) {
	doc := testDocument(t, "")
	opts := scrollOptions(t, ModeScroll, 64)
	ctx := context.Background()

	full, err := mustEngine(t, 2).Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	tiles := full.TileCount()
	if tiles < 2 {
		t.Fatalf("test document needs at least 2 tiles at height 64, got %d", tiles)
	}

	atLimit, err := New(Options{Workers: 2, Limits: Limits{MaxTiles: tiles}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l, err := atLimit.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout at MaxTiles=%d: %v", tiles, err)
	}
	if l.TileCount() != tiles {
		t.Fatalf("TileCount = %d, want %d", l.TileCount(), tiles)
	}

	over, err := New(Options{Workers: 2, Limits: Limits{MaxTiles: tiles - 1}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = over.Layout(ctx, doc, opts)
	de := limitError(t, err, "MaxTiles")
	if !strings.Contains(de.Detail, "> limit "+fmt.Sprint(tiles-1)) {
		t.Fatalf("Detail = %q, want the limit %d in it", de.Detail, tiles-1)
	}
}

// TestLimitMaxTilesRelayout checks that a Relayout which lowers TileHeight
// re-checks the tile count instead of reusing the previous layout's geometry.
func TestLimitMaxTilesRelayout(t *testing.T) {
	doc := testDocument(t, "")
	base := scrollOptions(t, ModeScroll, 97)
	ctx := context.Background()

	full, err := mustEngine(t, 2).Layout(ctx, doc, base)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	tiles := full.TileCount()
	if tiles < 2 || full.ContentHeight() <= float32(tiles)*float32(MinTileHeight) {
		t.Fatalf("document too short for this test: tiles=%d height=%v", tiles, full.ContentHeight())
	}

	eng, err := New(Options{Workers: 2, Limits: Limits{MaxTiles: tiles}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l, err := eng.Layout(ctx, doc, base)
	if err != nil {
		t.Fatalf("Layout at MaxTiles=%d: %v", tiles, err)
	}

	smaller := base
	smaller.TileHeight = MinTileHeight
	_, err = l.Relayout(ctx, smaller)
	de := limitError(t, err, "MaxTiles on relayout")
	if !strings.Contains(de.Detail, "> limit "+fmt.Sprint(tiles)) {
		t.Fatalf("Detail = %q, want the limit %d in it", de.Detail, tiles)
	}
}

// TestLimitMaxPagesRelayout checks that re-pagination after an option change
// enforces MaxPages, including when the line boxes come from the break cache.
func TestLimitMaxPagesRelayout(t *testing.T) {
	doc := testDocument(t, "")
	base := statsTestOptions(testFontSet(t))
	ctx := context.Background()

	full, err := mustEngine(t, 2).Layout(ctx, doc, base)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	pages := full.PageCount()
	if pages < 2 {
		t.Fatalf("test document needs at least 2 pages, got %d", pages)
	}

	eng, err := New(Options{Workers: 2, Limits: Limits{MaxPages: pages}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l, err := eng.Layout(ctx, doc, base)
	if err != nil {
		t.Fatalf("Layout at MaxPages=%d: %v", pages, err)
	}

	// A shorter page slot needs more pages for the same flow. The BreakKey does
	// not include the page height, so the line boxes are reused but pagination
	// must run again and enforce the limit.
	shorter := base
	shorter.Viewport.Height = base.Viewport.Height / 2
	_, err = l.Relayout(ctx, shorter)
	de := limitError(t, err, "MaxPages on relayout")
	if !strings.Contains(de.Detail, "> limit "+fmt.Sprint(pages)) {
		t.Fatalf("Detail = %q, want the limit %d in it", de.Detail, pages)
	}
}

// TestLimitsDefaultsNoFalsePositive checks the default limits leave ordinary
// documents alone in both modes, and that DefaultLimits documents the shipped
// values.
func TestLimitsDefaultsNoFalsePositive(t *testing.T) {
	d := DefaultLimits()
	if d.MaxPages != 100_000 || d.MaxTiles != 100_000 || d.MaxLines != 2_000_000 {
		t.Fatalf("DefaultLimits pages/tiles/lines = %d/%d/%d", d.MaxPages, d.MaxTiles, d.MaxLines)
	}

	doc := testDocument(t, "")
	fs := testFontSet(t)
	ctx := context.Background()
	eng := mustEngine(t, 4)

	paged, err := eng.Layout(ctx, doc, statsTestOptions(fs))
	if err != nil {
		t.Fatalf("paged Layout with default limits: %v", err)
	}
	if paged.PageCount() <= 0 || paged.PageCount() >= d.MaxPages {
		t.Fatalf("paged PageCount = %d", paged.PageCount())
	}
	if _, err := paged.Relayout(ctx, statsTestOptions(fs)); err != nil {
		t.Fatalf("Relayout with default limits: %v", err)
	}

	// The smallest accepted tile height is still far from the tile limit for an
	// ordinary document; a smaller TileHeight is rejected by input validation,
	// not by MaxTiles.
	scroll := scrollOptions(t, ModeScroll, MinTileHeight)
	sl, err := eng.Layout(ctx, doc, scroll)
	if err != nil {
		t.Fatalf("scroll Layout with default limits: %v", err)
	}
	if sl.TileCount() <= 0 || sl.TileCount() >= d.MaxTiles {
		t.Fatalf("scroll TileCount = %d", sl.TileCount())
	}
}

// TestCountBreakLinesNested checks nested blocks are included in the cached
// line count so MaxLines sees every line box.
func TestCountBreakLinesNested(t *testing.T) {
	line := lineBreak{end: 1}
	units := []breakUnit{
		{kind: breakLines, lines: []lineBreak{line, line}},
		{kind: breakBlockUnit, box: &breakBox{units: []breakUnit{
			{kind: breakLines, lines: []lineBreak{line}},
			{kind: breakBlockUnit, box: &breakBox{units: []breakUnit{
				{kind: breakLines, lines: []lineBreak{line, line, line}},
			}}},
		}}},
		{kind: breakImage},
	}
	if got := countBreakLines(units); got != 6 {
		t.Fatalf("countBreakLines = %d, want 6", got)
	}
}

// TestLayoutStatsPerCall covers the per-call phase attribution: it must
// describe exactly the call that produced the layout and must not be polluted
// by concurrent layouts of other documents.
func TestLayoutStatsPerCall(t *testing.T) {
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()
	doc := testDocument(t, "")
	small := multiChapterDoc(t, 1)

	// Baseline counters that do not depend on timing or concurrency.
	baseEngine, err := New(Options{Workers: 4, Measure: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	baseline := func(d *Document) LayoutStats {
		l, err := baseEngine.Layout(ctx, d, opts)
		if err != nil {
			t.Fatalf("baseline Layout: %v", err)
		}
		return l.Stats()
	}
	wantBig := baseline(doc)
	wantSmall := baseline(small)
	if wantBig.Shapes == 0 || wantSmall.Shapes == 0 {
		t.Fatalf("Shapes = %d / %d, want > 0", wantBig.Shapes, wantSmall.Shapes)
	}
	if wantBig.Tasks == 0 || wantSmall.Tasks == 0 {
		t.Fatalf("Tasks = %d / %d, want > 0", wantBig.Tasks, wantSmall.Tasks)
	}
	if wantBig.Pages != baseline(doc).Pages || wantBig.Pages <= 0 {
		t.Fatalf("baseline Pages is not stable: %d", wantBig.Pages)
	}

	eng, err := New(Options{Workers: 4, Measure: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]LayoutStats, 8)
	errs := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := doc
			if i%2 == 1 {
				d = small
			}
			l, lerr := eng.Layout(ctx, d, opts)
			if lerr != nil {
				errs[i] = lerr
				return
			}
			results[i] = l.Stats()
			if got := l.PageCount(); got != results[i].Pages {
				errs[i] = fmt.Errorf("layout %d: Stats().Pages = %d, PageCount = %d", i, results[i].Pages, got)
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("%v", err)
		}
	}
	for i, st := range results {
		want := wantBig
		if i%2 == 1 {
			want = wantSmall
		}
		if st.Pages != want.Pages || st.Shapes != want.Shapes || st.Tasks != want.Tasks {
			t.Fatalf("layout %d: Stats = Pages %d Shapes %d Tasks %d, want Pages %d Shapes %d Tasks %d (concurrent calls polluted it)",
				i, st.Pages, st.Shapes, st.Tasks, want.Pages, want.Shapes, want.Tasks)
		}
		if st.Total <= 0 {
			t.Fatalf("layout %d: Total = %v with Measure on", i, st.Total)
		}
	}
	if len(results) > 0 && results[0].Tiles != 0 {
		t.Fatalf("paged layout Tiles = %d, want 0", results[0].Tiles)
	}

	// A snapshot is immutable: later layouts on the same engine do not change it.
	l0, err := eng.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	first := l0.Stats()
	if _, err := eng.Layout(ctx, small, opts); err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if got := l0.Stats(); got != first {
		t.Fatalf("Layout.Stats changed after another call: %+v -> %+v", first, got)
	}

	// Measure off: durations are zero but the counters are still reported.
	off := mustEngine(t, 2)
	l, err := off.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout (Measure off): %v", err)
	}
	st := l.Stats()
	if st.Total != 0 || st.Keys != 0 || st.Break != 0 || st.Stack != 0 || st.Paginate != 0 || st.Assemble != 0 {
		t.Fatalf("Measure off reported durations: %+v", st)
	}
	if st.Shapes == 0 || st.Tasks == 0 || st.Pages != l.PageCount() {
		t.Fatalf("Measure off lost the counters: %+v", st)
	}

	// Scroll layouts report Tiles instead of Pages.
	sl, err := off.Layout(ctx, doc, scrollOptions(t, ModeScroll, 97))
	if err != nil {
		t.Fatalf("scroll Layout: %v", err)
	}
	sst := sl.Stats()
	if sst.Tiles != sl.TileCount() || sst.Tiles == 0 || sst.Pages != 0 {
		t.Fatalf("scroll Stats = Tiles %d (TileCount %d), Pages %d", sst.Tiles, sl.TileCount(), sst.Pages)
	}
}

// TestLayoutRangeStats checks the per-call attribution of a ranged layout:
// only the included chapters are broken and paginated, so its counters describe
// the range, not the document.
func TestLayoutRangeStats(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()
	eng, err := New(Options{Workers: 2, Measure: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	full, err := eng.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	ranged, err := eng.LayoutRange(ctx, doc, opts, 1, 2)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}
	if _, wantPages, ok := full.PageRange(1); !ok || ranged.PageCount() != wantPages {
		t.Fatalf("ranged PageCount = %d, want chapter 1's %d pages", ranged.PageCount(), wantPages)
	}
	fs1, fs2 := full.Stats(), ranged.Stats()
	if fs2.Tasks >= fs1.Tasks {
		t.Fatalf("ranged Tasks %d >= full Tasks %d, want strictly less work", fs2.Tasks, fs1.Tasks)
	}
	if fs2.Shapes >= fs1.Shapes {
		t.Fatalf("ranged Shapes %d >= full Shapes %d, want strictly less shaping", fs2.Shapes, fs1.Shapes)
	}
}

// TestLayoutStatsZeroValue keeps the Stats type usable from a host: the zero
// value must be safe to read.
func TestLayoutStatsZeroValue(t *testing.T) {
	var st LayoutStats
	if st.Total != 0 || st.Pages != 0 || st.Tiles != 0 || st.Shapes != 0 {
		t.Fatalf("zero LayoutStats = %+v", st)
	}
}

// TestLimitNoLayoutOnError makes sure a layout rejected by a limit leaves no
// Layout behind.
func TestLimitNoLayoutOnError(t *testing.T) {
	doc := testDocument(t, "")
	opts := statsTestOptions(testFontSet(t))
	eng, err := New(Options{Workers: 2, Limits: Limits{MaxPages: 1}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l, err := eng.Layout(context.Background(), doc, opts)
	if err == nil || l != nil {
		t.Fatalf("Layout = %v, %v; want nil, limit error", l, err)
	}
}

// TestIssueDroppedImage checks the §3.3 diagnostic exit for content the layout
// drops: an image node without a size cannot be represented and must surface as
// IssueDroppedContent, both through Options.OnIssue and through Layout.Issues.
func TestIssueDroppedImage(t *testing.T) {
	b := NewDocumentBuilder().SetStyles([]Style{{FontFamily: "Test Serif", FontSize: 16}})
	if err := b.AddChapter(Chapter{ID: "dropped", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeImage, Image: 7}, // no width or height
	}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var delivered []Issue
	eng, err := New(Options{Workers: 2, OnIssue: func(i Issue) { delivered = append(delivered, i) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opts := statsTestOptions(testFontSet(t))
	l, err := eng.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	issues := l.Issues()
	if len(issues) != 1 {
		t.Fatalf("Issues() = %v, want one IssueDroppedContent", issues)
	}
	got := issues[0]
	if got.Code != IssueDroppedContent {
		t.Fatalf("issue code = %v, want IssueDroppedContent", got.Code)
	}
	if got.Chapter != "dropped" || got.Node != 1 {
		t.Fatalf("issue context = chapter %q node %d, want dropped/1", got.Chapter, got.Node)
	}
	if got.Detail == "" {
		t.Fatal("issue Detail is empty")
	}
	if len(delivered) != len(issues) || delivered[0] != got {
		t.Fatalf("OnIssue delivered %v, Issues() has %v", delivered, issues)
	}
	if s := got.String(); !strings.Contains(s, "dropped-content") || !strings.Contains(s, "dropped") {
		t.Fatalf("Issue.String() = %q", s)
	}
}
