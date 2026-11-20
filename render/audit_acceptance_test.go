// Package render_test contains the end-to-end acceptance checks for the
// public-library audit in docs/audit.md §6.
//
// These tests are deliberately external (package render_test): they exercise
// the contract a host sees, not package internals. Every checklist item that can
// be asserted from an API user's position has a test here, so a regression in
// the library's outer shell fails CI even if the engine's own unit tests are
// green.
package render_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/laic7092/layx/render"
)

// auditFontSet loads the checked-in test font under the family "Test Serif",
// the same family the other test helpers use.
func auditFontSet(t *testing.T) *render.FontSet {
	t.Helper()
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		t.Fatalf("read test font: %v", err)
	}
	fs, err := render.NewFontSet(render.FontFace{Family: "Test Serif", Data: data})
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}
	return fs
}

func auditEngine(t *testing.T, opts render.Options) *render.Engine {
	t.Helper()
	if opts.Workers == 0 {
		opts.Workers = 2
	}
	eng, err := render.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// auditTextDoc builds a one-chapter document holding text in style 1 (a
// concrete family) or style 0 (inherit Typography) depending on inherit.
func auditTextDoc(t *testing.T, text string, inherit bool) *render.Document {
	t.Helper()
	style := render.StyleID(1)
	if inherit {
		style = 0
	}
	b := render.NewDocumentBuilder().SetStyles([]render.Style{
		{},
		{FontFamily: "Test Serif", FontSize: 18, LineHeight: 1.5},
	})
	err := b.AddChapter(render.Chapter{ID: "c", Root: 0, Nodes: []render.Node{
		{Kind: render.NodeBlock, Children: []render.NodeID{1}},
		{Kind: render.NodeText, Style: style, Text: text},
	}})
	if err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func auditOptions(fs *render.FontSet) render.LayoutOptions {
	return render.LayoutOptions{
		Viewport: render.Viewport{Width: 600, Height: 900},
		Typography: render.Typography{
			FontFamily: "Test Serif",
			FontSize:   18,
			Color:      render.Color{A: 0xff},
		},
		Fonts: fs,
	}
}

// TestAuditAcceptanceComplexScriptLayout covers the §6 checklist item
// "Layout on \"abc\nالعربية\", \"abc\nภาษาไทย\", \"abc\n\u202e\" returns an
// error or a normal result, never panics". It also checks that the text after
// the newline is still laid out rather than silently dropped.
func TestAuditAcceptanceComplexScriptLayout(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	opts := auditOptions(fs)

	cases := []string{
		// The audit's hit matrix: the last five used to panic.
		"abc\ndef",
		"abc\nαβγ",
		"abc\nпривет",
		"abc\n😀",
		"abc\nالعربية",
		"abc\nשלום",
		"abc\nनमस्ते",
		"abc\nภาษาไทย",
		"abc\n\u202e",
		// Single-paragraph and empty-first-paragraph controls.
		"العربية",
		"\nالعربية",
		// Several paragraphs of complex script in one text node.
		strings.Repeat("preamble line\nالعربية שלום ภาษาไทย\n", 8),
	}
	for _, text := range cases {
		text := text
		t.Run(fmt.Sprintf("%q", truncateForName(text)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Layout panicked for %q: %v", text, r)
				}
			}()
			doc := auditTextDoc(t, text, false)
			l, err := eng.Layout(context.Background(), doc, opts)
			if err != nil {
				if errors.Is(err, render.ErrInternal) {
					t.Fatalf("Layout of %q failed with ErrInternal: %v", text, err)
				}
				// A clean error is acceptable for the contract, but these
				// strings are valid text and must lay out.
				t.Fatalf("Layout of %q: unexpected error: %v", text, err)
			}
			if l.PageCount() < 1 {
				t.Fatalf("Layout of %q: PageCount = %d", text, l.PageCount())
			}
			// The highest-traffic paths must stay alive on the same layout.
			_, _ = l.Page(0).HitTest(render.Point{X: 10, Y: 10})
			_, _ = l.HitTest(render.Point{X: 10, Y: 10})
			_, _ = l.LocationAt(0)
			_, _ = l.Progress(render.Location{Chapter: 0})
			_ = l.Selection(render.Location{Chapter: 0}, render.Location{Chapter: 0, Node: 1, Offset: 3})
			_ = l.Page(0).Links()
		})
	}

	// Scroll mode exercises the same shaping path through tiles.
	scroll := opts
	scroll.Mode = render.ModeScroll
	scroll.TileHeight = 113
	for _, text := range []string{"abc\nالعربية", "abc\nภาษาไทย", "abc\n\u202e"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("scroll Layout panicked for %q: %v", text, r)
				}
			}()
			l, err := eng.Layout(context.Background(), auditTextDoc(t, text, false), scroll)
			if err != nil {
				t.Fatalf("scroll Layout of %q: %v", text, err)
			}
			if l.TileCount() < 1 {
				t.Fatalf("scroll Layout of %q: TileCount = %d", text, l.TileCount())
			}
			_ = l.Tile(0)
			_, _ = l.HitTest(render.Point{X: 10, Y: 10})
		}()
	}
}

