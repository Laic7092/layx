package render

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"github.com/laic7092/layx/render/paint"
	"golang.org/x/image/math/fixed"
)

// complexTextDocument builds the audit §2.1 document shape: one block with one
// text node that may contain newlines and complex scripts.
func complexTextDocument(t testing.TB, text string) *Document {
	t.Helper()
	b := NewDocumentBuilder().SetStyles([]Style{
		{},
		{FontFamily: "Test Serif", FontSize: 18},
	})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Style: 1, Text: text},
	}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func complexTextOptions(t testing.TB, fonts *FontSet) LayoutOptions {
	t.Helper()
	return LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   18,
		},
		Fonts: fonts,
	}
}

// assertLineCoverage verifies the property the audit §2.1 panic broke: every
// paragraph's line boxes must cover every rune of that paragraph, so the last
// line ends exactly at the paragraph's rune count. A shaped run list that stops
// early (or a wrapper that silently drops the tail) shows up here.
func assertLineCoverage(t *testing.T, l *Layout, text string) {
	t.Helper()
	paragraphs := 0
	for ci := range l.chapters {
		cf := &l.chapters[ci]
		for ui := range cf.units {
			u := cf.units[ui].unit
			if u == nil || u.para == nil || u.para.runeLen == 0 {
				continue
			}
			paragraphs++
			if len(u.lines) == 0 {
				t.Fatalf("%q: chapter %d has a paragraph with no lines", text, ci)
			}
			if got := u.lines[len(u.lines)-1].end; got != u.para.runeLen {
				t.Fatalf("%q: last line ends at rune %d, paragraph has %d runes",
					text, got, u.para.runeLen)
			}
		}
	}
	if text != "" && paragraphs == 0 {
		t.Fatalf("%q: layout produced no paragraph line boxes", text)
	}
}

// TestPanicReproAudit is the exact reproducer from docs/audit.md §7: it used to
// panic in go-text's line wrapper on "abc\nالعربية" and in hitTestItems on an
// empty text node.
func TestPanicReproAudit(t *testing.T) {
	fonts := testFontSet(t)
	eng := mustEngine(t, 2)
	opts := complexTextOptions(t, fonts)

	// 1) Multi-paragraph text with a script that needs the full segmenter.
	l, err := eng.Layout(context.Background(), complexTextDocument(t, "abc\nالعربية"), opts)
	if err != nil {
		t.Fatalf("Layout(abc\\nالعربية): %v", err)
	}
	assertLineCoverage(t, l, "abc\nالعربية")

	// 2) Empty text node: hit testing is a miss, not a panic.
	l, err = eng.Layout(context.Background(), complexTextDocument(t, ""), opts)
	if err != nil {
		t.Fatalf("Layout(empty): %v", err)
	}
	if l.PageCount() == 0 {
		t.Fatal("empty document produced no page")
	}
	if loc, ok := l.Page(0).HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Page.HitTest on empty text = %+v, %v; want zero, false", loc, ok)
	}
	if loc, ok := l.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Layout.HitTest on empty text = %+v, %v; want zero, false", loc, ok)
	}
}

