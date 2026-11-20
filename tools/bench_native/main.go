// Command bench_native lays out the A/B corpus with the engine running as a
// native Go binary, so scripts/ab.sh's webview numbers can be compared against
// a native baseline on the same text.
//
// It is a development tool for the README's performance table, not part of the
// library.
//
//	bench_native -font /path/to.ttf [-workers 1] [-chars 200000] [-runs 3]
//
// The corpus, styles and layout options mirror examples/wasm/bench.go:runBench
// and examples/wasm/web/ab.js (the same three paragraphs, joined by "\n", cut
// at -chars runes, justified, 640x900, 18px/1.65, 40px margins). Differences
// from the browser run are deliberate and small: ab.js drops paragraphs the
// pinned font cannot draw, and cuts at UTF-16 code units rather than runes, so
// page counts differ by a percent or two. Keep the paragraphs in sync with
// ab.js's PARAS array if that corpus ever changes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// abParas is examples/wasm/web/ab.js:PARAS. Both sides of the A/B must lay out
// the same text, so this is a copy by necessity; the comment above says where
// the original lives.
var abParas = []string{
	"这是一段用来比较排版性能的中文小说正文。主角走进屋子，看见桌上摊着一封没有署名的信。窗外下着雨，院子里积水映着灯光，风把门吹得吱呀作响。他站在那里，很久没有说话。",
	"第一章的名字叫“归途”。作者用了不少短句，也用了几个长句，中间夹着 English words 和数字 2024，用来观察中西文混排与自动间距。标点符号、引号、“这样的段落”、以及省略号……都应当被正确处理。",
	"她问他：“你打算怎么办？”他没有立刻回答，而是把信折好，放回信封里。那两个人在门口等了一会儿，最后决定明天一早再出发。雨停了，街上的灯一盏一盏地灭下去。",
}

const (
	viewportW = 640
	viewportH = 900
	fontSize  = 18
	lineH     = 1.65
	margin    = 40
)

func main() {
	fontPath := flag.String("font", "", "path to a .ttf/.otf the corpus can be drawn with")
	workers := flag.Int("workers", 0, "layout workers; 0 = GOMAXPROCS")
	chars := flag.Int("chars", 200_000, "corpus size in runes")
	runs := flag.Int("runs", 3, "number of measured repetitions")
	flag.Parse()
	if *fontPath == "" {
		fmt.Fprintln(os.Stderr, "bench_native: -font is required")
		os.Exit(2)
	}

	data, err := os.ReadFile(*fontPath)
	if err != nil {
		fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{Family: "book", Weight: 400, Data: data})
	if err != nil {
		fatal(err)
	}
	doc, paragraphs, err := build(*chars)
	if err != nil {
		fatal(err)
	}
	eng, err := render.New(render.Options{Workers: *workers})
	if err != nil {
		fatal(err)
	}

	opts := render.LayoutOptions{
		Viewport: render.Viewport{Width: viewportW, Height: viewportH},
		Typography: render.Typography{
			FontFamily: "book", FontSize: fontSize, LineHeight: lineH,
			Color: paint.Color{A: 0xff},
			// Chrome's multicol fragmentation does not apply widows/orphans;
			// both sides keep them off so they do the same work.
			Rules: render.TextRules{Widows: 1, Orphans: 1},
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: margin, Right: margin, Bottom: margin, Left: margin},
	}

	ctx := context.Background()
	fmt.Printf("corpus %d runes, %d paragraphs, workers=%d\n", *chars, paragraphs, eng.Workers())
	for i := 0; i < *runs; i++ {
		layoutMs, layout, err := measure(func() (*render.Layout, error) {
			return eng.Layout(ctx, doc, opts)
		})
		if err != nil {
			fatal(err)
		}
		// Reflow and repagination go through Relayout, exactly like the wasm
		// bench: a fresh Engine.Layout would measure a cold layout twice.
		reflow := opts
		reflow.Typography.FontSize = fontSize * 4 / 3
		reflowMs, _, err := measure(func() (*render.Layout, error) {
			return layout.Relayout(ctx, reflow)
		})
		if err != nil {
			fatal(err)
		}
		repage := opts
		repage.Typography.LineHeight = lineH * 1.2
		repageMs, _, err := measure(func() (*render.Layout, error) {
			return layout.Relayout(ctx, repage)
		})
		if err != nil {
			fatal(err)
		}
		fmt.Printf("run %d: pages=%d layout=%.1fms font+33%%=%.1fms lineheight*1.2=%.1fms\n",
			i+1, layout.PageCount(), layoutMs, reflowMs, repageMs)
	}
}

func measure(fn func() (*render.Layout, error)) (float64, *render.Layout, error) {
	t := time.Now()
	l, err := fn()
	ms := float64(time.Since(t).Microseconds()) / 1000
	return ms, l, err
}

// build mirrors examples/wasm/bench.go's benchDocument: one block per
// paragraph, all of them children of the chapter root.
func build(chars int) (*render.Document, int, error) {
	var text strings.Builder
	for i, n := 0, 0; n < chars; i++ {
		p := abParas[i%len(abParas)]
		text.WriteString(p)
		text.WriteString("\n")
		n += utf8.RuneCountInString(p) + 1
	}

	nodes := []render.Node{{Kind: render.NodeBlock}}
	var children []render.NodeID
	for _, para := range strings.Split(text.String(), "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		id := render.NodeID(len(nodes))
		nodes = append(nodes, render.Node{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{id + 1}})
		nodes = append(nodes, render.Node{Kind: render.NodeText, Text: para})
		children = append(children, id)
	}
	nodes[0].Children = children

	styles := []render.Style{
		{},
		{
			FontFamily: "book", FontSize: fontSize, LineHeight: lineH,
			Color: paint.Color{A: 0xff}, TextAlign: render.AlignJustify,
			MarginBottom: fontSize * 0.5, FirstLineIndent: fontSize * 2,
		},
	}
	b := render.NewDocumentBuilder().SetStyles(styles).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(render.Chapter{ID: "bench", Root: 0, Nodes: nodes}); err != nil {
		return nil, 0, err
	}
	doc, err := b.Build()
	return doc, len(children), err
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "bench_native: %v\n", err)
	os.Exit(1)
}
