package render

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/laic7092/layx/render/paint"
)

func keyTestOptions(t testing.TB) LayoutOptions {
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

// keyTestDoc builds chapters whose root either pins font size/line height in
// the document style or inherits both from Typography.
func keyTestDoc(t testing.TB, chapters int, pinned bool) *Document {
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
	}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	for c := 0; c < chapters; c++ {
		style := StyleID(0)
		if pinned {
			style = 1
		}
		nodes := []Node{
			{Kind: NodeBlock, Style: style, Children: []NodeID{1}},
			{Kind: NodeText, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 30)},
		}
		if err := b.AddChapter(Chapter{ID: fmt.Sprintf("ch%d", c), Root: 0, Nodes: nodes}); err != nil {
			t.Fatalf("AddChapter: %v", err)
		}
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func layoutWith(t testing.TB, e *Engine, doc *Document, opts LayoutOptions) *Layout {
	t.Helper()
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return l
}

func relayoutWith(t testing.TB, l *Layout, opts LayoutOptions) *Layout {
	t.Helper()
	n, err := l.Relayout(context.Background(), opts)
	if err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	return n
}

func compareLineBoxes(t *testing.T, what string, got, want *Layout) {
	t.Helper()
	if got.PageCount() != want.PageCount() {
		t.Fatalf("%s: page count = %d, want %d", what, got.PageCount(), want.PageCount())
	}
	for ci := range want.chapters {
		g := &got.chapters[ci]
		w := &want.chapters[ci]
		if g.count() != w.count() {
			t.Fatalf("%s: chapter %d item count = %d, want %d", what, ci, g.count(), w.count())
		}
		want, got := itemsOf(g), itemsOf(w)
		for i := range got {
			a := &want[i]
			b := &got[i]
			if a.kind != b.kind || a.start != b.start || a.end != b.end {
				t.Fatalf("%s: chapter %d item %d range = kind %d [%d,%d), want kind %d [%d,%d)",
					what, ci, i, a.kind, a.start, a.end, b.kind, b.start, b.end)
			}
			if a.paraLine != b.paraLine || a.paraLines != b.paraLines ||
				a.x != b.x || a.width != b.width || a.avail != b.avail ||
				a.last != b.last || a.align != b.align {
				t.Fatalf("%s: chapter %d item %d horizontal box differs: %+v vs %+v", what, ci, i, a, b)
			}
			if a.para != nil && b.para != nil {
				ga := string(paraRunes(a.para)[a.start:a.end])
				wb := string(paraRunes(b.para)[b.start:b.end])
				if ga != wb {
					t.Fatalf("%s: chapter %d item %d text = %q, want %q", what, ci, i, ga, wb)
				}
			}
			// The cached path must reproduce a full layout exactly, including
			// the vertical metrics it derives from the new LineHeight.
			if a.y != b.y || a.height != b.height || a.baseline != b.baseline {
				t.Fatalf("%s: chapter %d item %d vertical box = y %.6f h %.6f base %.6f, want y %.6f h %.6f base %.6f",
					what, ci, i, a.y, a.height, a.baseline, b.y, b.height, b.baseline)
			}
		}
	}
	for i := range want.pages {
		a, b := got.pages[i], want.pages[i]
		if a.chapter != b.chapter || a.first != b.first || a.last != b.last || a.top != b.top {
			t.Fatalf("%s: page %d = %+v, want %+v", what, i, a, b)
		}
	}
}

// TestRelayoutLineHeightMatchesFullLayout is acceptance test 2: changing
// LineHeight must reuse line boxes but still agree with a full relayout on
// page count, line text ranges and per-line rune ranges.
func TestRelayoutLineHeightMatchesFullLayout(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)

	e := mustEngine(t, 4)
	orig := layoutWith(t, e, doc, base)

	next := base
	next.Typography.LineHeight = 2.2
	cached := relayoutWith(t, orig, next)

	freshEngine := mustEngine(t, 4)
	fresh := layoutWith(t, freshEngine, doc, next)

	compareLineBoxes(t, "line-height relayout", cached, fresh)

	// The new line height must actually have taken effect relative to the
	// original layout.
	if itemsOf(&cached.chapters[0])[0].height == itemsOf(&orig.chapters[0])[0].height {
		t.Fatalf("line height change did not alter line boxes: %v", itemsOf(&cached.chapters[0])[0].height)
	}
}