// TestComplexScriptLayoutMatrix is the audit §2.1 hit matrix. Every entry must
// lay out without panicking, and (for the visible scripts) without losing the
// tail of the text.
func TestComplexScriptLayoutMatrix(t *testing.T) {
	fonts := testFontSet(t)
	eng := mustEngine(t, 4)
	opts := complexTextOptions(t, fonts)

	texts := []string{
		// The audit matrix.
		"",
		"abc\ndef",
		"abc\nαβγ",
		"abc\nпривет",
		"abc\n😀",
		"abc\nالعربية",
		"abc\nשלום",
		"abc\nनमस्ते",
		"abc\nภาษาไทย",
		"abc\n\u202e",
		"العربية",
		"\nالعربية",
		// Bidi paragraph separator variants and mixed directions.
		"a\r\nb",
		"a\rb",
		"a\u001cb",
		"a\u001db",
		"a\u001eb",
		"a\u0085b",
		"a\u2029b",
		"\u2029",
		"\r\n",
		"\n",
		"abc\n\ndef",
		"😀\n😀",
		"LTR العربية\nעברית 123",
		"العربية abc\nภาษาไทย xyz",
	}
	for _, text := range texts {
		text := text
		t.Run(shortTextName(text), func(t *testing.T) {
			l, err := eng.Layout(context.Background(), complexTextDocument(t, text), opts)
			if err != nil {
				t.Fatalf("Layout: %v", err)
			}
			if l.PageCount() == 0 {
				t.Fatal("no pages")
			}
			assertLineCoverage(t, l, text)

			// Drawing must work too: the draw path shapes individual lines
			// again and would hit any remaining rune→glyph hole.
			p := l.Page(0)
			if p == nil {
				t.Fatal("Page(0) is nil")
			}
			p.Draw(&posPainter{})

			// Selection of the whole node must cover the trailing text.
			if hasLetter(text) {
				n := len([]rune(text))
				quads := l.Selection(
					Location{Chapter: 0, Node: 1, Offset: 0},
					Location{Chapter: 0, Node: 1, Offset: n},
				)
				if len(quads) == 0 {
					t.Fatalf("full-node selection returned no quads for %q", text)
				}
			}
			// An empty node has no flow item, so Progress is legitimately
			// unresolvable; for real text the end of the node must resolve.
			if text != "" {
				if _, ok := l.Progress(Location{Chapter: 0, Node: 1, Offset: len([]rune(text))}); !ok {
					t.Fatalf("Progress at end of text not resolved for %q", text)
				}
			}
		})
	}
}

// shortTextName builds a readable subtest name for arbitrary text.
func shortTextName(text string) string {
	if text == "" {
		return "empty"
	}
	quoted := strconv.Quote(text)
	if len(quoted) > 40 {
		quoted = quoted[:37] + `..."`
	}
	return quoted
}

