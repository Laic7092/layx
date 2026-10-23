//go:build js && wasm

package main

import (
	"context"
	"errors"
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

func registerBench(api js.Value) {
	export(api, "bench", func(args []js.Value) (any, error) {
		// text, w, h, size, lineHeight, justify, turns
		if err := need(args, 7); err != nil {
			return nil, err
		}
		return runBench(args[0].String(), args[1].Float(), args[2].Float(),
			args[3].Float(), args[4].Float(), args[5].Bool(), args[6].Int())
	})
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
	align := render.AlignLeft
	if justify {
		align = render.AlignJustify
	}
	base := render.LayoutOptions{
		Viewport: render.Viewport{Width: float32(w), Height: float32(h)},
		Typography: render.Typography{
			FontFamily: "book", FontSize: float32(size), LineHeight: float32(lineHeight),
			Color: paint.Color{A: 0xff},
			// Chrome's multicol fragmentation does not apply widows/orphans;
			// disable them here so both sides do the same amount of work.
			Widows: 1, Orphans: 1,
		},
		Fonts:   state.fonts,
		Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}

	t0 := time.Now()
	doc, err := benchDocument(text, float32(size), float32(lineHeight), align)
	if err != nil {
		return res, err
	}
	res.BuildMS = msSince(t0)

	t0 = time.Now()
	layout, err := eng.Layout(context.Background(), doc, base)
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

	reflow := base
	reflow.Typography.FontSize = 24
	t0 = time.Now()
	if _, err := eng.Layout(ctx, doc, reflow); err != nil {
		return res, err
	}
	res.ReflowMS = msSince(t0)

	repage := base
	repage.Typography.LineHeight = float32(lineHeight * 1.2)
	t0 = time.Now()
	if _, err := eng.Layout(ctx, doc, repage); err != nil {
		return res, err
	}
	res.RepageMS = msSince(t0)

	res.MemSys = readMemStats()
	return res, nil
}

// benchDocument builds one chapter from plain paragraphs separated by "\n".
// It mirrors what a host parser would hand the engine.
func benchDocument(text string, size, lineHeight float32, align render.TextAlign) (*render.Document, error) {
	styles := []render.Style{
		{},
		{
			FontFamily: "book", FontSize: size, LineHeight: lineHeight,
			Color: paint.Color{A: 0xff}, TextAlign: align, MarginBottom: size * 0.5,
			FirstLineIndent: size * 2,
		},
	}
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

	b := render.NewDocumentBuilder().SetStyles(styles).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(render.Chapter{ID: "bench", Root: 0, Nodes: nodes}); err != nil {
		return nil, err
	}
	return b.Build()
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func readMemStats() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}