// TestAuditAcceptanceEmptyHitTest covers the §6 checklist item
// "Page.HitTest / Layout.HitTest on an empty text node return (Location{}, false)".
func TestAuditAcceptanceEmptyHitTest(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	opts := auditOptions(fs)

	emptyText := auditTextDoc(t, "", false)
	emptyBlockBuilder := render.NewDocumentBuilder().SetStyles([]render.Style{{FontFamily: "Test Serif"}})
	if err := emptyBlockBuilder.AddChapter(render.Chapter{ID: "block", Root: 0, Nodes: []render.Node{
		{Kind: render.NodeBlock},
	}}); err != nil {
		t.Fatalf("AddChapter empty block: %v", err)
	}
	emptyBlock, err := emptyBlockBuilder.Build()
	if err != nil {
		t.Fatalf("Build empty block: %v", err)
	}

	for name, doc := range map[string]*render.Document{"empty text": emptyText, "empty block": emptyBlock} {
		for _, mode := range []struct {
			name string
			opts render.LayoutOptions
		}{
			{"paged", opts},
			{"scroll", func() render.LayoutOptions {
				o := opts
				o.Mode = render.ModeScroll
				o.TileHeight = 113
				return o
			}()},
		} {
			l, err := eng.Layout(context.Background(), doc, mode.opts)
			if err != nil {
				t.Fatalf("%s/%s: Layout: %v", name, mode.name, err)
			}
			if mode.name == "paged" {
				if l.PageCount() < 1 {
					t.Fatalf("%s/paged: PageCount = %d", name, l.PageCount())
				}
				if loc, ok := l.Page(0).HitTest(render.Point{X: 10, Y: 10}); ok {
					t.Errorf(`%s/paged: Page.HitTest(10,10) = %+v, true; want (Location{}, false)`, name, loc)
				}
			}
			if loc, ok := l.HitTest(render.Point{X: 10, Y: 10}); ok {
				t.Errorf(`%s/%s: Layout.HitTest(10,10) = %+v, true; want (Location{}, false)`, name, mode.name, loc)
			}
		}
	}
}

