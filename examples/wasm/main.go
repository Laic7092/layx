//go:build js && wasm

// Command wasm exposes the layx layout engine to the browser. The Go side owns
// the document, the layout and the display list; JavaScript only loads a font,
// forwards pointer events and decorates the output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/go-text/typesetting/font"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

const (
	styleDefault render.StyleID = iota
	styleTitle
	styleSubtitle
	styleMeta
	styleBody
	styleHeading
	styleQuote
	styleLink
	styleNote
)

const anchorBase = 100

type palette struct {
	name   string
	bg     paint.Color
	fg     paint.Color
	muted  paint.Color
	accent paint.Color
	quote  paint.Color
	sel    paint.Color
	caret  paint.Color
}

func rgba(r, g, b, a uint8) paint.Color { return paint.Color{R: r, G: g, B: b, A: a} }

var palettes = []palette{
	{
		name:   "paper",
		bg:     rgba(0xfa, 0xf8, 0xf3, 0xff),
		fg:     rgba(0x22, 0x24, 0x28, 0xff),
		muted:  rgba(0x6d, 0x74, 0x80, 0xff),
		accent: rgba(0x2f, 0x6f, 0xd0, 0xff),
		quote:  rgba(0x4a, 0x53, 0x60, 0xff),
		sel:    rgba(0x2f, 0x6f, 0xd0, 0x3d),
		caret:  rgba(0x2f, 0x6f, 0xd0, 0xff),
	},
	{
		name:   "sepia",
		bg:     rgba(0xf3, 0xe7, 0xd3, 0xff),
		fg:     rgba(0x3a, 0x2f, 0x24, 0xff),
		muted:  rgba(0x7a, 0x6a, 0x55, 0xff),
		accent: rgba(0x9a, 0x54, 0x12, 0xff),
		quote:  rgba(0x5c, 0x4b, 0x38, 0xff),
		sel:    rgba(0x9a, 0x54, 0x12, 0x40),
		caret:  rgba(0x9a, 0x54, 0x12, 0xff),
	},
	{
		name:   "night",
		bg:     rgba(0x14, 0x16, 0x1a, 0xff),
		fg:     rgba(0xe2, 0xe6, 0xec, 0xff),
		muted:  rgba(0x93, 0x9c, 0xa9, 0xff),
		accent: rgba(0x7a, 0xa2, 0xf7, 0xff),
		quote:  rgba(0xa8, 0xb2, 0xc2, 0xff),
		sel:    rgba(0x7a, 0xa2, 0xf7, 0x45),
		caret:  rgba(0x7a, 0xa2, 0xf7, 0xff),
	},
}

type section struct {
	Anchor int    `json:"anchor"`
	Title  string `json:"title"`
	Page   int    `json:"page"`
}

type demo struct {
	ctx        js.Value
	overlayCtx js.Value
	painter    *canvasPainter
	dpr        float64

	faces     []*font.Face
	faceIndex int
	family    string
	fonts     *render.FontSet
	engine    *render.Engine

	doc     *render.Document
	layout  *render.Layout
	page    int
	pal     palette
	selA    render.Location
	selB    render.Location
	hasSel  bool
	sections []section
}

var state demo

// ---------------------------------------------------------------- document

func docStyles(base, lineHeight float32, pal palette) []render.Style {
	return []render.Style{
		{}, // styleDefault
		{ // styleTitle
			FontSize:     base * 2.2,
			LineHeight:   1.15,
			TextAlign:    render.AlignLeft,
			MarginBottom: base * 0.25,
		},
		{ // styleSubtitle
			FontSize:     base * 1.0,
			LineHeight:   1.5,
			MarginBottom: base * 0.2,
			Color:        pal.muted,
		},
		{ // styleMeta
			FontSize:     base * 0.82,
			LineHeight:   1.45,
			MarginBottom: base * 1.35,
			Color:        pal.muted,
		},
		{ // styleBody
			FontSize:        base,
			LineHeight:      lineHeight,
			FirstLineIndent: base * 2,
			MarginBottom:    base * 0.6,
		},
		{ // styleHeading
			FontSize:     base * 1.28,
			LineHeight:   1.4,
			MarginTop:    base * 1.15,
			MarginBottom: base * 0.4,
			KeepTogether: true,
		},
		{ // styleQuote
			FontSize:        base * 0.96,
			LineHeight:      lineHeight * 0.98,
			FirstLineIndent: base * 1.2,
			MarginTop:       base * 0.5,
			MarginBottom:    base * 1.0,
			MarginLeft:      base * 1.5,
			MarginRight:     base * 0.5,
			Color:           pal.quote,
		},
		{ // styleLink
			FontSize:   base,
			LineHeight: lineHeight,
			Color:      pal.accent,
		},
		{ // styleNote
			FontSize:     base * 0.84,
			LineHeight:   1.5,
			MarginTop:    base * 0.2,
			MarginBottom: base * 0.6,
			Color:        pal.muted,
		},
	}
}