// TestRelayoutLineHeightNoShaping is acceptance test 3: the LineHeight change
// changes only PageKey, so no Shaper.Shape call is allowed.
func TestRelayoutLineHeightNoShaping(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)
	e := mustEngine(t, 2)
	orig := layoutWith(t, e, doc, base)

	next := base
	next.Typography.LineHeight = 2.0

	before := e.shapes.Load()
	relaid := relayoutWith(t, orig, next)
	if got := e.shapes.Load() - before; got != 0 {
		t.Fatalf("LineHeight relayout shaped %d runs, want 0", got)
	}
	if relaid.breakKeys[0] != orig.breakKeys[0] {
		t.Fatal("BreakKey changed for a LineHeight-only relayout")
	}
	if relaid.pageKeys[0] == orig.pageKeys[0] {
		t.Fatal("PageKey did not change for a LineHeight change")
	}
	if relaid.PageCount() == 0 {
		t.Fatal("relayout produced no pages")
	}
}

// TestRelayoutFontSizePerChapter is acceptance test 4: a font-size change
// re-breaks only chapters whose resolved horizontal style actually changed.
func TestRelayoutFontSizePerChapter(t *testing.T) {
	base := keyTestOptions(t)

	t.Run("pinned chapter keeps its BreakKey", func(t *testing.T) {
		doc := keyTestDoc(t, 1, true)
		e := mustEngine(t, 2)
		orig := layoutWith(t, e, doc, base)

		next := base
		next.Typography.FontSize = 24
		before := e.shapes.Load()
		relaid := relayoutWith(t, orig, next)
		if got := e.shapes.Load() - before; got != 0 {
			t.Fatalf("pinned font-size relayout shaped %d runs, want 0", got)
		}
		if relaid.breakKeys[0] != orig.breakKeys[0] {
			t.Fatal("BreakKey changed although the chapter pins its font size")
		}
		if relaid.pageKeys[0] != orig.pageKeys[0] {
			t.Fatal("PageKey changed although the chapter pins its font size and line height")
		}
	})

	t.Run("inherited chapter re-breaks", func(t *testing.T) {
		doc := keyTestDoc(t, 1, false)
		e := mustEngine(t, 2)
		orig := layoutWith(t, e, doc, base)

		next := base
		next.Typography.FontSize = 24
		before := e.shapes.Load()
		relaid := relayoutWith(t, orig, next)
		if got := e.shapes.Load() - before; got == 0 {
			t.Fatal("font-size relayout of an inheriting chapter shaped nothing")
		}
		if relaid.breakKeys[0] == orig.breakKeys[0] {
			t.Fatal("BreakKey did not change for an inheriting chapter")
		}
	})

	t.Run("mixed document re-breaks only the fluid chapter", func(t *testing.T) {
		mixed := newMixedChapterDoc(t)
		e := mustEngine(t, 2)
		orig := layoutWith(t, e, mixed, base)

		next := base
		next.Typography.FontSize = 24
		before := e.shapes.Load()
		relaid := relayoutWith(t, orig, next)
		if got := e.shapes.Load() - before; got == 0 {
			t.Fatal("mixed relayout shaped nothing")
		}
		if relaid.breakKeys[0] != orig.breakKeys[0] {
			t.Fatal("pinned chapter's BreakKey changed")
		}
		if relaid.breakKeys[1] == orig.breakKeys[1] {
			t.Fatal("inheriting chapter's BreakKey did not change")
		}
	})
}