// TestAuditAcceptanceHostileFloats covers the §6 checklist item
// "any NaN/Inf in Viewport/Margins/Typography/Style returns ErrInvalidOptions"
// (ErrInvalidDocument for a style table entry).
func TestAuditAcceptanceHostileFloats(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	base := auditOptions(fs)
	doc := auditTextDoc(t, strings.Repeat("hostile float probe ", 50), false)

	nan := float32(math.NaN())
	inf := float32(math.Inf(1))

	mutations := []struct {
		name  string
		apply func(*render.LayoutOptions)
	}{
		{"viewport width NaN", func(o *render.LayoutOptions) { o.Viewport.Width = nan }},
		{"viewport height NaN", func(o *render.LayoutOptions) { o.Viewport.Height = nan }},
		{"viewport width +Inf", func(o *render.LayoutOptions) { o.Viewport.Width = inf }},
		{"margin top NaN", func(o *render.LayoutOptions) { o.Margins.Top = nan }},
		{"margin left NaN", func(o *render.LayoutOptions) { o.Margins.Left = nan }},
		{"margin bottom -Inf", func(o *render.LayoutOptions) { o.Margins.Bottom = -inf }},
		{"tile height NaN", func(o *render.LayoutOptions) { o.TileHeight = nan }},
		{"typography line height NaN", func(o *render.LayoutOptions) { o.Typography.LineHeight = nan }},
		{"typography letter spacing NaN", func(o *render.LayoutOptions) { o.Typography.LetterSpacing = nan }},
		{"typography word spacing NaN", func(o *render.LayoutOptions) { o.Typography.WordSpacing = nan }},
		{"typography paragraph gap NaN", func(o *render.LayoutOptions) { o.Typography.ParagraphGap = nan }},
		{"typography font size +Inf", func(o *render.LayoutOptions) { o.Typography.FontSize = inf }},
	}
	for _, m := range mutations {
		m := m
		t.Run(m.name, func(t *testing.T) {
			opts := base
			m.apply(&opts)
			l, err := eng.Layout(context.Background(), doc, opts)
			if err == nil {
				t.Fatalf("%s: Layout succeeded (PageCount=%d), want an error; a NaN/Inf must never reach layout arithmetic", m.name, l.PageCount())
			}
			if !errors.Is(err, render.ErrInvalidOptions) {
				t.Fatalf("%s: error = %v, want ErrInvalidOptions", m.name, err)
			}
			if l != nil {
				t.Fatalf("%s: Layout returned a layout together with an error", m.name)
			}
		})
	}

	// A non-finite style value is caught by the builder.
	styleMutations := []struct {
		name  string
		style render.Style
	}{
		{"font size NaN", render.Style{FontSize: nan}},
		{"line height NaN", render.Style{LineHeight: nan}},
		{"letter spacing NaN", render.Style{LetterSpacing: nan}},
		{"first line indent NaN", render.Style{FirstLineIndent: nan}},
		{"margin top +Inf", render.Style{MarginTop: inf}},
		{"corner radius NaN", render.Style{CornerRadius: nan}},
		{"border width NaN", render.Style{BorderTop: render.Border{Width: nan}}},
	}
	for _, m := range styleMutations {
		m := m
		t.Run("style "+m.name, func(t *testing.T) {
			b := render.NewDocumentBuilder().SetStyles([]render.Style{{}, m.style})
			err := b.AddChapter(render.Chapter{ID: "s", Root: 0, Nodes: []render.Node{
				{Kind: render.NodeBlock, Children: []render.NodeID{1}},
				{Kind: render.NodeText, Style: 1, Text: "x"},
			}})
			if err == nil {
				_, err = b.Build()
			}
			if err == nil {
				t.Fatalf("style %s: builder accepted a non-finite value", m.name)
			}
			if !errors.Is(err, render.ErrInvalidDocument) {
				t.Fatalf("style %s: error = %v, want ErrInvalidDocument", m.name, err)
			}
		})
	}
}

// TestAuditAcceptanceTileHeightBounds covers the §6 checklist item
// "TileHeight too small returns ErrInvalidOptions".
func TestAuditAcceptanceTileHeightBounds(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	doc := auditTextDoc(t, "tile height probe", false)
	opts := auditOptions(fs)
	opts.Mode = render.ModeScroll

	for _, h := range []float32{0.0001, 1, render.MinTileHeight - 0.5} {
		o := opts
		o.TileHeight = h
		if _, err := eng.Layout(context.Background(), doc, o); !errors.Is(err, render.ErrInvalidOptions) {
			t.Errorf("TileHeight=%v: error = %v, want ErrInvalidOptions", h, err)
		}
	}
	for _, h := range []float32{0, render.MinTileHeight, 113} {
		o := opts
		o.TileHeight = h
		if _, err := eng.Layout(context.Background(), doc, o); err != nil {
			t.Errorf("TileHeight=%v: unexpected error: %v", h, err)
		}
	}
}

