package render

import (
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

func textRunFor(t *testing.T, runs []TextRun, node NodeID) TextRun {
	t.Helper()
	for _, r := range runs {
		if r.Node == node {
			return r
		}
	}
	t.Fatalf("no TextRun for node %d", node)
	return TextRun{}
}

func TestDocumentTextReadsOrderAndRuns(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2, 3, 4}},
		{Kind: NodeBlock, Children: []NodeID{5}},
		{Kind: NodeInline, Children: []NodeID{6}},
		{Kind: NodeImage, Image: 7, Width: 10, Height: 10},
		{Kind: NodeBlock, Children: []NodeID{7}},
		{Kind: NodeText, Text: "Hello"},
		{Kind: NodeText, Text: " 世界"},
		{Kind: NodeText, Text: "!"},
	}
	b := NewDocumentBuilder().
		SetStyles([]Style{{}, {FontSize: 16}}).
		SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	text, runs, ok := doc.Text(0)
	if !ok {
		t.Fatal("Text(0) failed")
	}
	if text != "Hello 世界!" {
		t.Fatalf("text = %q, want %q", text, "Hello 世界!")
	}
	if len(runs) != 8 {
		t.Fatalf("runs = %d, want 8", len(runs))
	}

	want := map[NodeID][2]int32{
		0: {0, 0}, // root
		1: {0, 0}, // block before Hello
		5: {0, 5}, // Hello
		2: {5, 5}, // inline, zero width
		6: {5, 8}, // " 世界"
		3: {8, 8}, // image, zero width
		4: {8, 8}, // block before "!"
		7: {8, 9}, // "!"
	}
	for node, w := range want {
		r := textRunFor(t, runs, node)
		if r.RuneStart != w[0] || r.RuneEnd != w[1] {
			t.Fatalf("node %d run = [%d,%d), want [%d,%d)", node, r.RuneStart, r.RuneEnd, w[0], w[1])
		}
	}
	for _, r := range runs {
		if r.Node != 5 && r.Node != 6 && r.Node != 7 && r.RuneStart != r.RuneEnd {
			t.Fatalf("non-text node %d has width %d", r.Node, r.RuneEnd-r.RuneStart)
		}
	}

	runes := []rune(text)
	if got := string(runes[5:8]); got != " 世界" {
		t.Fatalf("rune range [5,8) = %q, want %q", got, " 世界")
	}
	if _, _, ok := doc.Text(-1); ok {
		t.Fatal("Text(-1) succeeded")
	}
	if _, _, ok := doc.Text(1); ok {
		t.Fatal("Text(1) succeeded")
	}
}

func TestDocumentTextEmptyChapter(t *testing.T) {
	b := NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{{Kind: NodeBlock}}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	text, runs, ok := doc.Text(0)
	if !ok || text != "" {
		t.Fatalf("Text = %q, %v; want empty, true", text, ok)
	}
	if len(runs) != 1 || runs[0] != (TextRun{Node: 0}) {
		t.Fatalf("runs = %+v, want one zero-width root run", runs)
	}
}

func TestLayoutReverseAndMetrics(t *testing.T) {
	doc := testDocument(t, "")
	l := testLayout(t, doc, 2)
	if l.Document() != doc {
		t.Fatal("Layout.Document did not return the source document")
	}
	if got, want := l.Margins(), (paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24}); got != want {
		t.Fatalf("Margins = %+v, want %+v", got, want)
	}
	if got := l.PageContentHeight(); got != 552 {
		t.Fatalf("PageContentHeight = %v, want 552", got)
	}

	for page := 0; page < l.PageCount(); page++ {
		loc, ok := l.LocationOfPage(page)
		if !ok {
			t.Fatalf("LocationOfPage(%d) failed", page)
		}
		got, ok := l.PageOf(loc)
		if !ok || got != page {
			t.Fatalf("PageOf(LocationOfPage(%d)) = %d, %v; want %d, true", page, got, ok, page)
		}
	}
	if _, ok := l.LocationOfPage(-1); ok {
		t.Fatal("LocationOfPage(-1) succeeded")
	}
	if _, ok := l.LocationOfPage(l.PageCount()); ok {
		t.Fatal("LocationOfPage(PageCount) succeeded")
	}
}

func TestLocationOfPageScrollAndBlank(t *testing.T) {
	doc := testDocument(t, "")
	e := mustEngine(t, 2)
	scroll := layoutOptions(t, e, doc, scrollOptions(t, ModeScroll, 0))
	if _, ok := scroll.LocationOfPage(0); ok {
		t.Fatal("scroll mode LocationOfPage succeeded")
	}

	b := NewDocumentBuilder().SetStyles([]Style{{}, {FontFamily: "Test Serif", FontSize: 16}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{{Kind: NodeBlock}}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	empty, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	blank := testLayout(t, empty, 2)
	if blank.PageCount() != 1 {
		t.Fatalf("blank chapter pages = %d, want 1", blank.PageCount())
	}
	if _, ok := blank.LocationOfPage(0); ok {
		t.Fatal("blank page returned a location")
	}
}

func TestDocumentTextLargeChapter(t *testing.T) {
	// The string is sized in one pass so the builder does not repeatedly copy a
	// large chapter as it grows.
	text := strings.Repeat("段落文字", 1000)
	b := NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, runs, ok := doc.Text(0)
	if !ok || got != text || len(runs) != 2 {
		t.Fatalf("Text = %d bytes, %d runs, %v", len(got), len(runs), ok)
	}
}