type docBuilder struct {
	nodes    []render.Node
	anchors  map[render.AnchorID]render.NodeID
	sections []section
}

func newDocBuilder() *docBuilder {
	return &docBuilder{
		nodes:   []render.Node{{Kind: render.NodeBlock}},
		anchors: make(map[render.AnchorID]render.NodeID),
	}
}

func (b *docBuilder) add(n render.Node) render.NodeID {
	b.nodes = append(b.nodes, n)
	return render.NodeID(len(b.nodes) - 1)
}

func (b *docBuilder) text(style render.StyleID, text, link string) render.NodeID {
	return b.add(render.Node{Kind: render.NodeText, Style: style, Text: text, Link: link})
}

func (b *docBuilder) block(style render.StyleID, children ...render.NodeID) render.NodeID {
	return b.add(render.Node{Kind: render.NodeBlock, Style: style, Children: children})
}

func (b *docBuilder) para(style render.StyleID, text string) render.NodeID {
	return b.block(style, b.text(0, text, ""))
}

func (b *docBuilder) heading(title string) render.NodeID {
	anchor := render.AnchorID(anchorBase + len(b.sections))
	id := b.add(render.Node{
		Kind:     render.NodeBlock,
		Style:    styleHeading,
		Children: []render.NodeID{b.text(0, title, "")},
		Anchor:   anchor,
	})
	b.anchors[anchor] = id
	b.sections = append(b.sections, section{Anchor: int(anchor), Title: title})
	return id
}

func buildDocument(base, lineHeight float32, pal palette) (*render.Document, []section, error) {
	b := newDocBuilder()

	title := b.para(styleTitle, "layx")
	subtitle := b.para(styleSubtitle, "纯 Go 的 reflowable 排版内核")
	meta := b.para(styleMeta, "WebAssembly + Canvas 2D 演示 · 分页 · 命中测试 · 选区")

	var flow []render.NodeID
	flow = append(flow, title, subtitle, meta)

	add := func(ids ...render.NodeID) { flow = append(flow, ids...) }

	add(
		b.heading("为什么是内核，而不是阅读器"),
		b.para(styleBody, "layx 只做一件事：把宿主已经解析、级联好的文档排成页。它不解析 EPUB，不实现 CSS 选择器，也不内置光栅器。输入是 render.Document，输出是分页结果、DisplayList，以及命中、选区与锚点映射。"),
		b.para(styleBody, "这个边界看起来苛刻，但正是它让内核可以被复用：同一份排版结果可以画到 GPU、软件光栅器，甚至电子墨水屏上。宿主负责把 Location 映射成 CFI、书签或数据库里的阅读进度。"),
		b.para(styleBody, "The quick brown fox jumps over the lazy dog. Reflowable text looks like a solved problem until the viewport changes, a font falls back, or a paragraph has to break in the middle. Then every assumption shows up at once."),
	)

	link := b.block(styleBody,
		b.text(0, "源码在 ", ""),
		b.text(styleLink, "GitHub", "https://github.com/laix/layx"),
		b.text(0, "，最小示例在 examples/layout。", ""),
	)
	add(link)

	add(
		b.heading("一次排版的数据流"),
		b.para(styleBody, "宿主把 XHTML 与 CSS 编译成扁平的节点 arena，再交给引擎。引擎用 go-text/typesetting 做 shaping 和 UAX #14 断行，把段落切成行盒，再按页高切页。"),
		b.para(styleBody, "Page 第一次被访问时才生成 DisplayList，之后按 PageCache 缓存。Page.Draw 只回放绘制指令，不重新 shaping，所以翻页和重绘是两件独立的事。"),
		b.para(styleQuote, "排版和绘制分开之后，翻页就只是换一个 DisplayList 回放而已。"),
		b.para(styleBody, "中英文混排时，行首禁则由 UAX #14 覆盖：，。、；：！？）等标点不会出现在行首；西文单词优先整体换行，必要时才断词。两端对齐的额外空间只分配给可拉伸的空格与 CJK 字符之间。"),
	)

	add(
		b.heading("分页不是罪恶"),
		b.para(styleBody, "reflowable 阅读器里，滚动和分页经常被对立起来。但分页真正提供的是稳定性：页高固定，widows 和 orphans 才有意义，跨页选区才有明确的矩形。"),
		b.para(styleBody, "引擎支持页边距、break-before/after、KeepTogether、widows/orphans。标题如果需要和后面的段落待在一起，只要在样式里打开 KeepTogether，剩下的交给分页器。"),
		b.para(styleBody, "图片按固有宽高比预留空间，超宽时等比缩放，居中或左右对齐，并且不跨页断开。图片没有自己的尺寸时，宿主可以只给宽度或高度，由布局补全另一边。"),
	)

	add(
		b.heading("命中、选区和锚点"),
		b.para(styleBody, "Page.HitTest 把页面坐标映射回 Location{Chapter, Node, Anchor, Offset}。Node 和 Offset 用来做命中、选区和搜索；Anchor 是宿主可以持久化的稳定锚点，重排之后仍然指向同一段文字。"),
		b.para(styleBody, "在下面的画布上拖动鼠标试试：高亮的矩形来自 Layout.Selection，返回的是同一套 Location。点击段落则会落下光标，并显示命中的 node、offset 和 anchor。"),
		b.para(styleBody, "跨页选区、跨章节选区走的是同一条路径。宿主需要做的，只是把 Location 翻译成自己的 CFI、书签或阅读进度。"),
	)

	add(
		b.heading("性能与内存"),
		b.para(styleBody, "本机基准：Noto Sans CJK SC、18px、600×900 视口、5,000,000 字，只做布局不含绘制。1 worker 用时 4.37s，8 workers 0.92s，16 workers 0.87s。"),
		b.para(styleBody, "常驻内存只保留行盒、页映射、段落 rune 和锚点，不保留全书的 shaped glyph。shaped glyph 是排版过程中的临时对象，M1 会用对象池和分块复用来压低分配量。"),
		b.para(styleBody, "这次演示跑在 WebAssembly 里，GOMAXPROCS 为 1，所以排版会明显慢一些。但 WASM 版和原生版是同一份 Go 代码：同一套 IR、同一套分页、同一套 DisplayList。"),
		b.para(styleNote, "提示：拖动鼠标选中文字；单击落光标；点击链接会打开新标签页；← → 翻页。"),
	)

	b.nodes[0].Children = flow

	styles := docStyles(base, lineHeight, pal)
	d := render.NewDocumentBuilder().
		SetStyles(styles).
		SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
	if err := d.AddChapter(render.Chapter{
		ID:      "demo",
		Root:    0,
		Nodes:   b.nodes,
		Anchors: b.anchors,
	}); err != nil {
		return nil, nil, err
	}
	doc, err := d.Build()
	if err != nil {
		return nil, nil, err
	}
	return doc, b.sections, nil
}

