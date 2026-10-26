package render

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laix/layx/render/paint"
)

func cjkBenchDoc(b testing.TB, totalRunes int) (*Document, *FontSet) {
	b.Helper()
	return cjkBenchDocAnchors(b, totalRunes, 0)
}

// cjkBenchDocAnchors builds the 5M-style corpus with anchors spread over the
// paragraph blocks. Multiple anchors may point at one block; this measures the
// anchor maps themselves without changing the text or line count.
func cjkBenchDocAnchors(b testing.TB, totalRunes, anchors int) (*Document, *FontSet) {
	b.Helper()
	face, ok := cjkFace(b)
	if !ok {
		b.Skip("no CJK font available; set LAYX_CJK_FONT to run the 5M benchmark")
	}
	fs, err := NewFontSet(face)
	if err != nil {
		b.Fatal(err)
	}
	chunk := 500
	blocks := totalRunes / chunk
	if blocks < 1 {
		blocks = 1
	}
	base := []rune("天地玄黄，宇宙洪荒。日月盈昃，辰宿列张。寒来暑往，秋收冬藏。闰余成岁，律吕调阳。云腾致雨，露结为霜。金生丽水，玉出昆冈。剑号巨阙，珠称夜光。果珍李柰，菜重芥姜。")
	para := make([]rune, chunk)
	for i := range para {
		para[i] = base[i%len(base)]
	}
	text := string(para)

	children := make([]NodeID, 0, blocks)
	nodes := make([]Node, 0, blocks*2+1)
	nodes = append(nodes, Node{Kind: NodeBlock})
	for i := 0; i < blocks; i++ {
		bi := NodeID(len(nodes))
		nodes = append(nodes, Node{Kind: NodeBlock, Style: 1, Children: []NodeID{bi + 1}})
		nodes = append(nodes, Node{Kind: NodeText, Text: text})
		children = append(children, bi)
	}
	nodes[0].Children = children

	styles := []Style{
		{},
		{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff}, MarginBottom: 8,
		},
	}
	var anchorMap map[AnchorID]NodeID
	if anchors > 0 {
		anchorMap = make(map[AnchorID]NodeID, anchors)
		per := anchors / blocks
		if per < 1 {
			per = 1
		}
		id := AnchorID(1)
		for i := 0; i < blocks && len(anchorMap) < anchors; i++ {
			node := NodeID(2*i + 1)
			for j := 0; j < per && len(anchorMap) < anchors; j++ {
				anchorMap[id] = node
				id++
			}
		}
	}
	builder := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	if err := builder.AddChapter(Chapter{ID: "long", Root: 0, Nodes: nodes, Anchors: anchorMap}); err != nil {
		b.Fatal(err)
	}
	doc, err := builder.Build()
	if err != nil {
		b.Fatal(err)
	}
	return doc, fs
}

func benchLayout(b *testing.B, totalRunes, workers int) {
	doc, fs := cjkBenchDoc(b, totalRunes)
	e, err := New(Options{Workers: workers, PageCache: 4})
	if err != nil {
		b.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	b.SetBytes(int64(totalRunes))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := e.Layout(context.Background(), doc, opts)
		if err != nil {
			b.Fatal(err)
		}
		if l.PageCount() == 0 {
			b.Fatal("no pages")
		}
	}
}

func BenchmarkLayout5M(b *testing.B) {
	b.Run("workers=1", func(b *testing.B) { benchLayout(b, 5_000_000, 1) })
	b.Run("workers=4", func(b *testing.B) { benchLayout(b, 5_000_000, 4) })
	b.Run("workers=8", func(b *testing.B) { benchLayout(b, 5_000_000, 8) })
	b.Run("workers=16", func(b *testing.B) { benchLayout(b, 5_000_000, 16) })
}

func BenchmarkLayout200K(b *testing.B) {
	benchLayout(b, 200_000, 8)
}

// BenchmarkLayoutPhases reports the per-stage split of a cold 200K-rune layout.
// Run it with -v to see the numbers; it is a diagnostic, not a competitive
// measurement.
func BenchmarkLayoutPhases(b *testing.B) {
	doc, fs := cjkBenchDoc(b, 200_000)
	e, err := New(Options{Workers: 1, Measure: true, PageCache: 4})
	if err != nil {
		b.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := e.Layout(context.Background(), doc, opts)
		if err != nil {
			b.Fatal(err)
		}
		if l.PageCount() == 0 {
			b.Fatal("no pages")
		}
	}
	b.StopTimer()
	s := e.Stats()
	n := time.Duration(s.Layouts)
	if n <= 0 {
		b.Fatal("no measured layouts")
	}
	b.Logf("per layout: total=%v keys=%v break=%v stack=%v paginate=%v assemble=%v shapes=%d pages=%d",
		s.Total/n, s.Keys/n, s.Break/n, s.Stack/n, s.Paginate/n, s.Assemble/n,
		s.Shapes/s.Layouts, s.Pages/s.Layouts)
}

