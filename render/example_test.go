// Package render_test contains the runnable examples shown on pkg.go.dev.
//
// The examples are the first code a stranger copies, so each one is
// self-contained, uses only the checked-in test font, touches no network, and
// prints deterministic output.
package render_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// Example is the smallest end-to-end layout: load a font, build a one-paragraph
// document, lay it out for a 600x900 viewport, and inspect the result.
func Example() {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		log.Fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{
		Family: "serif",
		Weight: 400,
		Data:   data,
	})
	if err != nil {
		log.Fatal(err)
	}

	b := render.NewDocumentBuilder().
		SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}}).
		SetMetadata(render.Metadata{Language: "en", Reflowable: true})
	err = b.AddChapter(render.Chapter{
		ID:   "ch1",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Children: []render.NodeID{1}},
			{Kind: render.NodeText, Style: 1, Text: "The quick brown fox jumps over the lazy dog."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		log.Fatal(err)
	}

	eng, err := render.New(render.Options{})
	if err != nil {
		log.Fatal(err)
	}
	layout, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport:   render.Viewport{Width: 600, Height: 900},
		Typography: render.Typography{FontFamily: "serif", FontSize: 18, LineHeight: 1.6},
		Fonts:      fonts,
		Margins:    paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("pages: %d\n", layout.PageCount())
	fmt.Printf("page size: %.0fx%.0f\n", layout.Size().Width, layout.Size().Height)
	fmt.Printf("page 0 nil: %v\n", layout.Page(0) == nil)

	// Output:
	// pages: 1
	// page size: 600x900
	// page 0 nil: false
}

// ExampleEngine_Layout shows the options a host usually sets: a worker budget,
// typography, margins, and the diagnostics callback. The layout is immutable
// and safe to read from several goroutines; a page display list is built on
// first access and then cached.
func ExampleEngine_Layout() {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		log.Fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{Family: "serif", Weight: 400, Data: data})
	if err != nil {
		log.Fatal(err)
	}

	b := render.NewDocumentBuilder().
		SetStyles([]render.Style{
			{},
			{FontFamily: "serif", FontSize: 18, LineHeight: 1.6, FirstLineIndent: 36},
		}).
		SetMetadata(render.Metadata{Language: "en", Reflowable: true})
	err = b.AddChapter(render.Chapter{
		ID:   "ch1",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Children: []render.NodeID{1, 2}},
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{3}},
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{4}},
			{Kind: render.NodeText, Style: 1, Text: "The quick brown fox jumps over the lazy dog."},
			{Kind: render.NodeText, Style: 1, Text: "Pack my box with five dozen liquor jugs."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		log.Fatal(err)
	}

	eng, err := render.New(render.Options{
		Workers: 2, // a budget for the whole Engine, not a per-call cap
		OnIssue: func(is render.Issue) {
			// Called once per issue before Layout returns, on this goroutine.
			fmt.Println("diagnostic:", is)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	layout, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: 600, Height: 900},
		Typography: render.Typography{
			FontFamily: "serif",
			FontSize:   18,
			LineHeight: 1.6,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: 48, Right: 48, Bottom: 48, Left: 48},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("pages: %d\n", layout.PageCount())
	fmt.Printf("diagnostics: %d\n", len(layout.Issues()))
	fmt.Printf("first page exists: %v\n", layout.Page(0) != nil)

	// Output:
	// pages: 1
	// diagnostics: 0
	// first page exists: true
}

// ExampleDocumentBuilder_Build shows the document IR. Nodes live in a flat
// arena per chapter and refer to their children by index; styles are an indexed
// table and must already be cascaded by the host.
func ExampleDocumentBuilder_Build() {
	b := render.NewDocumentBuilder().
		SetStyles([]render.Style{
			{},
			{FontFamily: "serif", FontSize: 18, LineHeight: 1.6, FirstLineIndent: 36},
		}).
		SetMetadata(render.Metadata{Language: "en", Reflowable: true})
	err := b.AddChapter(render.Chapter{
		ID:   "ch1",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Children: []render.NodeID{1, 2}},
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{3}},
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{4}},
			{Kind: render.NodeText, Style: 1, Text: "First paragraph."},
			{Kind: render.NodeText, Style: 1, Text: "Second paragraph."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("chapters: %d\n", doc.ChapterCount())
	fmt.Printf("styles: %d\n", doc.StyleCount())
	chapter, ok := doc.Chapter(0)
	fmt.Printf("chapter %q ok: %v, nodes: %d\n", chapter.ID, ok, len(chapter.Nodes))

	// Output:
	// chapters: 1
	// styles: 2
	// chapter "ch1" ok: true, nodes: 5
}

// ExampleFontSet_Coverage asks the font set what it can draw before layout
// runs: whether a family exists, whether any face has a glyph for a rune, and
// which face the layout would pick for a style and rune. This is how a host
// detects tofu and picks a fallback instead of waiting for a diagnostic.
func ExampleFontSet_Coverage() {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		log.Fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{Family: "serif", Weight: 400, Data: data})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("family present: %v\n", fonts.HasFamily("serif"))
	fmt.Printf("covers 'A': %v\n", fonts.Coverage('A'))
	fmt.Printf("covers '好': %v\n", fonts.Coverage('好'))

	// Resolve returns the face the layout would actually use for that rune
	// under the style's family, weight and slant.
	face, ok := fonts.Resolve(render.Style{FontFamily: "serif", FontWeight: 400}, 'A')
	fmt.Printf("resolved face: %d (ok: %v)\n", face, ok)

	// Output:
	// family present: true
	// covers 'A': true
	// covers '好': false
	// resolved face: 0 (ok: true)
}

// ExampleLayout_HitTest maps a point in content coordinates to a document
// location. The same coordinate system is used by LocationAt/OffsetOf, so a
// hit-test result can be turned into a page, a selection anchor or a saved
// reading position.
func ExampleLayout_HitTest() {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		log.Fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{Family: "serif", Weight: 400, Data: data})
	if err != nil {
		log.Fatal(err)
	}

	b := render.NewDocumentBuilder().
		SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}})
	err = b.AddChapter(render.Chapter{
		ID:   "ch1",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Children: []render.NodeID{1}},
			{Kind: render.NodeText, Style: 1, Text: "The quick brown fox jumps over the lazy dog."},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		log.Fatal(err)
	}

	eng, err := render.New(render.Options{Workers: 1})
	if err != nil {
		log.Fatal(err)
	}
	layout, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport:   render.Viewport{Width: 600, Height: 900},
		Typography: render.Typography{FontFamily: "serif", FontSize: 18, LineHeight: 1.6},
		Fonts:      fonts,
		Margins:    paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		log.Fatal(err)
	}

	// x is measured from the left content edge (page Margins.Left excluded);
	// y is measured from the top of the book body, which is page 0 here.
	loc, ok := layout.HitTest(paint.Point{X: 10, Y: 10})
	fmt.Printf("hit: %v\n", ok)
	fmt.Printf("chapter: %d\n", loc.Chapter)
	fmt.Printf("has node: %v\n", loc.Node != render.InvalidNode)

	// Output:
	// hit: true
	// chapter: 0
	// has node: true
}