// ---------------------------------------------------------------- api

type layoutResult struct {
	Pages    int       `json:"pages"`
	MS       float64   `json:"ms"`
	Sections []section `json:"sections"`
}

type renderResult struct {
	Page  int     `json:"page"`
	Pages int     `json:"pages"`
	MS    float64 `json:"ms"`
}

type pointInfo struct {
	Node   int32 `json:"node"`
	Anchor int   `json:"anchor"`
	Offset int   `json:"offset"`
}

type selectionResult struct {
	OK    bool      `json:"ok"`
	Caret bool      `json:"caret"`
	A     pointInfo `json:"a"`
	B     pointInfo `json:"b"`
}

func pointOf(loc render.Location) pointInfo {
	return pointInfo{Node: int32(loc.Node), Anchor: int(loc.Anchor), Offset: loc.Offset}
}

func (d *demo) relayout(w, h, base, lineHeight float64, justify bool, theme int) (layoutResult, error) {
	if d.fonts == nil {
		return layoutResult{}, errors.New("boot() must be called first")
	}
	if w < 240 || h < 240 {
		return layoutResult{}, fmt.Errorf("viewport too small: %.0fx%.0f", w, h)
	}
	if base < 8 || base > 72 {
		return layoutResult{}, fmt.Errorf("font size out of range: %.1f", base)
	}
	if lineHeight < 1 || lineHeight > 3 {
		return layoutResult{}, fmt.Errorf("line height out of range: %.2f", lineHeight)
	}
	if theme < 0 || theme >= len(palettes) {
		theme = 0
	}
	pal := palettes[theme]

	doc, sections, err := buildDocument(float32(base), float32(lineHeight), pal)
	if err != nil {
		return layoutResult{}, err
	}

	align := render.AlignLeft
	if justify {
		align = render.AlignJustify
	}

	start := time.Now()
	layout, err := d.engine.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: float32(w), Height: float32(h)},
		Typography: render.Typography{
			FontFamily: d.family,
			FontSize:   float32(base),
			LineHeight: float32(lineHeight),
			TextAlign:  align,
			Color:      pal.fg,
			Background: pal.bg,
		},
		Fonts:   d.fonts,
		Margins: paint.Insets{Top: 56, Right: 60, Bottom: 56, Left: 60},
	})
	if err != nil {
		return layoutResult{}, err
	}
	ms := float64(time.Since(start).Microseconds()) / 1000

	for i := range sections {
		if loc, ok := layout.Locate(render.AnchorID(sections[i].Anchor)); ok {
			if page, ok := layout.PageOf(loc); ok {
				sections[i].Page = page
			}
		}
	}

	d.doc = doc
	d.layout = layout
	d.pal = pal
	d.sections = sections
	d.hasSel = false
	if d.page >= layout.PageCount() {
		d.page = layout.PageCount() - 1
	}
	if d.page < 0 {
		d.page = 0
	}

	return layoutResult{Pages: layout.PageCount(), MS: ms, Sections: sections}, nil
}

