package epub

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

func TestParseCSSShapes(t *testing.T) {
	sheet := parseCSS(`
@charset "utf-8";
@import "base.css";
/* a comment with { braces } and ; semicolons */
@media screen, print {
	p { color: red; margin: 1em 0 }
}
@page { margin: 1cm }
<!-- .old { color: green } -->
a[href], .x > p:first-child { text-decoration: none !important }
a:hover, a::after { color: blue }
`)
	var imports int
	var rules []cssRule
	for _, part := range sheet.parts {
		if part.importHref != "" {
			imports++
			if part.importHref != "base.css" {
				t.Errorf("import href = %q", part.importHref)
			}
			continue
		}
		rules = append(rules, part.rule)
	}
	if imports != 1 {
		t.Fatalf("imports = %d, want 1", imports)
	}
	// @media is flattened, the HTML comment wrapper is stripped but its rule
	// still applies, and @page plus the :hover/::after rule are dropped.
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3", len(rules))
	}
	if rules[1].selectors[0].parts[0].classes[0] != "old" {
		t.Errorf("comment wrapper rule = %+v", rules[1].selectors)
	}
	media := rules[0]
	if len(media.selectors) != 1 || media.selectors[0].parts[0].tag != "p" {
		t.Errorf("media selectors = %+v", media.selectors)
	}
	if len(media.decls) != 2 || media.decls[1].prop != "margin" {
		t.Errorf("media declarations = %+v", media.decls)
	}
	link := rules[2]
	if len(link.selectors) != 2 {
		t.Fatalf("link selectors = %+v", link.selectors)
	}
	if !link.decls[0].important || link.decls[0].prop != "text-decoration" {
		t.Errorf("important was not parsed: %+v", link.decls)
	}
	if len(link.selectors[1].parts) != 2 || link.selectors[1].combs[0] != combChild {
		t.Errorf("child combinator = %+v", link.selectors[1])
	}
}