// BenchmarkLayoutAnchors measures the cost of the anchor maps in break/page
// placement. Anchors are attached to paragraph blocks, the granularity the
// API recommends; 1M anchors is the "anchor every node" pathological case.
func BenchmarkLayoutAnchors(b *testing.B) {
	for _, anchors := range []int{0, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("anchors=%d", anchors), func(b *testing.B) {
			doc, fs := cjkBenchDocAnchors(b, 5_000_000, anchors)
			e, err := New(Options{Workers: 8, PageCache: 2})
			if err != nil {
				b.Fatal(err)
			}
			opts := LayoutOptions{
				Viewport: Viewport{Width: 600, Height: 900},
				Typography: Typography{
					FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
					Color: paint.Color{A: 0xff},
				},
				Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
			}
			b.SetBytes(5_000_000)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l, err := e.Layout(context.Background(), doc, opts)
				if err != nil {
					b.Fatal(err)
				}
				if l.PageCount() == 0 {
					b.Fatal("no pages")
				}
			}
		})
	}
}

// BenchmarkDocumentText measures the on-demand text projection. It is not part
// of Layout; the allocation per iteration is the chapter string plus one
// TextRun per reachable node, which a search or copy host is expected to cache.
func BenchmarkDocumentText(b *testing.B) {
	for _, runes := range []int{200_000, 5_000_000} {
		b.Run(fmt.Sprintf("runes=%d", runes), func(b *testing.B) {
			doc, _ := cjkBenchDoc(b, runes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				text, runs, ok := doc.Text(0)
				if !ok || len(text) == 0 || len(runs) == 0 {
					b.Fatal("Text returned no chapter")
				}
			}
		})
	}
}

func BenchmarkRelayoutGap200K(b *testing.B) {
	doc, fs := cjkBenchDoc(b, 200_000)
	e, err := New(Options{Workers: 8, PageCache: 2})
	if err != nil {
		b.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff}, ParagraphGap: 8,
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := opts
		o.Typography.ParagraphGap = float32(8 + i%2)
		if _, err := l.Relayout(context.Background(), o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageDraw(b *testing.B) {
	doc := testDocument(b, "")
	l := testLayout(b, doc, 2)
	p := l.Page(0)
	rp := &recordPainter{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rp.glyphs = 0
		p.Draw(rp)
	}
}

func BenchmarkHitTest(b *testing.B) {
	doc := testDocument(b, "")
	l := testLayout(b, doc, 2)
	p := l.Page(0)
	pt := paint.Point{X: 130, Y: 120}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := p.HitTest(pt); !ok {
			b.Fatal("hit test failed")
		}
	}
}

// BenchmarkLayoutScale is the parallel-scaling measurement: the same 200K-rune
// chapter with an increasing worker budget. The ratio to workers=1 is what the
// concurrency work buys; it is bounded by the cores, not by the task granularity.
func BenchmarkLayoutScale(b *testing.B) {
	for _, w := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) { benchLayout(b, 200_000, w) })
	}
}

// BenchmarkConcurrentLayouts measures the whole library under load: several
// layouts in flight on one Engine, each a distinct variant so every call breaks
// and paginates. Without a shared budget each call starts its own Workers
// goroutines and they thrash; with one, throughput is what the cores allow.
func BenchmarkConcurrentLayouts(b *testing.B) {
	for _, conc := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("conc=%d", conc), func(b *testing.B) {
			doc, fs := cjkBenchDoc(b, 200_000)
			e, err := New(Options{Workers: 0, PageCache: 2})
			if err != nil {
				b.Fatal(err)
			}
			opts := LayoutOptions{
				Viewport: Viewport{Width: 600, Height: 900},
				Typography: Typography{
					FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
					Color: paint.Color{A: 0xff},
				},
				Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
			}
			var wg sync.WaitGroup
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wg.Add(conc)
				for c := 0; c < conc; c++ {
					go func(c int) {
						defer wg.Done()
						o := opts
						o.Typography.ParagraphGap = float32(1 + c)
						if _, err := e.Layout(context.Background(), doc, o); err != nil {
							b.Error(err)
						}
					}(c)
				}
				wg.Wait()
			}
		})
	}
}