func (d *demo) renderPage(page int) (renderResult, error) {
	if d.layout == nil {
		return renderResult{}, errors.New("no layout")
	}
	pages := d.layout.PageCount()
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	d.page = page

	p := d.layout.Page(page)
	if p == nil {
		return renderResult{}, fmt.Errorf("page %d out of range", page)
	}

	start := time.Now()
	size := p.Size()
	d.ctx.Call("setTransform", d.dpr, 0, 0, d.dpr, 0, 0)
	d.ctx.Call("clearRect", 0, 0, size.Width, size.Height)
	d.painter.reset()
	d.painter.ctx = d.ctx
	p.Draw(d.painter)
	ms := float64(time.Since(start).Microseconds()) / 1000

	return renderResult{Page: page, Pages: pages, MS: ms}, nil
}

// drawOverlay repaints only the selection layer, so dragging never has to
// replay the page display list.
func (d *demo) drawOverlay(page int) (renderResult, error) {
	if d.layout == nil {
		return renderResult{}, errors.New("no layout")
	}
	pages := d.layout.PageCount()
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	p := d.layout.Page(page)
	if p == nil {
		return renderResult{}, fmt.Errorf("page %d out of range", page)
	}

	start := time.Now()
	size := p.Size()
	ctx := d.overlayCtx
	ctx.Call("setTransform", d.dpr, 0, 0, d.dpr, 0, 0)
	ctx.Call("clearRect", 0, 0, size.Width, size.Height)
	d.painter.reset()
	d.painter.ctx = ctx
	d.drawSelection()
	d.painter.ctx = d.ctx
	ms := float64(time.Since(start).Microseconds()) / 1000

	return renderResult{Page: page, Pages: pages, MS: ms}, nil
}

func (d *demo) drawSelection() {
	if !d.hasSel || d.layout == nil {
		return
	}
	if d.selA == d.selB {
		to := d.selA
		to.Offset++
		for _, q := range d.layout.Selection(d.selA, to) {
			if q.Page != d.page {
				continue
			}
			d.painter.FillRect(paint.Rect{
				X:      q.Rect.X,
				Y:      q.Rect.Y + 1,
				Width:  1.6,
				Height: q.Rect.Height - 2,
			}, d.pal.caret)
		}
		return
	}
	for _, q := range d.layout.Selection(d.selA, d.selB) {
		if q.Page == d.page {
			d.painter.FillRect(q.Rect, d.pal.sel)
		}
	}
}

func (d *demo) hit(x, y float64) (render.Location, bool) {
	if d.layout == nil {
		return render.Location{}, false
	}
	p := d.layout.Page(d.page)
	if p == nil {
		return render.Location{}, false
	}
	return p.HitTest(paint.Point{X: float32(x), Y: float32(y)})
}

func (d *demo) selectRange(x0, y0, x1, y1 float64) (selectionResult, error) {
	a, ok := d.hit(x0, y0)
	if !ok {
		return selectionResult{}, errors.New("no layout")
	}
	b, ok := d.hit(x1, y1)
	if !ok {
		return selectionResult{}, errors.New("no layout")
	}
	d.selA, d.selB = a, b
	d.hasSel = true
	return selectionResult{
		OK:    true,
		Caret: a == b,
		A:     pointOf(a),
		B:     pointOf(b),
	}, nil
}

func (d *demo) linkAt(x, y float64) string {
	if d.layout == nil {
		return ""
	}
	p := d.layout.Page(d.page)
	if p == nil {
		return ""
	}
	pt := paint.Point{X: float32(x), Y: float32(y)}
	for _, l := range p.Links() {
		if l.Rect.Contains(pt) {
			return l.URL
		}
	}
	return ""
}