// hasLetter reports whether text contains a rune with visible ink. Control- and
// format-only strings are skipped by selection assertions.
func hasLetter(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// TestShapeRangeCoverage asserts the invariant quoted in the audit: shaped runs
// must cover [0, len(wrapText)). It exercises the worker directly so it also
// covers the fallback path where a segment list leaves a hole.
func TestShapeRangeCoverage(t *testing.T) {
	fonts := testFontSet(t)
	w := newTextWorker(fonts, Typography{FontFamily: "Test Serif", FontSize: 18}, nil)

	texts := []string{
		"abc\nالعربية",
		"abc\nภาษาไทย",
		"abc\n\u202e",
		"a\r\nb\u2029c\u0085d\u001ce\u001df\u001eg",
		"العربية",
		"abc\ndef",
		"😀\n😀",
	}
	for _, text := range texts {
		runes := []rune(text)
		p := &paragraph{
			segs: []paraSeg{{
				node: 1, style: Style{FontFamily: "Test Serif", FontSize: 18},
				text: text, start: 0, end: int32(len(runes)),
			}},
			runeLen: int32(len(runes)),
		}
		runs := w.shapeRange(p, runes, 0, len(runes), language.NewLanguage("en"))
		assertShapedCoverage(t, runs, 0, len(runes), text)
	}

	// A hole between segments must be filled with a placeholder shaped run
	// instead of returning a short list: this is the defensive layer below the
	// splitInput fix.
	runes := []rune("abcdef")
	p := &paragraph{
		segs: []paraSeg{
			{node: 1, style: Style{FontFamily: "Test Serif", FontSize: 18}, text: "ab", start: 0, end: 2},
			{node: 2, style: Style{FontFamily: "Test Serif", FontSize: 18}, text: "ef", start: 4, end: 6},
		},
		runeLen: 6,
	}
	runs := w.shapeRange(p, runes, 0, len(runes), language.NewLanguage("en"))
	assertShapedCoverage(t, runs, 0, len(runes), "segments-with-a-hole")
	foundGapRun := false
	for _, r := range runs {
		if r.out.Runes.Offset == 2 && r.out.Runes.Count == 2 {
			foundGapRun = true
		}
	}
	if !foundGapRun {
		t.Fatalf("no placeholder run covers the [2,4) gap: %v", runOffsets(runs))
	}
}

// runOffsets renders the shaped run ranges for failure messages.
func runOffsets(runs []shapedRun) []shaping.Range {
	out := make([]shaping.Range, len(runs))
	for i := range runs {
		out[i] = runs[i].out.Runes
	}
	return out
}

// assertShapedCoverage asserts that the shaped runs cover every rune of
// [start,end) exactly once.
func assertShapedCoverage(t *testing.T, runs []shapedRun, start, end int, context string) {
	t.Helper()
	seen := make([]int, end-start)
	for _, r := range runs {
		lo, hi := r.out.Runes.Offset, r.out.Runes.Offset+r.out.Runes.Count
		if r.out.Runes.Count <= 0 || lo < start || hi > end {
			t.Fatalf("%q: run %v outside [%d,%d)", context, r.out.Runes, start, end)
		}
		for i := lo; i < hi; i++ {
			seen[i-start]++
		}
	}
	for i, n := range seen {
		if n != 1 {
			t.Fatalf("%q: rune %d covered %d times, runs %v", context, start+i, n, runOffsets(runs))
		}
	}
}

// TestSplitInputCoverage checks the segmentation fix directly: for any text,
// splitInput must return runs covering [RunStart,RunEnd) with no gap and no
// overlap. Before the fix, go-text's Segmenter truncated the output at the first
// bidi paragraph separator.
func TestSplitInputCoverage(t *testing.T) {
	fonts := testFontSet(t)
	w := newTextWorker(fonts, Typography{FontFamily: "Test Serif", FontSize: 18}, nil)
	fm := workerFontmap{faces: w.faces, order: w.order(Style{FontFamily: "Test Serif"})}

	texts := []string{
		"abc\nالعربية",
		"abc\nภาษาไทย",
		"abc\n\u202e",
		"abc\ndef",
		"a\r\nb",
		"\r\n\r\n",
		"\u2029",
		"a\u001cb\u001db\u001eb",
		"a\u0085b",
		"abc\n\ndef",
		"العربية\nעברית",
		"只是测试\nمرحبا",
	}
	for _, text := range texts {
		runes := []rune(text)
		in := shaping.Input{
			Text:      runes,
			RunStart:  0,
			RunEnd:    len(runes),
			Direction: di.DirectionLTR,
			Size:      fixed.I(18),
			Language:  language.NewLanguage("en"),
		}
		// Exercise both the full segmenter path and the LTR fast path.
		for name, runs := range map[string][]shaping.Input{
			"complex":  w.splitComplex(in, fm),
			"dispatch": w.splitInput(in, fm),
		} {
			covered := make([]int, len(runes))
			for _, r := range runs {
				if r.RunStart >= r.RunEnd {
					t.Fatalf("%q/%s: empty run %+v", text, name, r)
				}
				if r.RunStart < 0 || r.RunEnd > len(runes) {
					t.Fatalf("%q/%s: run %+v outside [0,%d)", text, name, r, len(runes))
				}
				for i := r.RunStart; i < r.RunEnd; i++ {
					covered[i]++
				}
			}
			for i, n := range covered {
				if n != 1 {
					t.Fatalf("%q/%s: rune %d covered %d times by %+v", text, name, i, n, runs)
				}
			}
		}
	}
}

// TestSplitInputCopyOutOfSegmenter checks the copy: the segmenter invalidates
// its result on the next Split, so a run captured from one call must still
// describe that call after another segmentation ran.
func TestSplitInputCopyOutOfSegmenter(t *testing.T) {
	fonts := testFontSet(t)
	w := newTextWorker(fonts, Typography{FontFamily: "Test Serif", FontSize: 18}, nil)
	fm := workerFontmap{faces: w.faces, order: w.order(Style{FontFamily: "Test Serif"})}

	first := []rune("abc\nالعربية")
	in := shaping.Input{Text: first, RunStart: 0, RunEnd: len(first), Direction: di.DirectionLTR, Size: fixed.I(18), Language: language.NewLanguage("en")}
	saved := append([]shaping.Input(nil), w.splitComplex(in, fm)...)

	second := []rune("abcd efgh")
	in2 := shaping.Input{Text: second, RunStart: 0, RunEnd: len(second), Direction: di.DirectionLTR, Size: fixed.I(18), Language: language.NewLanguage("en")}
	w.splitComplex(in2, fm)

	for _, r := range saved {
		if r.RunEnd > len(first) || r.RunStart < 0 || r.RunEnd <= r.RunStart {
			t.Fatalf("saved run %+v corrupted by the next Split call", r)
		}
	}
	if len(saved) < 2 {
		t.Fatalf("got %d runs for %q; the fix should split at the newline", len(saved), "abc\nالعربية")
	}
}

// TestEmptyTextHitTest covers audit §2.2 in both modes: an empty text node must
// make Page.HitTest and Layout.HitTest miss instead of indexing unitOf[0].
func TestEmptyTextHitTest(t *testing.T) {
	fonts := testFontSet(t)
	eng := mustEngine(t, 2)
	opts := complexTextOptions(t, fonts)
	doc := complexTextDocument(t, "")

	// Paged mode.
	l, err := eng.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("paged Layout: %v", err)
	}
	if loc, ok := l.Page(0).HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Page.HitTest = %+v, %v; want zero, false", loc, ok)
	}
	if loc, ok := l.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Layout.HitTest (paged) = %+v, %v; want zero, false", loc, ok)
	}

	// Scroll mode.
	scrollOpts := opts
	scrollOpts.Mode = ModeScroll
	scrollOpts.TileHeight = 80
	sl, err := eng.Layout(context.Background(), doc, scrollOpts)
	if err != nil {
		t.Fatalf("scroll Layout: %v", err)
	}
	// Content height is zero, so there may be no tile at all; hit testing still
	// has to answer with a miss rather than a panic.
	if n := sl.TileCount(); n > 0 {
		if tile := sl.Tile(0); tile == nil {
			t.Fatal("Tile(0) is nil")
		} else {
			tile.Draw(&posPainter{})
		}
	}
	if loc, ok := sl.HitTest(paint.Point{X: 10, Y: 10}); ok || loc != (Location{}) {
		t.Fatalf("Layout.HitTest (scroll) = %+v, %v; want zero, false", loc, ok)
	}
}

