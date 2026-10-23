//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall/js"
	"time"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// benchResult mirrors what ab.js needs for the A/B table. It is encoded by the
// hand-rolled jsonWriter in main.go.
type benchResult struct {
	Pages    int       `json:"pages"`
	BuildMS  float64   `json:"buildMs"`
	LayoutMS float64   `json:"layoutMs"`
	ColdMS   []float64 `json:"coldMs"`
	HotMS    []float64 `json:"hotMs"`
	ReflowMS float64   `json:"reflowMs"`
	RepageMS float64   `json:"repageMs"`
	// MemSys is runtime.MemStats.Sys after the run (wasm linear memory is the
	// closest analogue to a process RSS here).
	MemSys uint64 `json:"memSys"`
}

// The layout-only exports (initLayout/layoutChapter/benchBook) are what the
// Web Worker pool uses. They deliberately share the bench styles and options
// so a pooled chapter and the same chapter inside a single-instance book
// produce the same page count.

const (
	benchMargin = 40
	// benchAnchorBase keeps the per-chapter anchors used by benchBook away from
	// the demo's anchor space (main.go starts its anchors at anchorBase=100).
	benchAnchorBase = 1000
)

type initLayoutResult struct {
	Family    string  `json:"family"`
	Faces     int     `json:"faces"`
	FaceIndex int     `json:"faceIndex"`
	InitMS    float64 `json:"initMs"`
}

type chapterLayoutResult struct {
	Pages    int     `json:"pages"`
	MS       float64 `json:"ms"`
	BuildMS  float64 `json:"buildMs"`
	LayoutMS float64 `json:"layoutMs"`
}

type bookLayoutResult struct {
	Pages        int     `json:"pages"`
	ChapterPages []int   `json:"chapterPages"`
	Chapters     int     `json:"chapters"`
	BuildMS      float64 `json:"buildMs"`
	LayoutMS     float64 `json:"layoutMs"`
	MS           float64 `json:"ms"`
	MemSys       uint64  `json:"memSys"`
}

func registerBench(api js.Value) {
	export(api, "bench", func(args []js.Value) (any, error) {
		// text, w, h, size, lineHeight, justify, turns
		if err := need(args, 7); err != nil {
			return nil, err
		}
		return runBench(args[0].String(), args[1].Float(), args[2].Float(),
			args[3].Float(), args[4].Float(), args[5].Bool(), args[6].Int())
	})

	// initLayout builds only the font set (and a layout engine): no canvas, no
	// overlay. Every worker has its own Go heap, so every worker must copy and
	// parse the font itself; the returned initMs makes that cost visible.
	export(api, "initLayout", func(args []js.Value) (any, error) {
		if err := need(args, 1); err != nil {
			return nil, err
		}
		r, err := initLayout(args[0])
		if err != nil {
			return nil, err
		}
		return initReply(r), nil
	})

	// layoutChapter lays out one chapter (paragraphs separated by "\n") and
	// returns its page count. It is the worker hot path.
	export(api, "layoutChapter", func(args []js.Value) (any, error) {
		if err := need(args, 6); err != nil {
			return nil, err
		}
		r, err := layoutChapter(args[0].String(), args[1].Float(), args[2].Float(),
			args[3].Float(), args[4].Float(), args[5].Bool())
		if err != nil {
			return nil, err
		}
		return chapterReply(r), nil
	})

	// benchBook is the single-instance baseline for the pool: the same
	// chapters, laid out one after another by one wasm instance. Chapters are
	// paginated independently, so its per-chapter page counts must match
	// layoutChapter exactly.
	export(api, "benchBook", func(args []js.Value) (any, error) {
		if err := need(args, 6); err != nil {
			return nil, err
		}
		if args[0].Type() != js.TypeObject {
			return nil, errors.New("benchBook: chapters must be an array")
		}
		n := args[0].Length()
		chapters := make([]string, n)
		for i := 0; i < n; i++ {
			chapters[i] = args[0].Index(i).String()
		}
		r, err := runBenchBook(chapters, args[1].Float(), args[2].Float(),
			args[3].Float(), args[4].Float(), args[5].Bool())
		if err != nil {
			return nil, err
		}
		return bookReply(r), nil
	})
}

// The three reply helpers keep the new exports on the value types the
// hand-rolled jsonWriter in main.go already understands: map[string]any,
// float64 and []float64. main.go's existing exports must not change, so the
// writer is not extended for these worker-only payloads.
func initReply(r initLayoutResult) map[string]any {
	return map[string]any{
		"family":    r.Family,
		"faces":     r.Faces,
		"faceIndex": r.FaceIndex,
		"initMs":    r.InitMS,
	}
}

func chapterReply(r chapterLayoutResult) map[string]any {
	return map[string]any{
		"pages":    r.Pages,
		"ms":       r.MS,
		"buildMs":  r.BuildMS,
		"layoutMs": r.LayoutMS,
	}
}

