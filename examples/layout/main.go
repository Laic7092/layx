// Command layout is a minimal example of the render library. It lays out a
// document and prints page and line statistics; it does not draw anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func main() {
	fontPath := flag.String("font", "", "path to a .ttf/.otf/.ttc font")
	fontIndex := flag.Int("font-index", 0, "face index in a collection")
	textPath := flag.String("text", "", "path to a UTF-8 text file (default: built-in sample)")
	width := flag.Float64("width", 600, "viewport width")
	height := flag.Float64("height", 900, "viewport height")
	fontSize := flag.Float64("size", 18, "font size")
	flag.Parse()

	text := "The quick brown fox jumps over the lazy dog. 中文排版测试，包含标点符号。"
	if *textPath != "" {
		data, err := os.ReadFile(*textPath)
		if err != nil {
			log.Fatal(err)
		}
		text = string(data)
	}
	if *fontPath == "" {
		log.Fatal("-font is required")
	}
	data, err := os.ReadFile(*fontPath)
	if err != nil {
		log.Fatal(err)
	}
	faces, err := render.NewFontSet(render.FontFace{
		Family: "book", Weight: 400, Data: data, Index: *fontIndex,
	})
	if err != nil {
		log.Fatal(err)
	}

	paras := strings.Split(text, "\n\n")
	nodes := []render.Node{{Kind: render.NodeBlock}}
	children := make([]render.NodeID, 0, len(paras))
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id := render.NodeID(len(nodes))
		nodes = append(nodes, render.Node{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{id + 1}})
		nodes = append(nodes, render.Node{Kind: render.NodeText, Text: p})
		children = append(children, id)
	}
	nodes[0].Children = children

	b := render.NewDocumentBuilder().
		SetStyles([]render.Style{
			{},
			{FontFamily: "book", FontSize: float32(*fontSize), LineHeight: 1.6, Color: paint.Color{A: 0xff}, MarginBottom: 8},
		})
	if err := b.AddChapter(render.Chapter{ID: "text", Root: 0, Nodes: nodes}); err != nil {
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
		Viewport: render.Viewport{Width: float32(*width), Height: float32(*height)},
		Typography: render.Typography{
			FontFamily: "book", FontSize: float32(*fontSize), LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts:   faces,
		Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pages: %d\n", layout.PageCount())
	if p := layout.Page(0); p != nil {
		fmt.Printf("page 0: %.0fx%.0f, links=%d\n", p.Size().Width, p.Size().Height, len(p.Links()))
	}
}
