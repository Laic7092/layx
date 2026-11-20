package render

import (
	"strings"
	"testing"

	"github.com/laic7092/layx/render/paint"
)

func anchoredDoc(t *testing.T, text string, anchors map[AnchorID]NodeID, nodeAnchors map[NodeID]AnchorID) *Document {
	t.Helper()
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: text},
	}
	for node, anchor := range nodeAnchors {
		nodes[node].Anchor = anchor
	}
	b := NewDocumentBuilder().
		SetStyles([]Style{{}, {FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{A: 0xff}}}).
		SetMetadata(Metadata{Language: "en", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: nodes, Anchors: anchors}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func TestNodeAnchorIsMerged(t *testing.T) {
	doc := anchoredDoc(t, strings.Repeat("anchor text ", 100), nil, map[NodeID]AnchorID{1: 42})
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("Chapter(0) failed")
	}
	if ch.Anchors[42] != 1 {
		t.Fatalf("Chapter.Anchors[42] = %d, want node 1", ch.Anchors[42])
	}
	l := testLayout(t, doc, 2)
	loc, ok := l.Locate(42)
	if !ok {
		t.Fatal("Locate(42) failed")
	}
	if loc.Node != 1 || loc.Anchor != 42 || loc.Offset != 0 {
		t.Fatalf("Locate(42) = %+v", loc)
	}
	if _, ok := l.PageOf(loc); !ok {
		t.Fatal("PageOf(anchored node) failed")
	}
}

func TestAnchorOffsetIsPreserved(t *testing.T) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 120)
	doc := anchoredDoc(t, text, nil, map[NodeID]AnchorID{1: 7})
	l := testLayout(t, doc, 2)

	mid := len([]rune(text)) / 2
	byAnchor := Location{Chapter: 0, Anchor: 7, Offset: mid}
	byNode := Location{Chapter: 0, Node: 1, Offset: mid}

	pa, okA := l.PageOf(byAnchor)
	pn, okN := l.PageOf(byNode)
	if !okA || !okN || pa != pn {
		t.Fatalf("PageOf anchor/node = %d/%d (%v/%v)", pa, pn, okA, okN)
	}
	if pa == 0 {
		t.Fatalf("mid offset landed on page 0; fixture is too short to prove offset preservation")
	}
	ya, okA := l.OffsetOf(byAnchor)
	yn, okN := l.OffsetOf(byNode)
	if !okA || !okN || ya != yn {
		t.Fatalf("OffsetOf anchor/node = %v/%v (%v/%v)", ya, yn, okA, okN)
	}

	// A zero Node must not win over the anchor: Location{Chapter, Anchor}
	// leaves Node at its zero value, which is the valid chapter root.
	bare := Location{Chapter: 0, Anchor: 7, Offset: mid}
	yb, ok := l.OffsetOf(bare)
	if !ok || yb != ya {
		t.Fatalf("bare anchor OffsetOf = %v (%v), want %v", yb, ok, ya)
	}
	if start, ok := l.OffsetOf(Location{Chapter: 0, Node: 1}); !ok {
		t.Fatal("OffsetOf(node 1) failed")
	} else if start != 0 {
		t.Fatalf("node 1 starts at %v, want 0", start)
	}
	if yb == 0 {
		t.Fatal("bare anchor resolved to the node start instead of the offset")
	}
}

func TestAnchorWinsOverStaleNode(t *testing.T) {
	nodes := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeBlock, Style: 1, Children: []NodeID{3}},
		{Kind: NodeBlock, Style: 1, Children: []NodeID{4}},
		{Kind: NodeText, Text: "first paragraph"},
		{Kind: NodeText, Text: "second paragraph"},
	}
	b := NewDocumentBuilder().
		SetStyles([]Style{{}, {FontFamily: "Test Serif", FontSize: 16, LineHeight: 1.5, Color: paint.Color{A: 0xff}, MarginBottom: 24}}).
		SetMetadata(Metadata{Language: "en", Reflowable: true})
	if err := b.AddChapter(Chapter{
		ID: "c", Root: 0, Nodes: nodes,
		Anchors: map[AnchorID]NodeID{100: 4},
	}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	l := testLayout(t, doc, 2)

	stale := Location{Chapter: 0, Node: 1, Anchor: 100}
	y, ok := l.OffsetOf(stale)
	if !ok {
		t.Fatal("OffsetOf(stale node + anchor) failed")
	}
	y4, ok := l.OffsetOf(Location{Chapter: 0, Node: 4})
	if !ok || y != y4 {
		t.Fatalf("anchor resolved to y=%v, want node 4 y=%v (%v)", y, y4, ok)
	}
	y1, ok := l.OffsetOf(Location{Chapter: 0, Node: 1})
	if !ok {
		t.Fatal("OffsetOf(node 1) failed")
	}
	if y == y1 {
		t.Fatal("stale Node won over the anchor")
	}

	loc, ok := l.Locate(100)
	if !ok || loc.Node != 4 {
		t.Fatalf("Locate(100) = %+v, %v; want node 4", loc, ok)
	}

	// PageOf resolves the anchor before checking Chapter, so an out-of-range
	// chapter with a valid anchor still works.
	if _, ok := l.PageOf(Location{Chapter: -1, Anchor: 100}); !ok {
		t.Fatal("PageOf(out-of-range chapter + valid anchor) failed")
	}
	if _, ok := l.PageOf(Location{Chapter: 0, Node: 1, Anchor: 999}); ok {
		t.Fatal("unknown anchor fell back to Node")
	}
}

func TestBuilderRejectsDuplicateNodeAnchors(t *testing.T) {
	duplicate := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeText, Text: "a", Anchor: 5},
		{Kind: NodeText, Text: "b", Anchor: 5},
	}
	b := NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: duplicate}); err == nil {
		t.Fatal("duplicate Node.Anchor was accepted")
	}

	conflict := []Node{
		{Kind: NodeBlock, Children: []NodeID{1, 2}},
		{Kind: NodeText, Text: "a", Anchor: 5},
		{Kind: NodeText, Text: "b"},
	}
	b = NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{
		ID: "c", Root: 0, Nodes: conflict, Anchors: map[AnchorID]NodeID{5: 2},
	}); err == nil {
		t.Fatal("conflicting Node.Anchor and Chapter.Anchors was accepted")
	}

	same := []Node{
		{Kind: NodeBlock, Children: []NodeID{1}},
		{Kind: NodeText, Text: "a", Anchor: 5},
	}
	b = NewDocumentBuilder().SetStyles([]Style{{}})
	if err := b.AddChapter(Chapter{
		ID: "c", Root: 0, Nodes: same, Anchors: map[AnchorID]NodeID{5: 1},
	}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ch, _ := doc.Chapter(0)
	if ch.Anchors[5] != 1 {
		t.Fatalf("Chapter.Anchors[5] = %d, want 1", ch.Anchors[5])
	}
}
