package render

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/laic7092/layx/render/paint"
)

// nonFiniteFloats are the values every float input must reject. Both
// infinities and NaN are covered: NaN slips through every "x <= 0" check, and
// an infinity overflows the same arithmetic in the other direction.
var nonFiniteFloats = []struct {
	name string
	v    float32
}{
	{"NaN", float32(math.NaN())},
	{"+Inf", float32(math.Inf(1))},
	{"-Inf", float32(math.Inf(-1))},
}

// hostileBaseOptions returns a LayoutOptions that passes validate. Each test
// case mutates exactly one field.
func hostileBaseOptions(t testing.TB) LayoutOptions {
	t.Helper()
	return LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily:    "Test Serif",
			FontSize:      16,
			LineHeight:    1.5,
			LetterSpacing: 0.25,
			WordSpacing:   0.5,
			ParagraphGap:  8,
			Color:         paint.Color{A: 0xff},
		},
		Fonts:      testFontSet(t),
		Margins:    paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
		Mode:       ModeScroll,
		TileHeight: 100,
	}
}

// TestHostileInputsLayoutOptions is the table-driven §3.1 regression test: every
// float field of LayoutOptions must reject NaN and infinities, before the
// defaulting step and before any layout work happens.
func TestHostileInputsLayoutOptions(t *testing.T) {
	base := hostileBaseOptions(t)
	fields := []struct {
		name   string
		set    func(o *LayoutOptions, v float32)
		detail string
	}{
		{"viewport width", func(o *LayoutOptions, v float32) { o.Viewport.Width = v }, "viewport width"},
		{"viewport height", func(o *LayoutOptions, v float32) { o.Viewport.Height = v }, "viewport height"},
		{"margin top", func(o *LayoutOptions, v float32) { o.Margins.Top = v }, "margin top"},
		{"margin right", func(o *LayoutOptions, v float32) { o.Margins.Right = v }, "margin right"},
		{"margin bottom", func(o *LayoutOptions, v float32) { o.Margins.Bottom = v }, "margin bottom"},
		{"margin left", func(o *LayoutOptions, v float32) { o.Margins.Left = v }, "margin left"},
		{"tile height", func(o *LayoutOptions, v float32) { o.TileHeight = v }, "tile height"},
		{"typography font size", func(o *LayoutOptions, v float32) { o.Typography.FontSize = v }, "typography font size"},
		{"typography line height", func(o *LayoutOptions, v float32) { o.Typography.LineHeight = v }, "typography line height"},
		{"typography letter spacing", func(o *LayoutOptions, v float32) { o.Typography.LetterSpacing = v }, "typography letter spacing"},
		{"typography word spacing", func(o *LayoutOptions, v float32) { o.Typography.WordSpacing = v }, "typography word spacing"},
		{"typography paragraph gap", func(o *LayoutOptions, v float32) { o.Typography.ParagraphGap = v }, "typography paragraph gap"},
	}
	for _, f := range fields {
		for _, nf := range nonFiniteFloats {
			t.Run(f.name+"/"+nf.name, func(t *testing.T) {
				o := base
				f.set(&o, nf.v)
				_, err := o.validate()
				if !errors.Is(err, ErrInvalidOptions) {
					t.Fatalf("validate(%s=%v) = %v, want ErrInvalidOptions", f.name, nf.v, err)
				}
				var de *DocumentError
				if !errors.As(err, &de) {
					t.Fatalf("error type = %T, want *DocumentError", err)
				}
				if !strings.Contains(de.Detail, f.detail) {
					t.Fatalf("detail %q does not name field %q", de.Detail, f.detail)
				}
			})
		}
	}
}