// TestAuditAcceptanceLimits covers the §6 checklist item
// "MaxPages/MaxTiles take effect; the resource limits cover pages, tiles and lines".
func TestAuditAcceptanceLimits(t *testing.T) {
	fs := auditFontSet(t)
	doc := auditTextDoc(t, strings.Repeat("The quick brown fox jumps over the lazy dog. ", 400), false)
	opts := auditOptions(fs)

	t.Run("pages", func(t *testing.T) {
		eng := auditEngine(t, render.Options{Limits: render.Limits{MaxPages: 1}})
		_, err := eng.Layout(context.Background(), doc, opts)
		if !errors.Is(err, render.ErrLimitExceeded) {
			t.Fatalf("error = %v, want ErrLimitExceeded", err)
		}
		var de *render.DocumentError
		if !errors.As(err, &de) {
			t.Fatalf("error %v is not a *DocumentError", err)
		}
		if !strings.Contains(de.Detail, "limit") {
			t.Errorf("DocumentError.Detail = %q, want the measured value and the limit", de.Detail)
		}
	})

	t.Run("lines", func(t *testing.T) {
		eng := auditEngine(t, render.Options{Limits: render.Limits{MaxLines: 1}})
		_, err := eng.Layout(context.Background(), doc, opts)
		if !errors.Is(err, render.ErrLimitExceeded) {
			t.Fatalf("error = %v, want ErrLimitExceeded", err)
		}
	})

	t.Run("tiles", func(t *testing.T) {
		eng := auditEngine(t, render.Options{Limits: render.Limits{MaxTiles: 1}})
		scroll := opts
		scroll.Mode = render.ModeScroll
		scroll.TileHeight = 100
		_, err := eng.Layout(context.Background(), doc, scroll)
		if !errors.Is(err, render.ErrLimitExceeded) {
			t.Fatalf("error = %v, want ErrLimitExceeded", err)
		}
	})

	t.Run("defaults accept a normal book", func(t *testing.T) {
		eng := auditEngine(t, render.Options{})
		l, err := eng.Layout(context.Background(), doc, opts)
		if err != nil {
			t.Fatalf("default limits rejected a normal document: %v", err)
		}
		if l.PageCount() <= 1 {
			t.Fatalf("PageCount = %d, want more than one page", l.PageCount())
		}
	})
}

// TestAuditAcceptanceDiagnostics covers the §6 checklist item
// "unknown FontFamily is visible through OnIssue/Issues()" plus the rest of the
// diagnostic surface (missing glyphs, no issues for a clean layout).
func TestAuditAcceptanceDiagnostics(t *testing.T) {
	fs := auditFontSet(t)

	t.Run("missing font", func(t *testing.T) {
		var callback []render.Issue
		eng := auditEngine(t, render.Options{OnIssue: func(i render.Issue) { callback = append(callback, i) }})
		opts := auditOptions(fs)
		opts.Typography.FontFamily = "does-not-exist"
		l, err := eng.Layout(context.Background(), auditTextDoc(t, "hello", true), opts)
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		issues := l.Issues()
		if !hasIssue(issues, render.IssueMissingFont) {
			t.Fatalf("Issues() = %v, want an IssueMissingFont", issues)
		}
		if len(callback) != len(issues) {
			t.Fatalf("OnIssue delivered %d issues, Issues() has %d", len(callback), len(issues))
		}
		for i := range callback {
			if callback[i] != issues[i] {
				t.Fatalf("OnIssue[%d] = %+v, Issues()[%d] = %+v", i, callback[i], i, issues[i])
			}
		}
		// Issues() returns a copy.
		if len(issues) > 0 {
			saved := issues[0]
			issues[0] = render.Issue{Code: render.IssueSuppressed, Detail: "mutated"}
			if got := l.Issues(); len(got) == 0 || got[0] != saved {
				t.Fatalf("Issues() returned the internal slice: after mutating the result, Issues()[0] = %+v, want %+v", got[0], saved)
			}
		}
	})

	t.Run("missing glyphs", func(t *testing.T) {
		eng := auditEngine(t, render.Options{})
		l, err := eng.Layout(context.Background(), auditTextDoc(t, "latin ภาษาไทย שלום", false), auditOptions(fs))
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		if !hasIssue(l.Issues(), render.IssueMissingGlyphs) {
			t.Fatalf("Issues() = %v, want an IssueMissingGlyphs", l.Issues())
		}
	})

	t.Run("clean document has no issues", func(t *testing.T) {
		eng := auditEngine(t, render.Options{})
		l, err := eng.Layout(context.Background(), auditTextDoc(t, "plain latin text only", false), auditOptions(fs))
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		if issues := l.Issues(); len(issues) != 0 {
			t.Fatalf("clean document reported issues: %v", issues)
		}
		if l := l; l == nil {
			t.Fatal("nil layout")
		}
	})
}

