// Command layx-shot lays out a plain text file with layx and rasterizes one
// page to a PNG. It is the smallest end-to-end example of layx + layx-raster.
//
//	layx-shot -font book.ttf -text chapter.txt -page 0 -out page.png
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"strings"

	"github.com/laix/layx-raster"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

const sampleText = `The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs.
How vexingly quick daft zebras jump!

A second paragraph shows that the display list contains independent blocks,
each with its own wrapping, line height and margins.`

func main() {
	fontPath := flag.String("font", "", "path to a .ttf/.otf/.ttc font")
	fontIndex := flag.Int("font-index", 0, "face index in a collection")
	textPath := flag.String("text", "", "path to a UTF-8 text file (default: built-in sample)")
	width := flag.Float64("width", 600, "viewport width in pixels")
	height := flag.Float64("height", 900, "viewport height in pixels")
	fontSize := flag.Float64("size", 18, "font size in pixels")
	margin := flag.Float64("margin", 40, "page margin in pixels")
	pageIndex := flag.Int("page", 0, "page number to render")
	out := flag.String("out", "page.png", "output PNG path")
	flag.Parse()

	if *fontPath == "" {
		log.Fatal("-font is required")
	}
	data, err := os.ReadFile(*fontPath)
	if err != nil {
		log.Fatal(err)
	}
	text := sampleText
	if *textPath != "" {
		body, err := os.ReadFile(*textPath)
		if err != nil {
			log.Fatal(err)
		}
		text = string(body)
	}

	faces, err := render.NewFontSet(render.FontFace{
		Family: "book", Weight: 400, Data: data, Index: *fontIndex,
	})
	if err != nil {
		log.Fatalf("load font: %v", err)
	}
	doc, err := buildDocument(text, float32(*fontSize))
	if err != nil {
		log.Fatalf("build document: %v", err)
	}
	engine, err := render.New(render.Options{})
	if err != nil {
		log.Fatalf("new engine: %v", err)
	}
	layout, err := engine.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: float32(*width), Height: float32(*height)},
		Typography: render.Typography{
			FontFamily: "book",
			FontSize:   float32(*fontSize),
			LineHeight: 1.6,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   faces,
		Margins: paint.Insets{Top: float32(*margin), Right: float32(*margin), Bottom: float32(*margin), Left: float32(*margin)},
	})
	if err != nil {
		log.Fatalf("layout: %v", err)
	}
	page := layout.Page(*pageIndex)
	if page == nil {
		log.Fatalf("page %d does not exist (pages: %d)", *pageIndex, layout.PageCount())
	}

	rasterizer := raster.New(int(page.Size().Width), int(page.Size().Height))
	rasterizer.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	page.Draw(rasterizer)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, rasterizer.Image()); err != nil {
		f.Close()
		log.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("page %d/%d -> %s\n", *pageIndex, layout.PageCount(), *out)
}

func buildDocument(text string, fontSize float32) (*render.Document, error) {
	paras := strings.Split(text, "\n\n")
	nodes := []render.Node{{Kind: render.NodeBlock}}
	children := make([]render.NodeID, 0, len(paras))
	for _, p := range paras {
		// The input may be hard-wrapped: join its lines so the paragraph is
		// reflowed instead of carrying literal newlines into one text node.
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		id := render.NodeID(len(nodes))
		nodes = append(nodes, render.Node{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{id + 1}})
		nodes = append(nodes, render.Node{Kind: render.NodeText, Text: p})
		children = append(children, id)
	}
	if len(children) == 0 {
		return nil, fmt.Errorf("no text")
	}
	nodes[0].Children = children

	b := render.NewDocumentBuilder().SetStyles([]render.Style{
		{},
		{
			FontFamily:   "book",
			FontSize:     fontSize,
			LineHeight:   1.6,
			Color:        paint.Color{A: 0xff},
			MarginBottom: 8,
			// A first-line indent is a document decision; layx has no default.
			FirstLineIndent: fontSize * 2,
		},
	})
	if err := b.AddChapter(render.Chapter{ID: "text", Root: 0, Nodes: nodes}); err != nil {
		return nil, err
	}
	return b.Build()
}