// TestHostileInputsDoNotProducePages runs the hostile configurations through a
// real Engine.Layout. The audit measured these as silently succeeding: NaN
// viewport produced NaN page geometry, NaN line height silently produced one
// page, and +Inf font size "succeeded". They must now fail instead.
func TestHostileInputsDoNotProducePages(t *testing.T) {
	doc := testDocument(t, "")
	base := hostileBaseOptions(t)
	base.Mode = ModePaged
	base.TileHeight = 0
	e := mustEngine(t, 2)

	cases := []struct {
		name string
		set  func(o *LayoutOptions)
	}{
		{"viewport width NaN", func(o *LayoutOptions) { o.Viewport.Width = float32(math.NaN()) }},
		{"viewport height +Inf", func(o *LayoutOptions) { o.Viewport.Height = float32(math.Inf(1)) }},
		{"margin top NaN", func(o *LayoutOptions) { o.Margins.Top = float32(math.NaN()) }},
		{"line height NaN", func(o *LayoutOptions) { o.Typography.LineHeight = float32(math.NaN()) }},
		{"letter spacing NaN", func(o *LayoutOptions) { o.Typography.LetterSpacing = float32(math.NaN()) }},
		{"paragraph gap NaN", func(o *LayoutOptions) { o.Typography.ParagraphGap = float32(math.NaN()) }},
		{"font size +Inf", func(o *LayoutOptions) { o.Typography.FontSize = float32(math.Inf(1)) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := base
			c.set(&o)
			l, err := e.Layout(context.Background(), doc, o)
			if err == nil {
				t.Fatalf("Layout accepted hostile options and returned a layout with %d pages", l.PageCount())
			}
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("Layout error = %v, want ErrInvalidOptions", err)
			}
			if l != nil {
				t.Fatalf("Layout returned a non-nil layout alongside %v", err)
			}
		})
	}
}

// TestTileHeightBounds pins the §3.2 minimum: a tiny non-zero TileHeight makes
// one extra pixel of content add another tile, so it is rejected; zero keeps its
// "use the viewport height" meaning and larger values are fine.
func TestTileHeightBounds(t *testing.T) {
	base := hostileBaseOptions(t)

	for _, h := range []float32{0, MinTileHeight, 100, 5000} {
		o := base
		o.TileHeight = h
		if _, err := o.validate(); err != nil {
			t.Fatalf("TileHeight %v: validate = %v, want nil", h, err)
		}
	}
	for _, h := range []float32{-1, 0.0001, MinTileHeight - 0.01, MinTileHeight - 1} {
		o := base
		o.TileHeight = h
		if _, err := o.validate(); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("TileHeight %v: validate = %v, want ErrInvalidOptions", h, err)
		}
	}

	// End to end: a layout, not just validate, refuses the configuration.
	e := mustEngine(t, 1)
	o := base
	o.TileHeight = 5
	if _, err := e.Layout(context.Background(), testDocument(t, ""), o); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("Layout with TileHeight 5 = %v, want ErrInvalidOptions", err)
	}
}

// TestValidOptionsStillLayout guards against validation that rejects legal
// configurations: both modes must produce output.
func TestValidOptionsStillLayout(t *testing.T) {
	e := mustEngine(t, 2)
	doc := testDocument(t, "")
	ctx := context.Background()

	paged := hostileBaseOptions(t)
	paged.Mode = ModePaged
	paged.TileHeight = 0
	l, err := e.Layout(ctx, doc, paged)
	if err != nil {
		t.Fatalf("paged Layout: %v", err)
	}
	if l.PageCount() == 0 {
		t.Fatal("paged Layout produced no pages")
	}

	scroll := hostileBaseOptions(t)
	scroll.Mode = ModeScroll
	scroll.TileHeight = 100
	sl, err := e.Layout(ctx, doc, scroll)
	if err != nil {
		t.Fatalf("scroll Layout: %v", err)
	}
	if sl.TileCount() == 0 {
		t.Fatal("scroll Layout produced no tiles")
	}
}

// hostileStyleValues returns a Style with every float field set to a legal
// value, so a test can make exactly one field hostile.
func hostileStyleValues() Style {
	return Style{
		FontFamily:      "Test Serif",
		FontSize:        16,
		LineHeight:      1.5,
		LetterSpacing:   0.25,
		WordSpacing:     0.5,
		FirstLineIndent: 12,
		MarginTop:       4,
		MarginBottom:    4,
		MarginLeft:      2,
		MarginRight:     2,
		CornerRadius:    3,
		BorderTop:       Border{Width: 1},
		BorderRight:     Border{Width: 1},
		BorderBottom:    Border{Width: 1},
		BorderLeft:      Border{Width: 1},
	}
}

// hostileChapter returns a minimal valid chapter whose text node uses style 1,
// so a builder with two styles can accept it.
func hostileChapter(id string) Chapter {
	return Chapter{ID: id, Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Style: 1, Text: "hello"},
	}}
}