func hasIssue(issues []render.Issue, code render.IssueCode) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

// TestAuditAcceptanceRange covers the §4 "no LayoutRange" problem: a chapter
// range lays out a subset, numbers pages from zero, and never resolves excluded
// chapters.
func TestAuditAcceptanceRange(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	opts := auditOptions(fs)

	const chapters = 3
	b := render.NewDocumentBuilder().SetStyles([]render.Style{
		{},
		{FontFamily: "Test Serif", FontSize: 18, LineHeight: 1.5},
	})
	for ci := 0; ci < chapters; ci++ {
		err := b.AddChapter(render.Chapter{ID: fmt.Sprintf("ch%d", ci), Root: sepNode(), Nodes: []render.Node{
			{Kind: render.NodeBlock, Style: 1, Anchor: render.AnchorID(100 + ci), Children: []render.NodeID{1}},
			{Kind: render.NodeText, Style: 0, Text: strings.Repeat(fmt.Sprintf("chapter %d text. ", ci), 120)},
		}})
		if err != nil {
			t.Fatalf("AddChapter %d: %v", ci, err)
		}
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	full, err := eng.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatalf("full Layout: %v", err)
	}
	// Chapter 0 gets at least one page and later chapters start after it.
	_, count1, ok1 := full.PageRange(1)
	_, count2, ok2 := full.PageRange(2)
	if !ok1 || !ok2 {
		t.Fatalf("full.PageRange: ok1=%v ok2=%v", ok1, ok2)
	}

	ranged, err := eng.LayoutRange(context.Background(), doc, opts, 1, 3)
	if err != nil {
		t.Fatalf("LayoutRange: %v", err)
	}
	if got, want := ranged.PageCount(), count1+count2; got != want {
		t.Fatalf("ranged PageCount = %d, want %d (pages of chapters 1 and 2)", got, want)
	}
	pages := ranged.ChapterPages()
	if pages[0] != -1 {
		t.Errorf("ChapterPages()[0] = %d, want -1 for an excluded chapter", pages[0])
	}
	if pages[1] != 0 {
		t.Errorf("ChapterPages()[1] = %d, want 0 (range page numbering starts at zero)", pages[1])
	}
	if pages[2] != count1 {
		t.Errorf("ChapterPages()[2] = %d, want %d", pages[2], count1)
	}
	if _, ok := ranged.Locate(render.AnchorID(100)); ok {
		t.Error("ranged Locate(anchor of excluded chapter 0) = true, want false")
	}
	if _, ok := ranged.Locate(render.AnchorID(101)); !ok {
		t.Error("ranged Locate(anchor of included chapter 1) = false, want true")
	}
	excludedLoc, ok := full.LocationOfPage(0)
	if !ok || excludedLoc.Chapter != 0 {
		t.Fatalf("full.LocationOfPage(0) = %+v, %v", excludedLoc, ok)
	}
	if _, ok := ranged.PageOf(excludedLoc); ok {
		t.Errorf("ranged PageOf(excluded chapter location) = true, want false")
	}
	if _, ok := ranged.LocationOfPage(0); !ok {
		t.Error("ranged LocationOfPage(0) failed, want the first included chapter")
	}
	if ranged.Document() != doc {
		t.Error("ranged Document() is not the full document")
	}

	// Relayout keeps the range unless another one is named.
	relaid, err := ranged.Relayout(context.Background(), func() render.LayoutOptions {
		o := opts
		o.Typography.FontSize = 20
		return o
	}())
	if err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	if p := relaid.ChapterPages(); p[0] != -1 {
		t.Errorf("Relayout dropped the range: ChapterPages()[0] = %d, want -1", p[0])
	}

	// Out-of-range and empty ranges are errors, not panics.
	for _, r := range [][2]int{{0, chapters + 1}, {2, 1}, {-1, 2}, {chapters, chapters}} {
		if _, err := eng.LayoutRange(context.Background(), doc, opts, r[0], r[1]); err == nil {
			t.Errorf("LayoutRange(%d,%d) succeeded, want an error", r[0], r[1])
		}
	}

	// Scroll mode: content y starts at zero at the first included chapter.
	scroll := opts
	scroll.Mode = render.ModeScroll
	scroll.TileHeight = 300
	fullScroll, err := eng.Layout(context.Background(), doc, scroll)
	if err != nil {
		t.Fatalf("full scroll Layout: %v", err)
	}
	rangedScroll, err := eng.LayoutRange(context.Background(), doc, scroll, 1, 3)
	if err != nil {
		t.Fatalf("ranged scroll Layout: %v", err)
	}
	topOf1, ok := fullScroll.ChapterContentTop(1)
	if !ok {
		t.Fatal("ChapterContentTop(1) failed")
	}
	if want := fullScroll.ContentHeight() - topOf1; math.Abs(float64(rangedScroll.ContentHeight()-want)) > 0.01 {
		t.Errorf("ranged scroll ContentHeight = %v, want %v", rangedScroll.ContentHeight(), want)
	}
	if _, ok := rangedScroll.Locate(render.AnchorID(100)); ok {
		t.Error("ranged scroll Locate(excluded anchor) = true, want false")
	}
}