func TestStylesheetLinkStyleBlockAndInline(t *testing.T) {
	doc, ch := buildSingleChapter(t, `<!doctype html>
<html><head>
<link rel="stylesheet" href="css/book.css"/>
<link rel="icon" href="css/ignored.css"/>
<style>p.note { text-indent: 0 }</style>
</head><body>
<p class="lead">lead</p>
<p class="note">note</p>
<p class="adj">adjacent</p>
<p id="intro">intro</p>
<div class="box"><p>inner</p></div>
<p style="margin-top: 2em; text-align: right">inline</p>
<p class="hide">hidden</p>
<p class="caps">caps</p>
<p><a href="https://example.com">link</a></p>
</body></html>`, map[string][]byte{
		"OEBPS/css/book.css": []byte(`
@import "more.css";
body { margin-left: 8%; font-size: 20px; color: #123456 }
p { text-indent: 1.5em; margin-bottom: 0.5em }
.lead { font-size: 120%; font-weight: bold; line-height: 1.4em; text-align: center }
.note { text-indent: 0 }
#intro { margin-left: 4em; text-indent: -0.6em }
div.box > p { margin-top: 2em }
p + p.adj { text-align: justify }
.hide { display: none }
a { color: green; text-decoration: none }
`),
		"OEBPS/css/more.css": []byte(`.caps { letter-spacing: 0.1em; word-spacing: 0.2em; color: red }`),
	}, Options{ContainerWidth: 500})

	styles := doc.Styles()
	bodyID := ch.Nodes[ch.Root].Children[0]
	body := styles[ch.Nodes[bodyID].Style]
	if body.MarginLeft != 40 {
		t.Errorf("body margin-left = %v, want 40 (8%% of 500)", body.MarginLeft)
	}
	if body.FontSize != 20 || body.Color != (paint.Color{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Errorf("body style = %+v", body)
	}

	lead := blockStyle(t, ch, styles, "lead")
	if lead.FontSize != 24 {
		t.Errorf("lead font size = %v, want 24 (120%%)", lead.FontSize)
	}
	if lead.FontWeight != 700 || lead.LineHeight != 1.4 || lead.TextAlign != render.AlignCenter {
		t.Errorf("lead style = %+v", lead)
	}
	if lead.FirstLineIndent != 36 {
		t.Errorf("lead indent = %v, want 36 (1.5em at its own 24px)", lead.FirstLineIndent)
	}

	if got := blockStyle(t, ch, styles, "note").FirstLineIndent; got != 0 {
		t.Errorf("<style> block did not override indent: %v", got)
	}
	intro := blockStyle(t, ch, styles, "intro")
	if intro.MarginLeft != 80 {
		t.Errorf("intro margin-left = %v, want 80 (4em at 20px)", intro.MarginLeft)
	}
	if intro.FirstLineIndent != -12 {
		t.Errorf("intro indent = %v, want -12 (-0.6em)", intro.FirstLineIndent)
	}
	if got := blockStyle(t, ch, styles, "inner").MarginTop; got != 40 {
		t.Errorf("child selector margin-top = %v, want 40 (2em)", got)
	}
	if got := blockStyle(t, ch, styles, "adjacent").TextAlign; got != render.AlignJustify {
		t.Errorf("adjacent sibling selector align = %v, want justify", got)
	}
	inline := blockStyle(t, ch, styles, "inline")
	if inline.MarginTop != 40 || inline.TextAlign != render.AlignRight {
		t.Errorf("inline style = %+v", inline)
	}
	if strings.Contains(chapterText(ch), "hidden") {
		t.Error("display:none subtree was emitted")
	}
	caps := blockStyle(t, ch, styles, "caps")
	if caps.Color != (paint.Color{R: 0xff, G: 0, B: 0, A: 0xff}) {
		t.Errorf("imported stylesheet color = %+v", caps.Color)
	}
	if caps.LetterSpacing != 2 || caps.WordSpacing != 4 {
		t.Errorf("spacing = %v/%v, want 2/4", caps.LetterSpacing, caps.WordSpacing)
	}

	ancestors := textAncestors(t, ch, styles, "link")
	if len(ancestors) == 0 {
		t.Fatal("link text has no ancestor")
	}
	link := ancestors[0]
	if link.Color != (paint.Color{R: 0, G: 0x80, B: 0, A: 0xff}) {
		t.Errorf("link colour = %+v, want author green", link.Color)
	}
	if link.TextDecoration != render.TextDecorationNone {
		t.Errorf("link decoration = %v, want none", link.TextDecoration)
	}
}

func TestPreformattedWhitespace(t *testing.T) {
	doc, ch := buildSingleChapter(t, `<html><body><pre>
line one
  indented

last	tab</pre><p>after</p></body></html>`, nil, Options{})
	_ = doc
	text := chapterText(ch)
	if !strings.Contains(text, "line one\n  indented\n\nlast    tab") {
		t.Errorf("pre text = %q", text)
	}
	if strings.HasPrefix(text, "\n") {
		t.Errorf("pre kept the newline HTML ignores after <pre>: %q", text)
	}
}

func TestListMarkers(t *testing.T) {
	doc, ch := buildSingleChapter(t, `<html><body>
<ol><li>一</li><li>二<ol><li>sub</li></ol></li><li>三</li></ol>
<ul><li>bullet</li><li>also</li></ul>
<ul style="list-style-type: none"><li>plain</li></ul>
</body></html>`, nil, Options{})
	text := chapterText(ch)
	for _, want := range []string{"1. 一", "2. 二", "1. sub", "3. 三", "• bullet", "• also", "plain"} {
		if !strings.Contains(text, want) {
			t.Errorf("list text %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "• plain") {
		t.Errorf("list-style-type:none still drew a marker: %q", text)
	}
	if doc.StyleCount() == 0 {
		t.Error("no styles")
	}
}

func TestHorizontalRuleAndDisplayRole(t *testing.T) {
	doc, ch := buildSingleChapter(t, `<html><head><link rel="stylesheet" href="css/x.css"/></head>
<body><hr/><p><span class="cap">caption</span></p></body></html>`,
		map[string][]byte{"OEBPS/css/x.css": []byte(`.cap { display: block }`)}, Options{})
	styles := doc.Styles()
	var rule, cap bool
	for _, n := range ch.Nodes {
		st := styles[n.Style]
		if n.Kind == render.NodeBlock && st.BorderBottom.Width > 0 && !st.BorderBottom.Color.Transparent() {
			rule = true
		}
		if n.Kind != render.NodeBlock {
			continue
		}
		for _, child := range n.Children {
			if ch.Nodes[child].Kind == render.NodeText && strings.Contains(ch.Nodes[child].Text, "caption") {
				cap = true
			}
		}
	}
	if !rule {
		t.Error("<hr> produced no visible rule")
	}
	if !cap {
		t.Error("display:block span did not become a block")
	}
}

func TestIgnoreColor(t *testing.T) {
	xhtml := `<html><body><link rel="stylesheet" href="css/x.css"/><p>text</p></body></html>`
	files := map[string][]byte{"OEBPS/css/x.css": []byte(`body { color: black } p { color: black }`)}
	doc, ch := buildSingleChapter(t, xhtml, files, Options{})
	if got := blockStyle(t, ch, doc.Styles(), "text").Color; got != (paint.Color{R: 0, G: 0, B: 0, A: 0xff}) {
		t.Errorf("author colour = %+v, want black", got)
	}
	doc, ch = buildSingleChapter(t, xhtml, files, Options{IgnoreColor: true})
	if got := blockStyle(t, ch, doc.Styles(), "text").Color; !got.Transparent() {
		t.Errorf("IgnoreColor left author ink: %+v", got)
	}
}

func TestPercentLengthsWithoutContainerWidth(t *testing.T) {
	doc, ch := buildSingleChapter(t, `<html><head><link rel="stylesheet" href="css/x.css"/></head>
<body><p>text</p></body></html>`, map[string][]byte{
		"OEBPS/css/x.css": []byte(`p { margin-left: 8%; text-indent: 5% }`),
	}, Options{})
	st := blockStyle(t, ch, doc.Styles(), "text")
	if st.MarginLeft != 0 || st.FirstLineIndent != 0 {
		t.Errorf("percentages without a width = %v/%v, want 0/0", st.MarginLeft, st.FirstLineIndent)
	}
}

// buildSingleChapter writes a one-chapter book and builds it.
func buildSingleChapter(t *testing.T, xhtml string, extras map[string][]byte, o Options) (*render.Document, render.Chapter) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "css.epub")
	files := map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>css</dc:title><dc:language>en</dc:language></metadata><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`),
		"OEBPS/ch1.xhtml":        []byte(xhtml),
	}
	for name, data := range extras {
		files[name] = data
	}
	writeEPUB(t, file, files)
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := b.BuildWith(18, o)
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("no chapter")
	}
	return doc, ch
}

// textAncestors returns the styles of the text node's ancestors, nearest
// first, for assertions about inline wrappers.
func textAncestors(t *testing.T, ch render.Chapter, styles []render.Style, want string) []render.Style {
	t.Helper()
	parent := make(map[render.NodeID]render.NodeID, len(ch.Nodes))
	var walk func(render.NodeID)
	walk = func(id render.NodeID) {
		for _, child := range ch.Nodes[id].Children {
			parent[child] = id
			walk(child)
		}
	}
	walk(ch.Root)
	for i := range ch.Nodes {
		n := ch.Nodes[i]
		if n.Kind != render.NodeText || !strings.Contains(n.Text, want) {
			continue
		}
		var out []render.Style
		id := render.NodeID(i)
		for {
			p, ok := parent[id]
			if !ok {
				return out
			}
			out = append(out, styles[ch.Nodes[p].Style])
			id = p
		}
	}
	t.Fatalf("no text %q", want)
	return nil
}

// blockStyle returns the nearest block ancestor's style for a text node.
func blockStyle(t *testing.T, ch render.Chapter, styles []render.Style, want string) render.Style {
	t.Helper()
	parent := make(map[render.NodeID]render.NodeID, len(ch.Nodes))
	var walk func(render.NodeID)
	walk = func(id render.NodeID) {
		for _, child := range ch.Nodes[id].Children {
			parent[child] = id
			walk(child)
		}
	}
	walk(ch.Root)
	for i := range ch.Nodes {
		n := ch.Nodes[i]
		if n.Kind != render.NodeText || !strings.Contains(n.Text, want) {
			continue
		}
		id := render.NodeID(i)
		for {
			p, ok := parent[id]
			if !ok {
				break
			}
			if ch.Nodes[p].Kind == render.NodeBlock {
				return styles[ch.Nodes[p].Style]
			}
			id = p
		}
	}
	t.Fatalf("no block contains text %q", want)
	return render.Style{}
}