// ---------------------------------------------------------------- boot

func pickFace(faces []*font.Face) int {
	best := 0
	bestScore := -1
	for i, f := range faces {
		name := f.Describe().Family
		score := 0
		if strings.Contains(name, "CJK") {
			score = 1
		}
		if strings.Contains(name, "CJK SC") || strings.HasSuffix(name, " SC") {
			score = 2
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func boot(canvas, overlay js.Value, data js.Value, dpr float64) (any, error) {
	if data.Length() == 0 {
		return nil, errors.New("empty font")
	}
	fontBytes := make([]byte, data.Length())
	js.CopyBytesToGo(fontBytes, data)

	faces, err := newOutlineFace(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	index := pickFace(faces)
	face := faces[index]

	fonts, err := render.NewFontSet(render.FontFace{
		Family: "book",
		Weight: 400,
		Data:   fontBytes,
		Index:  index,
	})
	if err != nil {
		return nil, fmt.Errorf("font set: %w", err)
	}
	engine, err := render.New(render.Options{Workers: 1})
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}

	ctx := canvas.Call("getContext", "2d")
	overlayCtx := overlay.Call("getContext", "2d")
	if ctx.IsNull() || ctx.IsUndefined() || overlayCtx.IsNull() || overlayCtx.IsUndefined() {
		return nil, errors.New("canvas 2d context unavailable")
	}

	state = demo{
		ctx:        ctx,
		overlayCtx: overlayCtx,
		painter:    newCanvasPainter(ctx, face, index),
		dpr:        dpr,
		faces:     faces,
		faceIndex: index,
		family:    "book",
		fonts:     fonts,
		engine:    engine,
		pal:       palettes[0],
	}

	return map[string]any{
		"family":    face.Describe().Family,
		"upem":      face.Upem(),
		"faceIndex": index,
		"faces":     len(faces),
	}, nil
}

// ---------------------------------------------------------------- exports

var kept []js.Func

func export(api js.Value, name string, fn func(args []js.Value) (any, error)) {
	f := js.FuncOf(func(this js.Value, args []js.Value) any {
		v, err := fn(args)
		if err != nil {
			js.Global().Get("console").Call("error", "layx."+name+": "+err.Error())
			return reply(map[string]any{"error": err.Error()})
		}
		return reply(v)
	})
	kept = append(kept, f)
	api.Set(name, f)
}

func reply(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"encode failed"}`
	}
	return string(b)
}

func need(args []js.Value, n int) error {
	if len(args) < n {
		return fmt.Errorf("expected %d arguments, got %d", n, len(args))
	}
	return nil
}

func main() {
	api := js.Global().Get("Object").New()
	js.Global().Set("layx", api)

	export(api, "boot", func(args []js.Value) (any, error) {
		if err := need(args, 4); err != nil {
			return nil, err
		}
		return boot(args[0], args[1], args[2], args[3].Float())
	})

	export(api, "layout", func(args []js.Value) (any, error) {
		if err := need(args, 6); err != nil {
			return nil, err
		}
		return state.relayout(
			args[0].Float(), args[1].Float(),
			args[2].Float(), args[3].Float(),
			args[4].Bool(), args[5].Int(),
		)
	})

	export(api, "render", func(args []js.Value) (any, error) {
		if err := need(args, 1); err != nil {
			return nil, err
		}
		return state.renderPage(args[0].Int())
	})

	export(api, "overlay", func(args []js.Value) (any, error) {
		if err := need(args, 1); err != nil {
			return nil, err
		}
		return state.drawOverlay(args[0].Int())
	})

	export(api, "select", func(args []js.Value) (any, error) {
		if err := need(args, 4); err != nil {
			return nil, err
		}
		return state.selectRange(args[0].Float(), args[1].Float(), args[2].Float(), args[3].Float())
	})

	export(api, "clearSelection", func(args []js.Value) (any, error) {
		state.hasSel = false
		return selectionResult{OK: true}, nil
	})

	export(api, "hitTest", func(args []js.Value) (any, error) {
		if err := need(args, 2); err != nil {
			return nil, err
		}
		loc, ok := state.hit(args[0].Float(), args[1].Float())
		if !ok {
			return map[string]any{"ok": false}, nil
		}
		return map[string]any{"ok": true, "hit": pointOf(loc)}, nil
	})

	export(api, "linkAt", func(args []js.Value) (any, error) {
		if err := need(args, 2); err != nil {
			return nil, err
		}
		return map[string]any{"url": state.linkAt(args[0].Float(), args[1].Float())}, nil
	})

	select {}
}