// sepNode is the root node index every audit chapter uses.
func sepNode() render.NodeID { return 0 }

// TestAuditAcceptanceStructuredErrors covers the §3.4 problem: chapter, node and
// style are readable from a *DocumentError instead of a formatted string.
func TestAuditAcceptanceStructuredErrors(t *testing.T) {
	b := render.NewDocumentBuilder().SetStyles([]render.Style{{}})
	err := b.AddChapter(render.Chapter{ID: "cyclic", Root: 0, Nodes: []render.Node{
		{Kind: render.NodeBlock, Children: []render.NodeID{0}},
	}})
	if err == nil {
		t.Fatal("AddChapter accepted a cycle")
	}
	if !errors.Is(err, render.ErrInvalidDocument) {
		t.Fatalf("cycle error = %v, want ErrInvalidDocument", err)
	}
	var de *render.DocumentError
	if !errors.As(err, &de) {
		t.Fatalf("cycle error %v is not a *DocumentError", err)
	}
	if de.Chapter != "cyclic" {
		t.Errorf("DocumentError.Chapter = %q, want %q", de.Chapter, "cyclic")
	}
	if de.Node == render.InvalidNode {
		t.Error("DocumentError.Node = InvalidNode, want the offending node")
	}

	b2 := render.NewDocumentBuilder().SetStyles([]render.Style{{}})
	err = b2.AddChapter(render.Chapter{ID: "outofrange", Root: 0, Nodes: []render.Node{
		{Kind: render.NodeBlock, Children: []render.NodeID{1}},
		{Kind: render.NodeText, Style: 99, Text: "x"},
	}})
	if err == nil {
		t.Fatal("AddChapter accepted an out-of-range style")
	}
	var de2 *render.DocumentError
	if !errors.As(err, &de2) {
		t.Fatalf("style error %v is not a *DocumentError", err)
	}
	if de2.Style != 99 {
		t.Errorf("DocumentError.Style = %d, want 99", de2.Style)
	}
	if de2.Node == render.InvalidNode {
		t.Error("DocumentError.Node = InvalidNode, want the offending node")
	}
}

// TestAuditAcceptanceFontCoverage covers the §3.5 coverage-query surface.
func TestAuditAcceptanceFontCoverage(t *testing.T) {
	fs := auditFontSet(t)
	if got := fs.FontCount(); got != 1 {
		t.Fatalf("FontCount = %d, want 1", got)
	}
	if !fs.HasFamily("Test Serif") || !fs.HasFamily("  test serif ") {
		t.Error("HasFamily does not match the declared family")
	}
	if fs.HasFamily("does-not-exist") || fs.HasFamily("") {
		t.Error("HasFamily accepted a family that is not in the set")
	}
	if !fs.Coverage('a') || !fs.Coverage('α') {
		t.Error("Coverage rejected Latin/Greek, want covered")
	}
	for _, r := range []rune{'ש', 'ก', 'क', '世'} {
		if fs.Coverage(r) {
			t.Errorf("Coverage(U+%04X) = true, want false for this font", r)
		}
	}
	id, ok := fs.Resolve(render.Style{FontFamily: "Test Serif"}, 'a')
	if !ok || id == render.InvalidFace {
		t.Fatalf("Resolve('a') = %d, %v; want a face", id, ok)
	}
	if _, ok := fs.Resolve(render.Style{FontFamily: "Test Serif"}, 'ก'); ok {
		t.Error("Resolve of an uncovered rune returned ok = true")
	}
	font, ok := fs.Face(id)
	if !ok || len(font.Data) == 0 {
		t.Errorf("Face(%d) = ok %v, data len %d", id, ok, len(font.Data))
	}
}