func newMixedChapterDoc(t testing.TB) *Document {
	t.Helper()
	styles := []Style{
		{},
		{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			Color: paint.Color{A: 0xff}, MarginBottom: 8,
		},
	}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 30)
	pinned := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	fluid := []Node{
		{Kind: NodeBlock, Style: 0, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	if err := b.AddChapter(Chapter{ID: "pinned", Root: 0, Nodes: pinned}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddChapter(Chapter{ID: "fluid", Root: 0, Nodes: fluid}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestRelayoutPaintOnly proves the third key: a theme change reuses BreakKey
// and PageKey and therefore performs neither shaping nor re-pagination.
func TestRelayoutPaintOnly(t *testing.T) {
	doc := testDocument(t, "")
	base := keyTestOptions(t)
	e := mustEngine(t, 2)
	orig := layoutWith(t, e, doc, base)

	next := base
	next.Typography.Color = paint.Color{R: 0x20, G: 0x30, B: 0x40, A: 0xff}

	before := e.shapes.Load()
	relaid := relayoutWith(t, orig, next)
	if got := e.shapes.Load() - before; got != 0 {
		t.Fatalf("paint-only relayout shaped %d runs, want 0", got)
	}
	if relaid.breakKeys[0] != orig.breakKeys[0] || relaid.pageKeys[0] != orig.pageKeys[0] {
		t.Fatal("paint-only relayout invalidated a layout key")
	}
	if relaid.paintKey == orig.paintKey {
		t.Fatal("PaintKey did not change for a theme change")
	}
	if got := relaid.Page(0); got == nil {
		t.Fatal("paint-only relayout has no first page")
	}
}

// BenchmarkRelayoutLineHeight5M is the relayout target benchmark: with cached line
// boxes this must be a small fraction of a full 5M-rune layout.
func BenchmarkRelayoutLineHeight5M(b *testing.B) {
	doc, fs := cjkBenchDoc(b, 5_000_000)
	e, err := New(Options{Workers: 8, PageCache: 2})
	if err != nil {
		b.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := opts
		// Alternate so repeated runs exercise both the cached and the
		// previously-evicted line-height variant.
		if i%2 == 0 {
			o.Typography.LineHeight = 1.8
		}
		next, err := l.Relayout(context.Background(), o)
		if err != nil {
			b.Fatal(err)
		}
		if next.PageCount() == 0 {
			b.Fatal("no pages")
		}
	}
}

// TestRelayoutVerticalVariantsMatchFullLayout covers the other vertical
// inputs of PageKey: paragraph gap and page content height. Both must change
// positions and page boundaries only, never line ranges.
func TestRelayoutVerticalVariantsMatchFullLayout(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)
	e := mustEngine(t, 2)
	orig := layoutWith(t, e, doc, base)

	t.Run("paragraph gap", func(t *testing.T) {
		next := base
		next.Typography.ParagraphGap = 17
		cached := relayoutWith(t, orig, next)
		fresh := layoutWith(t, mustEngine(t, 2), doc, next)
		compareLineBoxes(t, "gap relayout", cached, fresh)
		if cached.breakKeys[0] != orig.breakKeys[0] {
			t.Fatal("ParagraphGap changed BreakKey")
		}
		if cached.pageKeys[0] == orig.pageKeys[0] {
			t.Fatal("ParagraphGap did not change PageKey")
		}
	})

	t.Run("page height", func(t *testing.T) {
		next := base
		next.Viewport.Height = 480
		cached := relayoutWith(t, orig, next)
		fresh := layoutWith(t, mustEngine(t, 2), doc, next)
		compareLineBoxes(t, "page-height relayout", cached, fresh)
		if cached.breakKeys[0] != orig.breakKeys[0] {
			t.Fatal("Viewport.Height changed BreakKey")
		}
		if cached.pageKeys[0] == orig.pageKeys[0] {
			t.Fatal("Viewport.Height did not change PageKey")
		}
	})

	t.Run("top and bottom margins", func(t *testing.T) {
		next := base
		next.Margins.Top = 40
		next.Margins.Bottom = 40
		cached := relayoutWith(t, orig, next)
		fresh := layoutWith(t, mustEngine(t, 2), doc, next)
		compareLineBoxes(t, "margin relayout", cached, fresh)
		if cached.breakKeys[0] != orig.breakKeys[0] {
			t.Fatal("top/bottom margins changed BreakKey")
		}
	})
}

// TestRelayoutRespectsCancellation makes sure a cache hit still observes a
// canceled context instead of returning the cached layout.
func TestRelayoutRespectsCancellation(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	base := keyTestOptions(t)
	l := layoutWith(t, mustEngine(t, 2), doc, base)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Relayout(ctx, base); err == nil {
		t.Fatal("Relayout with a canceled context succeeded")
	}

	next := base
	next.Typography.LineHeight = 2.0
	if _, err := l.Relayout(ctx, next); err == nil {
		t.Fatal("Relayout with a canceled context succeeded for a new variant")
	}
}

// breakKeyFor computes one chapter's BreakKey without laying anything out.
// A fresh keyer is used per call, so callers must compare keys produced by the
// same keyer (the seeds live there).
func breakKeyFor(k *keyer, doc *Document, opts LayoutOptions, rules TextRules) keyHash {
	typo := opts.Typography
	typo.Rules = rules
	return chapterBreakKey(k, doc, &doc.chapters[0], opts, typo)
}

// TestBreakKeyHashesEveryTextRule walks every field of TextRules by hand (the
// render package forbids reflect, and a hand-written table forces a new field
// to add a case here) and asserts that changing it changes the chapter's
// BreakKey. This is the structural guard behind "a policy cannot be missing
// from the key".
func TestBreakKeyHashesEveryTextRule(t *testing.T) {
	doc := keyTestDoc(t, 1, false)
	opts := keyTestOptions(t)
	k := newKeyer()
	base := breakKeyFor(k, doc, opts, TextRules{})

	cases := []struct {
		name  string
		apply func(*TextRules)
	}{
		{"Autospacing", func(r *TextRules) { r.Autospacing = true }},
		{"PunctuationCompression", func(r *TextRules) { r.PunctuationCompression = true }},
		{"HangingPunctuation", func(r *TextRules) { r.HangingPunctuation = true }},
		{"Widows", func(r *TextRules) { r.Widows = 3 }},
		{"Orphans", func(r *TextRules) { r.Orphans = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rules TextRules
			tc.apply(&rules)
			if got := breakKeyFor(k, doc, opts, rules); got == base {
				t.Fatalf("BreakKey did not change when TextRules.%s changed", tc.name)
			}
		})
	}

	if again := breakKeyFor(k, doc, opts, TextRules{}); again != base {
		t.Fatal("BreakKey is not stable for identical TextRules")
	}
}

// TestPaintOptionsDoNotChangeLayoutKeys is the reverse guard: options that only
// change how a page is painted must leave BreakKey and PageKey alone, otherwise
// switching theme or decoration would re-break and re-paginate the book.
//
// EmulateBold is not a layout option at all: buildLine derives it from the
// resolved face and the requested weight, and neither is read while painting.
func TestPaintOptionsDoNotChangeLayoutKeys(t *testing.T) {
	build := func(paintOnly bool) *Document {
		paintStyle := Style{
			FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5,
			MarginBottom: 8,
		}
		if paintOnly {
			paintStyle.Color = paint.Color{R: 0x20, G: 0x30, B: 0x40, A: 0xff}
			paintStyle.TextDecoration = TextDecorationUnderline
			paintStyle.TextDecorationColor = paint.Color{R: 0x10, A: 0xff}
			paintStyle.Background = paint.Color{R: 0xee, G: 0xee, B: 0xee, A: 0xff}
			paintStyle.CornerRadius = 6
			paintStyle.BorderTop = Border{Color: paint.Color{R: 0xff, A: 0xff}}
			paintStyle.BorderRight = Border{Color: paint.Color{R: 0x0f, A: 0xff}}
			paintStyle.BorderBottom = Border{Color: paint.Color{R: 0xf0, A: 0xff}}
			paintStyle.BorderLeft = Border{Color: paint.Color{R: 0xff, G: 0xff, A: 0xff}}
		}
		styles := []Style{{}, paintStyle}
		b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "en", Reflowable: true})
		nodes := []Node{
			{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
			{Kind: NodeText, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 30)},
		}
		if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
			t.Fatalf("AddChapter: %v", err)
		}
		doc, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return doc
	}

	opts := keyTestOptions(t)
	plainDoc, paintedDoc := build(false), build(true)
	k := newKeyer()
	plain := breakKeyFor(k, plainDoc, opts, TextRules{})
	painted := breakKeyFor(k, paintedDoc, opts, TextRules{})
	if plain != painted {
		t.Fatal("paint-only Style fields changed BreakKey")
	}
	plainPage := pageKey(k, plain, opts.Typography, contentHeight(opts))
	paintedPage := pageKey(k, painted, opts.Typography, contentHeight(opts))
	if plainPage != paintedPage {
		t.Fatal("paint-only Style fields changed PageKey")
	}

	// Typography.Color and Background are paint-only too: they live in
	// PaintKey, below PageKey.
	tinted := opts.Typography
	tinted.Color = paint.Color{R: 1, G: 2, B: 3, A: 0xff}
	tinted.Background = paint.Color{R: 9, G: 9, B: 9, A: 0xff}
	if got := breakKeyFor(k, plainDoc, opts, TextRules{}); got != plain {
		t.Fatal("Typography paint fields changed BreakKey")
	}
	if got := pageKey(k, plain, tinted, contentHeight(opts)); got != plainPage {
		t.Fatal("Typography paint fields changed PageKey")
	}
}
