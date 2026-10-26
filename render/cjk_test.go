package render

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

var cjkCandidates = []string{
	"/home/laix/.local/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
}

func cjkFace(tb testing.TB) (FontFace, bool) {
	tb.Helper()
	path := os.Getenv("LAYX_CJK_FONT")
	if path == "" {
		for _, c := range cjkCandidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}
	if path == "" {
		return FontFace{}, false
	}
	index := 2
	if strings.Contains(path, "wqy") || strings.Contains(path, "uming") {
		index = 0
	}
	if v := os.Getenv("LAYX_CJK_FONT_INDEX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			index = n
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Logf("CJK font unavailable: %v", err)
		return FontFace{}, false
	}
	return FontFace{Family: "CJK", Weight: 400, Data: data, Index: index}, true
}

func TestCJKLineBreakingAndFallback(t *testing.T) {
	cjk, ok := cjkFace(t)
	if !ok {
		t.Skip("no CJK font available")
	}
	latin := loadTestFont(t)
	fs, err := NewFontSet(latin, cjk)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("中文排版测试，包含标点符号。这是第二句！还有英文 mixed text 与数字 12345。", 20)
	nodes := []Node{
		{Kind: NodeBlock, Style: 1, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	styles := []Style{
		{},
		{FontFamily: "CJK", FontSize: 18, LineHeight: 1.6, Color: paint.Color{A: 0xff}},
	}
	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, _ := b.Build()
	e := mustEngine(t, 4)
	l, err := e.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 360, Height: 600},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 20, Right: 20, Bottom: 20, Left: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	cf := &l.chapters[0]
	if cf.count() < 10 {
		t.Fatalf("expected many CJK lines, got %d", cf.count())
	}
	forbidden := "，。、；：！？）」』】》"
	for _, it := range itemsOf(cf) {
		if it.kind != itemLine {
			continue
		}
		txt := string(paraRunes(it.para)[it.start:it.end])
		if txt == "" {
			continue
		}
		for _, r := range forbidden {
			if strings.HasPrefix(txt, string(r)) {
				t.Fatalf("line starts with forbidden punctuation %q: %q", r, txt)
			}
		}
		if it.width > it.avail+0.5 {
			t.Fatalf("CJK line too wide: %v > %v", it.width, it.avail)
		}
	}
	rp := &recordPainter{}
	l.Page(0).Draw(rp)
	if rp.glyphs == 0 {
		t.Fatal("no CJK glyphs drawn")
	}
	foundCJK := false
	for _, f := range rp.fonts {
		if f == "CJK" {
			foundCJK = true
		}
	}
	if !foundCJK {
		t.Fatalf("fallback did not select CJK face: %v", rp.fonts)
	}
}

// TestCJKGolden pins a small deterministic CJK layout.
func TestCJKGolden(t *testing.T) {
	cjk, ok := cjkFace(t)
	if !ok {
		t.Skip("no CJK font available")
	}
	fs, err := NewFontSet(cjk)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("天地玄黄，宇宙洪荒。日月盈昃，辰宿列张。", 40)
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	b := NewDocumentBuilder().SetStyles([]Style{{}, {FontFamily: "CJK", FontSize: 18, LineHeight: 1.6, Color: paint.Color{A: 0xff}}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, _ := b.Build()
	l, err := mustEngine(t, 8).Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: 360, Height: 600},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 20, Right: 20, Bottom: 20, Left: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	cf := &l.chapters[0]
	items := itemsOf(cf)
	lines := 0
	for _, it := range items {
		if it.kind == itemLine {
			lines++
		}
	}
	if lines == 0 || l.PageCount() == 0 {
		t.Fatal("empty CJK layout")
	}
	// Lines must be split at CJK boundaries; each line should be non-empty and
	// no wider than the content box.
	for _, it := range itemsOf(cf) {
		if it.kind != itemLine {
			continue
		}
		if it.end <= it.start {
			t.Fatal("empty line item")
		}
		if it.width > 320.5 {
			t.Fatalf("line exceeds content width: %v", it.width)
		}
	}
}