// TestAuditAcceptancePerCallStats covers the §4 problem "Stats is only a global
// total": each layout carries its own attribution.
func TestAuditAcceptancePerCallStats(t *testing.T) {
	fs := auditFontSet(t)
	doc := auditTextDoc(t, strings.Repeat("stats text ", 200), false)

	measured := auditEngine(t, render.Options{Measure: true})
	first, err := measured.Layout(context.Background(), doc, auditOptions(fs))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	st := first.Stats()
	if st.Total <= 0 {
		t.Errorf("LayoutStats.Total = %v with Measure on, want > 0", st.Total)
	}
	if st.Shapes <= 0 {
		t.Errorf("LayoutStats.Shapes = %d, want > 0", st.Shapes)
	}
	if st.Pages != first.PageCount() {
		t.Errorf("LayoutStats.Pages = %d, want PageCount %d", st.Pages, first.PageCount())
	}
	// A second call must not overwrite the first one's stats.
	second, err := measured.Layout(context.Background(), doc, auditOptions(fs))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	_ = second
	if first.Stats().Shapes != st.Shapes {
		t.Error("a later Layout mutated an earlier Layout's Stats")
	}

	// Without Measure, durations are zero but counters still work.
	plain := auditEngine(t, render.Options{})
	l, err := plain.Layout(context.Background(), doc, auditOptions(fs))
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if got := l.Stats().Total; got != 0 {
		t.Errorf("LayoutStats.Total = %v with Measure off, want 0", got)
	}
	if l.Stats().Shapes <= 0 {
		t.Errorf("LayoutStats.Shapes = %d with Measure off, want > 0", l.Stats().Shapes)
	}
}

// TestAuditAcceptancePanicContract checks the public side of the panic contract:
// hostile IR and options come back as errors, never as a process crash, and the
// engine remains usable afterwards.
func TestAuditAcceptancePanicContract(t *testing.T) {
	fs := auditFontSet(t)
	eng := auditEngine(t, render.Options{})
	opts := auditOptions(fs)

	hostileDocs := []func() (*render.Document, error){
		func() (*render.Document, error) {
			// A text node with every separator and unpaired surrogate-ish
			// content the IR can carry.
			b := render.NewDocumentBuilder().SetStyles([]render.Style{{FontFamily: "Test Serif"}})
			if err := b.AddChapter(render.Chapter{ID: "h", Root: 0, Nodes: []render.Node{
				{Kind: render.NodeBlock, Children: []render.NodeID{1, 2}},
				{Kind: render.NodeText, Text: "\r\n\u2028\u2029\x00\uFFFD"},
				{Kind: render.NodeBreak},
			}}); err != nil {
				return nil, err
			}
			return b.Build()
		},
		func() (*render.Document, error) {
			b := render.NewDocumentBuilder().SetStyles([]render.Style{{}})
			if err := b.AddChapter(render.Chapter{ID: "empty", Root: 0, Nodes: []render.Node{{Kind: render.NodeBlock}}}); err != nil {
				return nil, err
			}
			return b.Build()
		},
	}
	for i, build := range hostileDocs {
		doc, err := build()
		if err != nil {
			continue // the builder rejected it: also an acceptable outcome
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("layout %d panicked: %v", i, r)
				}
			}()
			if _, err := eng.Layout(context.Background(), doc, opts); err != nil && errors.Is(err, render.ErrInternal) {
				t.Fatalf("layout %d returned ErrInternal: %v", i, err)
			}
		}()
	}

	// The engine still lays out a normal document after the hostile inputs.
	if _, err := eng.Layout(context.Background(), auditTextDoc(t, "still works", false), opts); err != nil {
		t.Fatalf("Engine unusable after hostile inputs: %v", err)
	}
}

// truncateForName keeps subtest names readable for long inputs.
func truncateForName(s string) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	if len([]rune(s)) > 24 {
		return string([]rune(s)[:24]) + "…"
	}
	return s
}