// TestHostileInputsStyle is the §3.1 Style half: a non-finite style float must
// fail the DocumentBuilder path with ErrInvalidDocument and the style index,
// whether the failure surfaces at AddChapter or at Build.
func TestHostileInputsStyle(t *testing.T) {
	fields := []struct {
		name   string
		set    func(s *Style, v float32)
		detail string
	}{
		{"font size", func(s *Style, v float32) { s.FontSize = v }, "font size"},
		{"line height", func(s *Style, v float32) { s.LineHeight = v }, "line height"},
		{"letter spacing", func(s *Style, v float32) { s.LetterSpacing = v }, "letter spacing"},
		{"word spacing", func(s *Style, v float32) { s.WordSpacing = v }, "word spacing"},
		{"first line indent", func(s *Style, v float32) { s.FirstLineIndent = v }, "first line indent"},
		{"margin top", func(s *Style, v float32) { s.MarginTop = v }, "margin top"},
		{"margin bottom", func(s *Style, v float32) { s.MarginBottom = v }, "margin bottom"},
		{"margin left", func(s *Style, v float32) { s.MarginLeft = v }, "margin left"},
		{"margin right", func(s *Style, v float32) { s.MarginRight = v }, "margin right"},
		{"corner radius", func(s *Style, v float32) { s.CornerRadius = v }, "corner radius"},
		{"border top width", func(s *Style, v float32) { s.BorderTop.Width = v }, "border top width"},
		{"border right width", func(s *Style, v float32) { s.BorderRight.Width = v }, "border right width"},
		{"border bottom width", func(s *Style, v float32) { s.BorderBottom.Width = v }, "border bottom width"},
		{"border left width", func(s *Style, v float32) { s.BorderLeft.Width = v }, "border left width"},
	}

	check := func(t *testing.T, err error, wantDetail string) {
		t.Helper()
		if !errors.Is(err, ErrInvalidDocument) {
			t.Fatalf("error = %v, want ErrInvalidDocument", err)
		}
		var de *DocumentError
		if !errors.As(err, &de) {
			t.Fatalf("error type = %T, want *DocumentError", err)
		}
		if de.Style != 1 {
			t.Fatalf("DocumentError.Style = %d, want 1", de.Style)
		}
		if !strings.Contains(de.Detail, wantDetail) {
			t.Fatalf("detail %q does not name field %q", de.Detail, wantDetail)
		}
	}

	for _, f := range fields {
		for _, nf := range nonFiniteFloats {
			t.Run(f.name+"/"+nf.name, func(t *testing.T) {
				st := hostileStyleValues()
				f.set(&st, nf.v)
				styles := []Style{{}, st}

				// The error surfaces at AddChapter, the first fallible call
				// after SetStyles.
				b := NewDocumentBuilder().SetStyles(styles)
				if err := b.AddChapter(hostileChapter("c")); err == nil {
					t.Fatal("AddChapter accepted a non-finite style")
				} else {
					check(t, err, f.detail)
				}

				// ... and at Build for a builder that never added a chapter.
				b2 := NewDocumentBuilder().SetStyles(styles)
				if _, err := b2.Build(); err == nil {
					t.Fatal("Build accepted a non-finite style")
				} else {
					check(t, err, f.detail)
				}
			})
		}
	}

	// Style 0 is reported as style 0, not as the InvalidStyle sentinel.
	st := hostileStyleValues()
	st.FontSize = float32(math.NaN())
	b := NewDocumentBuilder().SetStyles([]Style{st})
	_, err := b.Build()
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("Style 0: error = %v, want ErrInvalidDocument", err)
	}
	var de *DocumentError
	if !errors.As(err, &de) || de.Style != 0 {
		t.Fatalf("Style 0: DocumentError = %+v, want Style 0", de)
	}
}

// TestValidStylesStillBuild makes sure the style validation does not reject
// ordinary style tables or change the builder's existing behaviour.
func TestValidStylesStillBuild(t *testing.T) {
	styles := []Style{{}, hostileStyleValues()}
	b := NewDocumentBuilder().SetStyles(styles)
	if err := b.AddChapter(hostileChapter("c")); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if doc.StyleCount() != 2 {
		t.Fatalf("StyleCount = %d, want 2", doc.StyleCount())
	}
	if doc.ChapterCount() != 1 {
		t.Fatalf("ChapterCount = %d, want 1", doc.ChapterCount())
	}
}