func bookReply(r bookLayoutResult) map[string]any {
	pages := make([]float64, len(r.ChapterPages))
	for i, n := range r.ChapterPages {
		pages[i] = float64(n)
	}
	return map[string]any{
		"pages":        r.Pages,
		"chapterPages": pages,
		"chapters":     r.Chapters,
		"buildMs":      r.BuildMS,
		"layoutMs":     r.LayoutMS,
		"ms":           r.MS,
		// float64: the jsonWriter has no uint64 case.
		"memSys": float64(r.MemSys),
	}
}

func benchAlign(justify bool) render.TextAlign {
	if justify {
		return render.AlignJustify
	}
	return render.AlignLeft
}

func benchStyles(size, lineHeight float32, align render.TextAlign) []render.Style {
	return []render.Style{
		{},
		{
			FontFamily: "book", FontSize: size, LineHeight: lineHeight,
			Color: paint.Color{A: 0xff}, TextAlign: align, MarginBottom: size * 0.5,
			FirstLineIndent: size * 2,
		},
	}
}

// benchLayoutOptions is the one place that decides viewport, typography and
// margins for the bench. The pool and the single-instance baseline must not
// drift apart here.
func benchLayoutOptions(w, h, size, lineHeight float64, justify bool, fonts *render.FontSet) render.LayoutOptions {
	return render.LayoutOptions{
		Viewport: render.Viewport{Width: float32(w), Height: float32(h)},
		Typography: render.Typography{
			FontFamily: "book", FontSize: float32(size), LineHeight: float32(lineHeight),
			Color: paint.Color{A: 0xff},
			// Chrome's multicol fragmentation does not apply widows/orphans;
			// keep them off (the zero value) so both sides do the same work.
			Rules: render.TextRules{Widows: 1, Orphans: 1},
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: benchMargin, Right: benchMargin, Bottom: benchMargin, Left: benchMargin},
	}
}

func runBench(text string, w, h, size, lineHeight float64, justify bool, turns int) (benchResult, error) {
	var res benchResult
	if state.fonts == nil {
		return res, errors.New("boot() must be called first")
	}
	if turns < 1 {
		turns = 1
	}

	eng, err := render.New(render.Options{Workers: 0})
	if err != nil {
		return res, err
	}
	if state.painter != nil {
		state.painter.ctx = state.ctx
	}
	align := benchAlign(justify)

	t0 := time.Now()
	doc, err := benchDocument(text, float32(size), float32(lineHeight), align)
	if err != nil {
		return res, err
	}
	res.BuildMS = msSince(t0)

	t0 = time.Now()
	layout, err := eng.Layout(context.Background(), doc, benchLayoutOptions(w, h, size, lineHeight, justify, state.fonts))
	if err != nil {
		return res, err
	}
	res.LayoutMS = msSince(t0)
	res.Pages = layout.PageCount()

	ctx := context.Background()
	painter := state.painter
	for i := 0; i < turns && i < res.Pages; i++ {
		t0 = time.Now()
		page := layout.Page(i)
		if page != nil && painter != nil {
			page.Draw(painter)
		}
		res.ColdMS = append(res.ColdMS, msSince(t0))
	}
	for i := 0; i < turns && i < res.Pages; i++ {
		t0 = time.Now()
		page := layout.Page(i)
		if page != nil && painter != nil {
			page.Draw(painter)
		}
		res.HotMS = append(res.HotMS, msSince(t0))
	}

	reflow := benchLayoutOptions(w, h, size, lineHeight, justify, state.fonts)
	reflow.Typography.FontSize = 24
	t0 = time.Now()
	if _, err := eng.Layout(ctx, doc, reflow); err != nil {
		return res, err
	}
	res.ReflowMS = msSince(t0)

	repage := benchLayoutOptions(w, h, size, lineHeight, justify, state.fonts)
	repage.Typography.LineHeight = float32(lineHeight * 1.2)
	t0 = time.Now()
	if _, err := eng.Layout(ctx, doc, repage); err != nil {
		return res, err
	}
	res.RepageMS = msSince(t0)

	res.MemSys = readMemStats()
	return res, nil
}

// initLayout is the worker's boot path. It parses nothing beyond what
// render.NewFontSet needs: the canvas painter's outline face is a demo-only
// cost, so workers do not pay for it.
func initLayout(data js.Value) (initLayoutResult, error) {
	start := time.Now()
	if data.Length() == 0 {
		return initLayoutResult{}, errors.New("empty font")
	}
	fontBytes := make([]byte, data.Length())
	js.CopyBytesToGo(fontBytes, data)

	fonts, err := render.NewFontSet(render.FontFace{
		Family: "book",
		Weight: 400,
		Data:   fontBytes,
	})
	if err != nil {
		return initLayoutResult{}, fmt.Errorf("font set: %w", err)
	}
	engine, err := render.New(render.Options{Workers: 1})
	if err != nil {
		return initLayoutResult{}, fmt.Errorf("engine: %w", err)
	}

	state = demo{
		family: "book",
		fonts:  fonts,
		engine: engine,
		pal:    palettes[0],
	}
	return initLayoutResult{
		Family:    "book",
		Faces:     fonts.FontCount(),
		FaceIndex: 0,
		InitMS:    msSince(start),
	}, nil
}

