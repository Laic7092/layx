// Command memreport measures the memory a host sees when it builds a Document
// from a large plain text, lays it out, draws a few pages and changes the font
// size. It reports live heap, heap from the OS, allocation churn and the peak
// resident set, which is what a task manager shows.
//
// It is a development tool for the README's memory numbers, not part of the
// library. It is also the quickest way to see where a host's memory goes: run
// it with the host's own viewport, paragraph size and chapter count.
//
//	go run ./tools/memreport -chars 3300000 -turns 5
//	go run ./tools/memreport -chars 3300000 -profile /tmp/alloc.pprof
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func stats() (live, sys, idle, total uint64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc, m.HeapSys, m.HeapIdle, m.TotalAlloc
}

// rss prints the kernel's peak and current resident set, which is the number a
// host's task manager shows. On Linux it comes from /proc/self/status; the tool
// prints nothing elsewhere.
func rss() {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmHWM:") || strings.HasPrefix(line, "VmRSS:") {
			fmt.Printf("    %s\n", line)
		}
	}
}

func report(what string, prevTotal uint64) uint64 {
	live, sys, idle, total := stats()
	fmt.Printf("%-28s live=%4dMB sys=%4dMB idle=%4dMB churn=+%4dMB\n",
		what, live>>20, sys>>20, idle>>20, (total-prevTotal)>>20)
	return total
}

// buildTXT mirrors what a plain-text reader does: split the file into chapters,
// lay the chapters out as blocks of paragraphs, and give each paragraph an
// explicit line height and an anchor, as an EPUB cascade would.
func buildTXT(paragraphs, paraRunes, chapters int) *render.Document {
	base := []rune("天地玄黄，宇宙洪荒。日月盈昃，辰宿列张。寒来暑往，秋收冬藏。闰余成岁，律吕调阳。云腾致雨，露结为霜。金生丽水，玉出昆冈。剑号巨阙，珠称夜光。果珍李柰，菜重芥姜。")
	text := make([]rune, paraRunes)
	for i := range text {
		text[i] = base[i%len(base)]
	}
	para := string(text)

	styles := []render.Style{
		{},
		{
			FontSize: 18, LineHeight: 1.6, MarginBottom: 8,
			Color: paint.Color{A: 0xff}, FirstLineIndent: 36,
		},
	}
	b := render.NewDocumentBuilder().SetStyles(styles).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	per := paragraphs / chapters
	for ci := 0; ci < chapters; ci++ {
		nodes := []render.Node{{Kind: render.NodeBlock}}
		children := make([]render.NodeID, 0, per)
		for i := 0; i < per; i++ {
			bi := render.NodeID(len(nodes))
			nodes = append(nodes, render.Node{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{bi + 1}})
			nodes = append(nodes, render.Node{Kind: render.NodeText, Text: para, Anchor: render.AnchorID(ci*per + i + 1)})
			children = append(children, bi)
		}
		nodes[0].Children = children
		if err := b.AddChapter(render.Chapter{ID: fmt.Sprint("ch", ci), Root: 0, Nodes: nodes}); err != nil {
			panic(err)
		}
	}
	doc, err := b.Build()
	if err != nil {
		panic(err)
	}
	return doc
}

func main() {
	chars := flag.Int("chars", 3_300_000, "total runes")
	paraRunes := flag.Int("para", 160, "runes per paragraph")
	chapters := flag.Int("chapters", 100, "chapters (spine items)")
	turns := flag.Int("turns", 5, "font size changes")
	fontPath := flag.String("font", "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc", "font file")
	profile := flag.String("profile", "", "write an allocation profile here")
	liveProfile := flag.String("live-profile", "", "write a live heap profile here")
	flag.Parse()

	fmt.Printf("input: %d runes (%.1f MB utf-8), %d paragraphs, %d chapters\n",
		*chars, float64(*chars*3)/1e6, *chars / *paraRunes, *chapters)
	var total uint64
	doc := buildTXT(*chars / *paraRunes, *paraRunes, *chapters)
	runtime.GC()
	total = report("after Build", total)

	face, err := os.ReadFile(*fontPath)
	if err != nil {
		panic(err)
	}
	fs, err := render.NewFontSet(render.FontFace{Family: "book", Weight: 400, Data: face})
	if err != nil {
		panic(err)
	}
	runtime.GC()
	total = report("after FontSet", total)

	eng, _ := render.New(render.Options{Workers: 1, Measure: true})
	opts := render.LayoutOptions{
		Viewport:   render.Viewport{Width: 600, Height: 900},
		Typography: render.Typography{FontFamily: "book", FontSize: 18, LineHeight: 1.6, Color: paint.Color{A: 0xff}},
		Fonts:      fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	l, err := eng.Layout(context.Background(), doc, opts)
	if err != nil {
		panic(err)
	}
	if *profile != "" {
		f, err := os.Create(*profile)
		if err != nil {
			panic(err)
		}
		if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
			panic(err)
		}
		_ = f.Close()
	}
	runtime.GC()
	total = report("after Layout", total)
	if *liveProfile != "" {
		f, err := os.Create(*liveProfile)
		if err != nil {
			panic(err)
		}
		if err := pprof.WriteHeapProfile(f); err != nil {
			panic(err)
		}
		_ = f.Close()
	}
	fmt.Printf("    pages=%d\n", l.PageCount())

	for i := 0; i < 5; i++ {
		if p := l.Page(i); p != nil {
			_ = p.Links()
		}
	}
	total = report("after drawing 5 pages", total)
	rss()

	for i := 0; i < *turns; i++ {
		opts.Typography.FontSize = float32(18 + i + 1)
		l, err = l.Relayout(context.Background(), opts)
		if err != nil {
			panic(err)
		}
		runtime.GC()
		total = report(fmt.Sprintf("after Relayout %d", i+1), total)
	}
	_ = l
	_ = total
	rss()
	runtime.KeepAlive(doc)
}
