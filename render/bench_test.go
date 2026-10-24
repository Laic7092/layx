package render

import (
	"context"
	"fmt"
	"testing"

	"github.com/laix/layx/render/paint"
)

func cjkBenchDoc(b *testing.B, totalRunes int) (*Document, *FontSet) {
	b.Helper()
	return cjkBenchDocAnchors(b, totalRunes, 0)
}

// cjkBenchDocAnchors builds the 5M-style corpus with anchors spread over the
// paragraph blocks. Multiple anchors may point at one block; this measures the
// anchor maps themselves without changing the text or line count.
func cjkBenchDocAnchors(b *testing.B, totalRunes, anchors int) (*Document, *FontSet) {
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