// BenchmarkPrefetchPages measures the spike a page turn used to pay: building
// a window of display lists one at a time versus on the engine's budget.
func BenchmarkPrefetchPages(b *testing.B) {
	doc, fs := cjkBenchDoc(b, 200_000)
	opts := LayoutOptions{
		Viewport: Viewport{Width: 640, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	e, err := New(Options{Workers: 0, PageCache: 32})
	if err != nil {
		b.Fatal(err)
	}
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		b.Fatal(err)
	}
	const window = 16
	b.Run("lazy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			l.cacheMu.Lock()
			l.cache = make(map[int]*Page)
			l.cacheOrder = nil
			l.cacheMu.Unlock()
			l.lineCache = newLineCache(l.engine.lineCache)
			for p := 0; p < window; p++ {
				l.Page(p)
			}
		}
	})
	b.Run("prefetch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			l.cacheMu.Lock()
			l.cache = make(map[int]*Page)
			l.cacheOrder = nil
			l.cacheMu.Unlock()
			l.lineCache = newLineCache(l.engine.lineCache)
			if err := l.Prefetch(context.Background(), 0, window); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRelayoutMultiChapter measures the interactive path on a book-shaped
// document: 100 chapters, a vertical-only change (paragraph gap), so no shaping
// or line breaking happens and the remaining stack + paginate work is what the
// relayout costs.
func BenchmarkRelayoutMultiChapter(b *testing.B) {
	face, ok := cjkFace(b)
	if !ok {
		b.Skip("no CJK font available; set LAYX_CJK_FONT to run the relayout benchmark")
	}
	fs, err := NewFontSet(face)
	if err != nil {
		b.Fatal(err)
	}
	const chapters, paragraphs = 100, 40
	text := strings.Repeat("天地玄黄，宇宙洪荒。日月盈昃，辰宿列张。寒来暑往，秋收冬藏。", 2)
	styles := []Style{
		{},
		{FontFamily: "CJK", FontSize: 18, LineHeight: 1.6, Color: paint.Color{A: 0xff}},
	}
	builder := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	for c := 0; c < chapters; c++ {
		nodes := make([]Node, 0, paragraphs*2+1)
		nodes = append(nodes, Node{Kind: NodeBlock, Style: 1})
		children := make([]NodeID, 0, paragraphs*2)
		for p := 0; p < paragraphs; p++ {
			children = append(children, NodeID(len(nodes)))
			nodes = append(nodes, Node{Kind: NodeText, Text: text})
			children = append(children, NodeID(len(nodes)))
			nodes = append(nodes, Node{Kind: NodeBreak})
		}
		nodes[0].Children = children
		if err := builder.AddChapter(Chapter{ID: fmt.Sprintf("ch%d", c), Root: 0, Nodes: nodes}); err != nil {
			b.Fatal(err)
		}
	}
	doc, err := builder.Build()
	if err != nil {
		b.Fatal(err)
	}
	e, err := New(Options{Workers: 0, PageCache: 2})
	if err != nil {
		b.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff}, ParagraphGap: 8,
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := opts
		o.Typography.ParagraphGap = float32(8 + i%2)
		if _, err := l.Relayout(context.Background(), o); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForegroundUnderLoad is the interactive case the budget exists for: a
// small layout (the page the reader just triggered) while a background layout
// of a large book is already running on the same Engine. Without a shared
// budget the foreground call starts its own full set of workers and waits behind
// the background one.
func BenchmarkForegroundUnderLoad(b *testing.B) {
	small, fs := cjkBenchDoc(b, 20_000)
	big, _ := cjkBenchDoc(b, 200_000)
	e, err := New(Options{Workers: 0, PageCache: 2})
	if err != nil {
		b.Fatal(err)
	}
	opts := func(gap float32) LayoutOptions {
		return LayoutOptions{
			Viewport: Viewport{Width: 600, Height: 900},
			Typography: Typography{
				FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
				Color: paint.Color{A: 0xff}, ParagraphGap: gap,
			},
			Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
		}
	}
	stop := make(chan struct{})
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := e.Layout(context.Background(), big, opts(float32(i%2))); err != nil {
				return
			}
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Layout(context.Background(), small, opts(float32(i%2))); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	close(stop)
	bg.Wait()
}