// currentGoroutineID returns the calling goroutine's id. Tests use it to make a
// recovered panic happen on a helper goroutine deterministically.
func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	id, _ := strconv.ParseUint(s, 10, 64)
	return id
}

// TestParallelForPanicContract is the audit §2 "worker panic" contract: a panic
// inside a task is recovered on the calling goroutine and on helper goroutines,
// the phase returns an error wrapping ErrInternal, and the engine's budget is
// back to its full size afterwards.
func TestParallelForPanicContract(t *testing.T) {
	cases := []struct {
		name    string
		workers int
		n       int
		measure bool
	}{
		{"caller-single", 1, 1, false},
		{"caller-sequential", 1, 8, false},
		{"helpers-measured", 8, 64, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(Options{Workers: tc.workers, Measure: tc.measure})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = e.parallelFor(context.Background(), tc.n, func(i int) {
				panic("task boom")
			})
			if !errors.Is(err, ErrInternal) {
				t.Fatalf("parallelFor = %v, want ErrInternal", err)
			}
			if !strings.Contains(err.Error(), "task boom") {
				t.Fatalf("error %q lost the panic value", err)
			}
			if got, want := e.budget.avail(), e.workers; got != want {
				t.Fatalf("budget tokens after panic = %d, want %d", got, want)
			}
			if got := e.active.Load(); got != 0 {
				t.Fatalf("active phases after panic = %d, want 0", got)
			}
		})
	}
}