// TestDocumentErrorContext checks the §3.4 conversion: builder failures carry
// chapter, node and style identifiers in structured fields, not only in the
// message, and errors.Is still reaches the sentinel.
func TestDocumentErrorContext(t *testing.T) {
	dup := hostileChapter("dup")
	b := NewDocumentBuilder().SetStyles([]Style{{}, {}})
	if err := b.AddChapter(dup); err != nil {
		t.Fatalf("first AddChapter: %v", err)
	}
	err := b.AddChapter(dup)
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("duplicate chapter: error = %v, want ErrInvalidDocument", err)
	}
	var de *DocumentError
	if !errors.As(err, &de) || de.Chapter != "dup" {
		t.Fatalf("duplicate chapter: DocumentError = %+v, want Chapter %q", de, "dup")
	}
	if !strings.Contains(err.Error(), "dup") {
		t.Fatalf("duplicate chapter message %q lost the chapter id", err.Error())
	}

	// Style out of range: the failing node is reported, and the style id too.
	b = NewDocumentBuilder()
	ch := Chapter{ID: "styles", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Style: 3, Text: "x"},
	}}
	err = b.AddChapter(ch)
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("style range: error = %v, want ErrInvalidDocument", err)
	}
	de = nil
	if !errors.As(err, &de) || de.Chapter != "styles" || de.Node != 1 || de.Style != 3 {
		t.Fatalf("style range: DocumentError = %+v, want chapter styles node 1 style 3", de)
	}

	// Anchor target out of range keeps the chapter context.
	b = NewDocumentBuilder()
	ch = Chapter{ID: "anchors", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: "x"},
	}, Anchors: map[AnchorID]NodeID{7: 99}}
	err = b.AddChapter(ch)
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("anchor range: error = %v, want ErrInvalidDocument", err)
	}
	de = nil
	if !errors.As(err, &de) || de.Chapter != "anchors" {
		t.Fatalf("anchor range: DocumentError = %+v, want Chapter %q", de, "anchors")
	}

	// Unknown node kind names the node.
	b = NewDocumentBuilder()
	ch = Chapter{ID: "kind", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeKind(99), Text: "x"},
	}}
	err = b.AddChapter(ch)
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("unknown kind: error = %v, want ErrInvalidDocument", err)
	}
	de = nil
	if !errors.As(err, &de) || de.Chapter != "kind" || de.Node != 1 {
		t.Fatalf("unknown kind: DocumentError = %+v, want chapter kind node 1", de)
	}
}

// TestStructuredFontErrors covers the error sites in options.go and font.go that
// §3.4 converted: they must keep errors.Is and add Detail.
func TestStructuredFontErrors(t *testing.T) {
	if err := func() error { _, e := New(Options{Workers: -1}); return e }(); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("New(negative workers) = %v, want ErrInvalidOptions", err)
	} else {
		var de *DocumentError
		if !errors.As(err, &de) || de.Detail == "" {
			t.Fatalf("New error = %+v, want DocumentError with Detail", err)
		}
	}

	if _, err := NewFontSet(); !errors.Is(err, ErrNoFonts) {
		t.Fatalf("NewFontSet(no faces) = %v, want ErrNoFonts", err)
	} else {
		var de *DocumentError
		if !errors.As(err, &de) || de.Detail == "" {
			t.Fatalf("NewFontSet(no faces) = %+v, want DocumentError with Detail", err)
		}
	}

	bad := []struct {
		name string
		face FontFace
		kind error
	}{
		{"no data", FontFace{Family: "x"}, ErrInvalidOptions},
		{"bad index", FontFace{Family: "x", Index: -1, Data: loadTestFont(t).Data}, ErrInvalidOptions},
		{"bad slant", FontFace{Family: "x", Slant: Slant(9), Data: loadTestFont(t).Data}, ErrInvalidOptions},
		{"corrupt data", FontFace{Family: "x", Data: []byte("not a font file")}, ErrInvalidOptions},
	}
	for _, c := range bad {
		if _, err := NewFontSet(c.face); !errors.Is(err, c.kind) {
			t.Fatalf("%s: error = %v, want %v", c.name, err, c.kind)
		} else {
			var de *DocumentError
			if !errors.As(err, &de) {
				t.Fatalf("%s: error type = %T, want *DocumentError", c.name, err)
			}
		}
	}

	// Missing Fonts is ErrNoFonts, and Layout reports it as a DocumentError.
	e := mustEngine(t, 1)
	o := hostileBaseOptions(t)
	o.Fonts = nil
	if _, err := e.Layout(context.Background(), testDocument(t, ""), o); !errors.Is(err, ErrNoFonts) {
		t.Fatalf("Layout without Fonts = %v, want ErrNoFonts", err)
	}
}