func layoutChapter(text string, w, h, size, lineHeight float64, justify bool) (chapterLayoutResult, error) {
	var res chapterLayoutResult
	if state.fonts == nil {
		return res, errors.New("initLayout() must be called first")
	}
	eng := state.engine
	if eng == nil {
		e, err := render.New(render.Options{Workers: 1})
		if err != nil {
			return res, err
		}
		eng = e
	}

	align := benchAlign(justify)
	start := time.Now()
	doc, err := benchDocument(text, float32(size), float32(lineHeight), align)
	if err != nil {
		return res, err
	}
	res.BuildMS = msSince(start)

	start = time.Now()
	layout, err := eng.Layout(context.Background(), doc, benchLayoutOptions(w, h, size, lineHeight, justify, state.fonts))
	if err != nil {
		return res, err
	}
	res.LayoutMS = msSince(start)
	res.Pages = layout.PageCount()
	res.MS = res.BuildMS + res.LayoutMS
	return res, nil
}

func runBenchBook(chapters []string, w, h, size, lineHeight float64, justify bool) (bookLayoutResult, error) {
	var res bookLayoutResult
	if state.fonts == nil {
		return res, errors.New("initLayout() must be called first")
	}
	if len(chapters) == 0 {
		return res, errors.New("benchBook: no chapters")
	}
	if state.engine == nil {
		e, err := render.New(render.Options{Workers: 1})
		if err != nil {
			return res, err
		}
		state.engine = e
	}

	align := benchAlign(justify)
	start := time.Now()
	doc, anchors, err := benchBookDocument(chapters, float32(size), float32(lineHeight), align)
	if err != nil {
		return res, err
	}
	res.BuildMS = msSince(start)

	start = time.Now()
	layout, err := state.engine.Layout(context.Background(), doc, benchLayoutOptions(w, h, size, lineHeight, justify, state.fonts))
	if err != nil {
		return res, err
	}
	res.LayoutMS = msSince(start)
	res.MS = res.BuildMS + res.LayoutMS
	res.Pages = layout.PageCount()
	res.Chapters = len(chapters)

	// Chapters are paginated independently, so a chapter's first page is the
	// start of its slice of the global page array.
	firsts := make([]int, len(anchors))
	for i, a := range anchors {
		loc, ok := layout.Locate(a)
		if !ok {
			return res, fmt.Errorf("benchBook: chapter %d anchor not found", i)
		}
		page, ok := layout.PageOf(loc)
		if !ok {
			return res, fmt.Errorf("benchBook: chapter %d page not found", i)
		}
		firsts[i] = page
	}
	res.ChapterPages = make([]int, len(anchors))
	for i := range firsts {
		end := res.Pages
		if i+1 < len(firsts) {
			end = firsts[i+1]
		}
		res.ChapterPages[i] = end - firsts[i]
	}

	res.MemSys = readMemStats()
	return res, nil
}

// benchDocument builds one chapter from plain paragraphs separated by "\n".
// It mirrors what a host parser would hand the engine.
func benchDocument(text string, size, lineHeight float32, align render.TextAlign) (*render.Document, error) {
	nodes, _ := benchChapterNodes(text, render.NoAnchor)
	b := render.NewDocumentBuilder().SetStyles(benchStyles(size, lineHeight, align)).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(render.Chapter{ID: "bench", Root: 0, Nodes: nodes}); err != nil {
		return nil, err
	}
	return b.Build()
}

// benchBookDocument builds one chapter per element of chapters. It is the
// multi-chapter counterpart of benchDocument: same styles, same paragraph
// shape, only the chapter boundaries differ.
func benchBookDocument(chapters []string, size, lineHeight float32, align render.TextAlign) (*render.Document, []render.AnchorID, error) {
	b := render.NewDocumentBuilder().SetStyles(benchStyles(size, lineHeight, align)).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	anchors := make([]render.AnchorID, len(chapters))
	for i, text := range chapters {
		anchor := render.AnchorID(benchAnchorBase + i)
		nodes, ids := benchChapterNodes(text, anchor)
		if err := b.AddChapter(render.Chapter{
			ID:      fmt.Sprintf("ch%d", i),
			Root:    0,
			Nodes:   nodes,
			Anchors: ids,
		}); err != nil {
			return nil, nil, err
		}
		anchors[i] = anchor
	}
	doc, err := b.Build()
	if err != nil {
		return nil, nil, err
	}
	return doc, anchors, nil
}

func benchChapterNodes(text string, anchor render.AnchorID) ([]render.Node, map[render.AnchorID]render.NodeID) {
	nodes := []render.Node{{Kind: render.NodeBlock}}
	children := make([]render.NodeID, 0, 256)
	for _, para := range strings.Split(text, "\n") {
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
	if anchor == render.NoAnchor {
		return nodes, nil
	}
	return nodes, map[render.AnchorID]render.NodeID{anchor: 0}
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func readMemStats() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}