// TestParallelForHelperPanic forces the panic onto a helper goroutine: the
// calling goroutine's ids are known, so a task claimed by anyone else panics.
func TestParallelForHelperPanic(t *testing.T) {
	e, err := New(Options{Workers: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caller := currentGoroutineID()
	var helperRan atomic.Bool
	err = e.parallelFor(context.Background(), 256, func(i int) {
		if currentGoroutineID() == caller {
			// Keep the caller busy so helpers pick up work; it stops quickly
			// once the panic is recorded.
			time.Sleep(100 * time.Microsecond)
			return
		}
		helperRan.Store(true)
		panic("helper boom")
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("parallelFor = %v, want ErrInternal", err)
	}
	if !helperRan.Load() {
		t.Fatal("no task ran on a helper goroutine; test did not exercise the helper path")
	}
	if got, want := e.budget.avail(), e.workers; got != want {
		t.Fatalf("budget tokens after helper panic = %d, want %d", got, want)
	}
}

// TestParallelForStopsAfterPanic checks that no new task is claimed after the
// first panic (already-running tasks may finish).
func TestParallelForStopsAfterPanic(t *testing.T) {
	e, err := New(Options{Workers: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var started atomic.Int64
	err = e.parallelFor(context.Background(), 1_000_000, func(i int) {
		started.Add(1)
		if i == 0 {
			panic("stop")
		}
		time.Sleep(50 * time.Microsecond)
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("parallelFor = %v, want ErrInternal", err)
	}
	if n := started.Load(); n > 64 {
		t.Fatalf("%d tasks started after a panic with help available; the phase should stop", n)
	}
}

// TestRecoverInto covers the calling-goroutine panic helper.
func TestRecoverInto(t *testing.T) {
	var err error
	func() {
		defer recoverInto(&err)
		panic("recoverInto boom")
	}()
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("recoverInto error = %v, want ErrInternal", err)
	}
	if !strings.Contains(err.Error(), "recoverInto boom") {
		t.Fatalf("recoverInto error %q lost the panic value", err)
	}

	// A normal return must not touch the error.
	var nilErr error
	func() {
		defer recoverInto(&nilErr)
	}()
	if nilErr != nil {
		t.Fatalf("recoverInto on a clean return = %v", nilErr)
	}
}

// TestParallelForCancelled checks the precedence rule: a cancelled context is
// returned even if a task panicked.
func TestParallelForCancelled(t *testing.T) {
	e := mustEngine(t, 4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := e.parallelFor(ctx, 8, func(i int) { panic("never runs") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parallelFor on cancelled context = %v, want context.Canceled", err)
	}
	if got, want := e.budget.avail(), e.workers; got != want {
		t.Fatalf("budget tokens = %d, want %d", got, want)
	}
}

// TestEngineUsableAfterPanic checks the state-consistency part of the contract:
// after a recovered panic the same Engine must still lay out correctly.
func TestEngineUsableAfterPanic(t *testing.T) {
	fonts := testFontSet(t)
	e := mustEngine(t, 4)
	opts := complexTextOptions(t, fonts)
	doc := complexTextDocument(t, "abc\ndef")

	err := e.parallelFor(context.Background(), 32, func(i int) { panic("boom") })
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("parallelFor = %v, want ErrInternal", err)
	}

	got, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("Layout after panic: %v", err)
	}
	want, err := mustEngine(t, 4).Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("reference Layout: %v", err)
	}
	if got.PageCount() != want.PageCount() {
		t.Fatalf("page count after panic = %d, want %d", got.PageCount(), want.PageCount())
	}
	compareLayouts(t, want, got)
}

// TestMissingGlyphIssues covers the §3.3 diagnostic for text no configured face
// can draw, end to end through Layout/OnIssue.
func TestMissingGlyphIssues(t *testing.T) {
	fonts := testFontSet(t)
	var issues []Issue
	e, err := New(Options{
		Workers: 2,
		OnIssue: func(i Issue) { issues = append(issues, i) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Emoji are not in DejaVuSerif: layout continues, but the missing glyphs
	// must be visible to the host.
	l, err := e.Layout(context.Background(), complexTextDocument(t, "abc😀"), complexTextOptions(t, fonts))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	found := false
	for _, is := range issues {
		if is.Code != IssueMissingGlyphs {
			continue
		}
		if is.Chapter != "c" {
			t.Fatalf("IssueMissingGlyphs chapter = %q, want %q", is.Chapter, "c")
		}
		if is.Node != 1 {
			t.Fatalf("IssueMissingGlyphs node = %d, want 1", is.Node)
		}
		if !strings.Contains(is.Detail, "U+1F600") {
			t.Fatalf("IssueMissingGlyphs detail %q does not name U+1F600", is.Detail)
		}
		found = true
	}
	if !found {
		t.Fatalf("no IssueMissingGlyphs; got %v", issues)
	}
	// Drawing is a pure replay and the per-line shaping path must not report
	// again: a reader that repaints a page should not observe new issues.
	before := len(issues)
	if page := l.Page(0); page != nil {
		page.Draw(&posPainter{})
	}
	if got := len(issues); got != before {
		t.Fatalf("drawing reported %d extra issues; diagnostics must only come from the break path", got-before)
	}
	// The end-of-layout list must agree with the callback.
	if list := l.Issues(); len(list) == 0 {
		t.Fatalf("Layout.Issues() is empty; callback saw %v", issues)
	}

	// Control characters, newlines and default ignorables shape as .notdef or
	// nothing at all on purpose and must not be reported.
	var clean []Issue
	e3, err := New(Options{Workers: 2, OnIssue: func(i Issue) { clean = append(clean, i) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e3.Layout(context.Background(), complexTextDocument(t, "abc\ndef\u200d"), complexTextOptions(t, fonts)); err != nil {
		t.Fatalf("Layout: %v", err)
	}
	for _, is := range clean {
		if is.Code == IssueMissingGlyphs {
			t.Fatalf("false-positive IssueMissingGlyphs for control/ignorable text: %v", is)
		}
	}
}

// TestMissingFontIssue covers IssueMissingFont for a Style.FontFamily with no
// face in the set.
func TestMissingFontIssue(t *testing.T) {
	fonts := testFontSet(t)
	var issues []Issue
	e, err := New(Options{Workers: 2, OnIssue: func(i Issue) { issues = append(issues, i) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b := NewDocumentBuilder().SetStyles([]Style{
		{},
		{FontFamily: "No Such Family", FontSize: 18},
	})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Style: 1, Text: "hello"},
	}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := e.Layout(context.Background(), doc, complexTextOptions(t, fonts)); err != nil {
		t.Fatalf("Layout: %v", err)
	}
	found := false
	for _, is := range issues {
		if is.Code == IssueMissingFont && is.Chapter == "c" && is.Node == 1 {
			if !strings.Contains(is.Detail, "No Such Family") {
				t.Fatalf("IssueMissingFont detail %q does not name the family", is.Detail)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no IssueMissingFont; got %v", issues)
	}
	// A family that is present must not be reported.
	var clean []Issue
	e2, err := New(Options{Workers: 2, OnIssue: func(i Issue) { clean = append(clean, i) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e2.Layout(context.Background(), complexTextDocument(t, "hello"), complexTextOptions(t, fonts)); err != nil {
		t.Fatalf("Layout: %v", err)
	}
	for _, is := range clean {
		if is.Code == IssueMissingFont {
			t.Fatalf("false-positive IssueMissingFont: %v", is)
		}
	}
}

// panicPainter panics from its drawing methods. It exists to pin the documented
// Page.Draw contract: host painter panics are the host's to handle and the
// library must not swallow them.
type panicPainter struct {
	posPainter
}

func (p *panicPainter) FillRect(paint.Rect, paint.Color) { panic("host painter boom") }

func (p *panicPainter) DrawGlyphs(paint.GlyphRun) { panic("host painter boom") }

// TestPageDrawDoesNotRecoverPainterPanic pins the other half of the panic
// contract (audit §4): the library recovers its own worker panics, but a panic
// raised by host paint code must propagate to the caller of Page.Draw.
func TestPageDrawDoesNotRecoverPainterPanic(t *testing.T) {
	fonts := testFontSet(t)
	eng := mustEngine(t, 2)
	l, err := eng.Layout(context.Background(), complexTextDocument(t, "abc\ndef"), complexTextOptions(t, fonts))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	p := l.Page(0)
	if p == nil || len(p.ops) == 0 {
		t.Fatal("page has no display list to draw")
	}
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		p.Draw(&panicPainter{})
	}()
	if !panicked {
		t.Fatal("Page.Draw swallowed the host painter panic")
	}
}
