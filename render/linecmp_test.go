package render

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/laix/layx/render/paint"
)

// TestLineBoxes writes every line box of a corpus to a file, so scripts/linecmp.sh
// can diff them against a browser laying out the same text with the same font.
//
// It is a measurement fixture, not a regression test: it skips unless LPX_TEXT,
// LPX_OUT and LPX_FONT are set, and the comparison it feeds lives outside the
// suite. Line boxes are only reachable from inside the package, which is why
// the dump is a test rather than a tool under tools/.
//
// The dump format is one line per box:
//
//	index <tab> paragraph line <tab> width <tab> avail <tab> y <tab> height <tab> text
//
// scripts/linecmp.sh compares the text column with the browser's line boxes and
// reports the first divergence, so a mismatch points at a line-breaking rule
// rather than at pagination or painting.
func TestLineBoxes(t *testing.T) {
	textPath, outPath, fontPath := os.Getenv("LPX_TEXT"), os.Getenv("LPX_OUT"), os.Getenv("LPX_FONT")
	if textPath == "" || outPath == "" || fontPath == "" {
		t.Skip("set LPX_TEXT, LPX_OUT and LPX_FONT to dump line boxes")
	}
	text, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatal(err)
	}
	fontData, err := os.ReadFile(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := NewFontSet(FontFace{Family: "book", Weight: 400, Data: fontData})
	if err != nil {
		t.Fatal(err)
	}
	size, lineHeight, indent, gap := float32(18), float32(1.65), float32(36), float32(9)
	if v := os.Getenv("LPX_SIZE"); v != "" {
		fmt.Sscan(v, &size)
		lineHeight = size * 1.65 / 18
		indent = size * 2
		gap = size * 0.5
	}
	width, height := float32(640), float32(900)
	if v := os.Getenv("LPX_VIEWPORT"); v != "" {
		fmt.Sscanf(v, "%gx%g", &width, &height)
	}

	// One block per paragraph, styled like the A/B webview column: justified,
	// 2em first-line indent, half an em of space below.
	styles := []Style{
		{},
		{
			FontFamily: "book", FontSize: size, LineHeight: lineHeight,
			Color: paint.Color{A: 0xff}, TextAlign: AlignJustify,
			MarginBottom: gap, FirstLineIndent: indent,
		},
	}
	nodes := []Node{{Kind: NodeBlock, Style: 0}}
	var blocks []NodeID
	for _, p := range strings.Split(strings.TrimRight(string(text), "\n"), "\n") {
		nodes = append(nodes, Node{Kind: NodeText, Text: p})
		nodes = append(nodes, Node{Kind: NodeBlock, Style: 1, Children: []NodeID{NodeID(len(nodes) - 1)}})
		blocks = append(blocks, NodeID(len(nodes)-1))
	}
	nodes[0].Children = blocks

	b := NewDocumentBuilder().SetStyles(styles).SetMetadata(Metadata{Language: "zh-CN", Reflowable: true})
	if err := b.AddChapter(Chapter{ID: "cmp", Root: 0, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	lay, err := eng.Layout(context.Background(), doc, LayoutOptions{
		Viewport: Viewport{Width: width, Height: height},
		Typography: Typography{
			FontFamily: "book", FontSize: size, LineHeight: lineHeight,
			Color: paint.Color{A: 0xff}, Widows: 1, Orphans: 1,
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	n := 0
	for ci := range lay.chapters {
		for i := range lay.chapters[ci].items {
			it := &lay.chapters[ci].items[i]
			if it.kind != itemLine {
				continue
			}
			n++
			text := string(it.para.text[it.start:it.end])
			fmt.Fprintf(&sb, "%d\t%d\t%.2f\t%.2f\t%.2f\t%.2f\t%s\n",
				n, it.paraLine, it.width, it.avail, it.y, it.height, text)
		}
	}
	if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("line boxes: %d, pages: %d", n, lay.PageCount())
}
