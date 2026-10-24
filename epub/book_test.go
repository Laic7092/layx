package epub

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func TestOpenAndDocument(t *testing.T) {
	b, err := Open("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "最小的书" {
		t.Errorf("title %q", b.Title)
	}
	if b.Language != "zh-CN" {
		t.Errorf("language %q", b.Language)
	}
	if len(b.Chapters) != 2 {
		t.Fatalf("chapters %d, want 2", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "第一章 初见" {
		t.Errorf("chapter 1 title %q (nav titles should win)", got)
	}
	if got := b.Chapters[1].Title; got != "第二章 再见" {
		t.Errorf("chapter 2 title %q", got)
	}
	if !strings.Contains(string(b.Chapters[0].XHTML), "Hello") {
		t.Errorf("chapter 1 lost its XHTML")
	}
	if len(b.Toc) != 2 {
		t.Fatalf("toc entries %d, want 2", len(b.Toc))
	}
	if e := b.Toc[0]; e.Title != "第一章 初见" || e.Chapter != 0 || e.Depth != 0 || e.Anchor != 1 {
		t.Errorf("toc[0] = %+v", e)
	}
	if e := b.Toc[1]; e.Chapter != 1 || e.Anchor != 2 {
		t.Errorf("toc[1] = %+v", e)
	}

	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChapterCount() != 2 {
		t.Fatalf("document chapters %d, want 2", doc.ChapterCount())
	}
	if doc.Metadata().Title != b.Title || !doc.Metadata().Reflowable {
		t.Errorf("metadata %+v", doc.Metadata())
	}

	first, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter 0 missing")
	}
	text := chapterText(first)
	for _, want := range []string{
		"第一章 初见",
		"Hello reader, this is chapter one.",
		"第二段，包含中文文本。",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chapter 1 text %q lacks %q", text, want)
		}
	}

	second, ok := doc.Chapter(1)
	if !ok {
		t.Fatal("chapter 1 missing")
	}
	text = chapterText(second)
	if !strings.Contains(text, "The second chapter has") || !strings.Contains(text, "bold words") {
		t.Errorf("chapter 2 text %q", text)
	}
}

func TestDocumentSizedScalesHeadings(t *testing.T) {
	b, err := Open("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	small, err := b.DocumentSized(16)
	if err != nil {
		t.Fatal(err)
	}
	large, err := b.DocumentSized(24)
	if err != nil {
		t.Fatal(err)
	}
	h1Small := headingSize(t, small)
	h1Large := headingSize(t, large)
	if h1Large <= h1Small {
		t.Errorf("h1 size did not scale: %g then %g", h1Small, h1Large)
	}
	if want := float32(16 * 1.6); h1Small != want {
		t.Errorf("h1 small %g, want %g", h1Small, want)
	}
}

// writeEPUB writes a minimal EPUB archive whose entries are the given
// archive paths. container.xml and the OPF are up to the caller.
func writeEPUB(t testing.TB, file string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeImageEPUB writes a book whose first spine item is a cover page made of
// one SVG-wrapped raster image, followed by one text chapter with an inline
// image.
func writeImageEPUB(t *testing.T, file string) {
	t.Helper()
	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 0xc0, G: 0x20, B: 0x20, A: 0xff})
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>有图的书</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="pic" href="pic.png" media-type="image/png" properties="cover-image"/></manifest><spine><itemref idref="cover"/><itemref idref="ch1"/></spine></package>`),
		"OEBPS/cover.xhtml":      []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><div><svg viewBox="0 0 2 3"><image width="2" height="3" xlink:href="pic.png"/></svg></div></body></html>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h1>正文</h1><p>Hello image.</p><img src="pic.png" width="2" height="3"/></body></html>`),
		"OEBPS/pic.png":          pic.Bytes(),
	}
	writeEPUB(t, file, entries)
}

// The nav document is the authority on chapter titles. mini.epub cannot prove
// it: its headings repeat the nav text, so a converter that never reads the
// nav still passes. This book gives the two different titles.
func TestNavTitlesWinOverHeadings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nav.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>目录测试</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`),
		"OEBPS/nav.xhtml":        []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">目录里的标题</a></li></ol></nav></body></html>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h1>正文里的标题</h1><p>正文。</p></body></html>`),
	})

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "目录里的标题" {
		t.Errorf("chapter title %q, want the nav title", got)
	}
}

// A chapter with no heading still gets its title from the NCX named by the
// spine's toc attribute, which is how EPUB 2 books label themselves.
func TestNCXTitlesFromSpineToc(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ncx.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>NCX 测试</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine toc="ncx"><itemref idref="ch1"/></spine></package>`),
		"OEBPS/toc.ncx":          []byte(`<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap><navPoint><navLabel><text>NCX 里的标题</text></navLabel><content src="ch1.xhtml"/></navPoint></navMap></ncx>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><p>这一章没有标题元素。</p></body></html>`),
	})

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "NCX 里的标题" {
		t.Errorf("chapter title %q, want the NCX title", got)
	}
}

// Navigation targets inside one spine item must stay separate rows with their
// own anchors. This is the shape that real books (and aq-zhuan) use: one XHTML
// file, many fragments.
func TestTOCFragmentsBecomeAnchors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sections.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>分节测试</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`),
		"OEBPS/nav.xhtml":        []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">第一章</a><ol><li><a href="ch1.xhtml#s1">第一节</a></li><li><a href="ch1.xhtml#missing">找不到的锚点</a></li></ol></li><li><span>第二卷</span><ol><li><a href="ch1.xhtml#s2">第二节</a></li></ol></li></ol></nav></body></html>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h1>第一章</h1><h2 id="s1">第一节</h2><p id="s2">第二节</p></body></html>`),
	})

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Toc) != 5 {
		t.Fatalf("toc entries %d, want 5: %+v", len(b.Toc), b.Toc)
	}
	want := []struct {
		title   string
		depth   int
		chapter int
	}{
		{"第一章", 0, 0},
		{"第一节", 1, 0},
		{"找不到的锚点", 1, 0},
		{"第二卷", 0, -1},
		{"第二节", 1, 0},
	}
	for i, w := range want {
		got := b.Toc[i]
		if got.Title != w.title || got.Depth != w.depth || got.Chapter != w.chapter {
			t.Errorf("toc[%d] = %q depth %d chapter %d, want %q depth %d chapter %d",
				i, got.Title, got.Depth, got.Chapter, w.title, w.depth, w.chapter)
		}
	}
	if b.Toc[0].Anchor != 1 {
		t.Errorf("chapter entry anchor %d, want the chapter root anchor 1", b.Toc[0].Anchor)
	}
	if b.Toc[1].Anchor == b.Toc[0].Anchor || b.Toc[4].Anchor == b.Toc[0].Anchor {
		t.Error("section entries must have anchors distinct from the chapter root")
	}
	if b.Toc[3].Anchor != 0 {
		t.Errorf("an entry without a target has anchor %d, want 0", b.Toc[3].Anchor)
	}

	doc, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter 0 missing")
	}
	// Every resolvable row must point at a real node: s1 and s2 at their own
	// nodes, the missing fragment back at the chapter root.
	root := ch.Anchors[1]
	if ch.Anchors[b.Toc[1].Anchor] == root {
		t.Error("#s1 resolved to the chapter root instead of its heading")
	}
	if ch.Anchors[b.Toc[4].Anchor] == root {
		t.Error("#s2 resolved to the chapter root instead of its paragraph")
	}
	if ch.Anchors[b.Toc[2].Anchor] != root {
		t.Error("a missing fragment should fall back to the chapter root")
	}
}

// sampleBook returns a fixture path, skipping when the file is not in this
// checkout. Real-book samples are not committed; see testdata/samples/README.md.
func sampleBook(t testing.TB, name string) string {
	t.Helper()
	if _, err := os.Stat(name); err != nil {
		t.Skipf("%s is not in this checkout; see testdata/samples/README.md", name)
	}
	return name
}

// The real Gutenberg sample has seven NCX navPoints, six of them pointing at
// fragments of one XHTML file; the old map-based parser collapsed all of them
// into two chapter titles.
func TestTOCOnRealSample(t *testing.T) {
	b, err := Open(sampleBook(t, "testdata/samples/aq-zhuan.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Toc) != 7 {
		t.Fatalf("toc entries %d, want 7", len(b.Toc))
	}
	fragments := 0
	for i, e := range b.Toc {
		if e.Chapter < 0 {
			t.Errorf("toc[%d] %q has no chapter", i, e.Title)
		}
		if e.Anchor != render.AnchorID(e.Chapter+1) {
			fragments++
		}
	}
	if fragments < 5 {
		t.Errorf("only %d fragment anchors; sections collapsed into chapters", fragments)
	}

	doc, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	nonRoot := 0
	for i, e := range b.Toc {
		ch, ok := doc.Chapter(e.Chapter)
		if !ok {
			t.Fatalf("toc[%d] points at missing chapter %d", i, e.Chapter)
		}
		node, ok := ch.Anchors[e.Anchor]
		if !ok {
			t.Errorf("toc[%d] %q anchor %d is not in chapter %d", i, e.Title, e.Anchor, e.Chapter)
			continue
		}
		if node != ch.Anchors[render.AnchorID(e.Chapter+1)] {
			nonRoot++
		}
	}
	// Every aq-zhuan target names an existing id, so none may fall back to the
	// chapter root; that would silently make the rows indistinguishable again.
	if nonRoot != len(b.Toc) {
		t.Errorf("%d of %d entries fell back to the chapter root", len(b.Toc)-nonRoot, len(b.Toc))
	}
}

// Nested navPoints keep their hierarchy: the section is a child of the chapter,
// not a sibling guessed from its title text.
func TestParseNCXNestedTitles(t *testing.T) {
	ncx := []byte(`<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
<navPoint><navLabel><text>第一章</text></navLabel><content src="ch1.xhtml"/>
  <navPoint><navLabel><text>第一节</text></navLabel><content src="ch1.xhtml#s1"/></navPoint>
</navPoint></navMap></ncx>`)
	nodes := parseNCX(ncx, "OEBPS/toc.ncx")
	if len(nodes) != 1 {
		t.Fatalf("top-level entries %d, want 1", len(nodes))
	}
	if nodes[0].Title != "第一章" || nodes[0].Path != "OEBPS/ch1.xhtml" || nodes[0].Frag != "" {
		t.Errorf("chapter node %+v", nodes[0])
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("children %d, want 1", len(nodes[0].Children))
	}
	sub := nodes[0].Children[0]
	if sub.Title != "第一节" || sub.Path != "OEBPS/ch1.xhtml" || sub.Frag != "s1" {
		t.Errorf("section node %+v (fragment must be kept)", sub)
	}
}

// <a href> must survive as render.Node.Link: external URLs verbatim, in-book
// targets resolved to an archive path + fragment that ResolveLink can map.
func TestLinksPreservedAndResolvable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "links.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>链接测试</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/><itemref idref="ch2"/></spine></package>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h2 id="s1">本页标题</h2><p>看 <a href="ch2.xhtml#s2">下一章第二节</a>，回到 <a href="#s1">本节</a>，或者 <a href="https://example.com/x">外部</a>。</p><p><a href="missing.xhtml">断链</a></p></body></html>`),
		"OEBPS/ch2.xhtml":        []byte(`<html><body><h2 id="s2">目标标题</h2><p>目标正文。</p></body></html>`),
	})

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}

	links := map[string]bool{}
	for ci := 0; ci < doc.ChapterCount(); ci++ {
		ch, _ := doc.Chapter(ci)
		for _, n := range ch.Nodes {
			if n.Link != "" {
				links[n.Link] = true
			}
		}
	}
	for _, want := range []string{
		"OEBPS/ch2.xhtml#s2",
		"OEBPS/ch1.xhtml#s1",
		"https://example.com/x",
		"OEBPS/missing.xhtml",
	} {
		if !links[want] {
			t.Errorf("Node.Link %q missing; got %v", want, links)
		}
	}

	target, ok := b.ResolveLink("OEBPS/ch2.xhtml#s2")
	if !ok || target.Chapter != 1 {
		t.Fatalf("ResolveLink(cross chapter) = %+v, %v", target, ok)
	}
	ch2, _ := doc.Chapter(1)
	if node, ok := ch2.Anchors[target.Anchor]; !ok || node == ch2.Anchors[2] {
		t.Errorf("cross-chapter link did not resolve to #s2: node %d, ok %v", node, ok)
	}

	target, ok = b.ResolveLink("OEBPS/ch1.xhtml#s1")
	if !ok || target.Chapter != 0 {
		t.Fatalf("ResolveLink(fragment in same chapter) = %+v, %v", target, ok)
	}
	ch1, _ := doc.Chapter(0)
	if node, ok := ch1.Anchors[target.Anchor]; !ok || node == ch1.Anchors[1] {
		t.Errorf("fragment link did not resolve to #s1: node %d, ok %v", node, ok)
	}

	if _, ok := b.ResolveLink("https://example.com/x"); ok {
		t.Error("an external URL must not resolve inside the book")
	}
	if _, ok := b.ResolveLink("OEBPS/missing.xhtml"); ok {
		t.Error("a link to a file outside the spine must not resolve")
	}
}

func TestCoverAndInlineImages(t *testing.T) {
	file := filepath.Join(t.TempDir(), "images.epub")
	writeImageEPUB(t, file)

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasCover {
		t.Fatal("cover image not detected")
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1 (the cover page must leave the reading flow)", len(b.Chapters))
	}
	if got := b.Chapters[0].Title; got != "正文" {
		t.Errorf("chapter title %q", got)
	}

	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter 0 missing")
	}
	var found []render.Node
	for _, n := range ch.Nodes {
		if n.Kind == render.NodeImage {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("image nodes %d, want 1", len(found))
	}
	if found[0].Width != 2 || found[0].Height != 3 {
		t.Errorf("image size %gx%g, want 2x3", found[0].Width, found[0].Height)
	}
	if _, ok := images[found[0].Image]; !ok {
		t.Error("inline image bytes missing")
	}
	if _, ok := images[b.Cover]; !ok {
		t.Error("cover bytes missing")
	}
	if !strings.Contains(chapterText(ch), "Hello image.") {
		t.Errorf("chapter text %q", chapterText(ch))
	}
}

func TestTableLinearizedIntoRows(t *testing.T) {
	xhtml := []byte(`<html><body>
<table>
  <caption>表 1：水果</caption>
  <colgroup><col width="50%"/></colgroup>
  <thead><tr><th>名称</th><th>数量</th></tr></thead>
  <tbody>
    <tr><td>苹果</td><td>3</td></tr>
    <tr><td><p>香蕉</p></td><td>5</td></tr>
    <tr><td colspan="2">合并单元格</td></tr>
  </tbody>
</table>
<p>表后正文。</p>
</body></html>`)
	style := newCascade(18, Options{}, nil)
	styles := newStyleRegistry()
	ch, err := convertChapter(xhtml, "t", 1, nil, nil, "OEBPS/ch1.xhtml", style, styles)
	if err != nil {
		t.Fatal(err)
	}
	// The builder is the validator Build uses; a row holding a nested
	// paragraph must still be a legal chapter tree.
	builder := render.NewDocumentBuilder().SetStyles(styles.list())
	if err := builder.AddChapter(ch); err != nil {
		t.Fatalf("table chapter is not a valid document: %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("table document is invalid: %v", err)
	}

	text := chapterText(ch)
	for _, want := range []string{
		"表 1：水果",
		"名称 ｜ 数量",
		"苹果 ｜ 3",
		"香蕉 ｜ 5",
		"合并单元格",
		"表后正文。",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("table text %q lacks %q", text, want)
		}
	}

	rows, captions, headerCells := 0, 0, 0
	for _, n := range ch.Nodes {
		if int(n.Style) >= len(styles.styles) {
			t.Fatalf("node style %d is out of range", n.Style)
		}
		st := styles.styles[n.Style]
		switch {
		case n.Kind == render.NodeBlock && st.KeepTogether && st.MarginBottom > 0:
			rows++
		case n.Kind == render.NodeBlock && st.FontSlant == render.SlantItalic:
			captions++
		case n.Kind == render.NodeInline && st.FontWeight == 700:
			headerCells++
		}
	}
	if rows != 4 {
		t.Errorf("table rows %d, want 4 (header + 3 body rows)", rows)
	}
	if captions != 1 {
		t.Errorf("captions %d, want 1", captions)
	}
	if headerCells != 2 {
		t.Errorf("bold header cells %d, want 2", headerCells)
	}
}

func TestEmptyChapterGetsAPlaceholder(t *testing.T) {
	ch, err := convertChapter([]byte(`<html><body><div></div></body></html>`), "x", 1, nil, nil, "OEBPS/ch1.xhtml", newCascade(18, Options{}, nil), newStyleRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if got := chapterText(ch); !strings.Contains(got, "没有可显示的文本") {
		t.Errorf("placeholder missing, chapter text %q", got)
	}
}

func chapterText(ch render.Chapter) string {
	var b strings.Builder
	var walk func(render.NodeID)
	walk = func(id render.NodeID) {
		if id < 0 || int(id) >= len(ch.Nodes) {
			return
		}
		n := ch.Nodes[id]
		if n.Kind == render.NodeText {
			b.WriteString(n.Text)
		}
		if n.Kind == render.NodeBreak {
			b.WriteByte('\n')
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(ch.Root)
	return b.String()
}

func headingSize(t *testing.T, doc *render.Document) float32 {
	t.Helper()
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("no chapter")
	}
	styles := doc.Styles()
	for _, n := range ch.Nodes {
		if n.Kind == render.NodeBlock && int(n.Style) < len(styles) && styles[n.Style].FontWeight == 700 {
			return styles[n.Style].FontSize
		}
	}
	t.Fatal("no heading block")
	return 0
}

// A link is not just a URL: its text is painted in the link colour and carries
// an underline, over the slant and weight of whatever wraps it.
func TestLinkStyle(t *testing.T) {
	custom := paint.Color{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
	for _, c := range []struct {
		name  string
		opts  Options
		color paint.Color
	}{
		{"default", Options{}, DefaultLinkColor},
		{"host colour", Options{LinkColor: custom}, custom},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "link.epub")
			writeEPUB(t, file, map[string][]byte{
				"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
				"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>链接</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`),
				"OEBPS/ch1.xhtml":        []byte(`<html><body><p>正文 <a href="https://example.com">链接</a> 正文</p></body></html>`),
			})
			b, err := Open(file)
			if err != nil {
				t.Fatal(err)
			}
			doc, _, err := b.BuildWith(18, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			ch, ok := doc.Chapter(0)
			if !ok {
				t.Fatal("no chapter")
			}
			styles := doc.Styles()
			// The URL lands on the first leaf (render builds hit rectangles
			// from text tokens); the colour and underline live on the inline
			// <a> node that wraps it.
			linkLeaf := render.InvalidNode
			for i, n := range ch.Nodes {
				if n.Link != "" {
					linkLeaf = render.NodeID(i)
					break
				}
			}
			if linkLeaf == render.InvalidNode {
				t.Fatal("no node carries the link")
			}
			var styled bool
			for _, n := range ch.Nodes {
				for _, child := range n.Children {
					if child != linkLeaf {
						continue
					}
					if int(n.Style) >= len(styles) {
						t.Fatalf("node style %d is out of range", n.Style)
					}
					st := styles[n.Style]
					if st.Color != c.color {
						t.Errorf("link run colour = %+v, want %+v", st.Color, c.color)
					}
					if st.TextDecoration != render.TextDecorationUnderline {
						t.Errorf("link decoration = %v, want underline", st.TextDecoration)
					}
					styled = true
				}
			}
			if !styled {
				t.Error("link text has no styled ancestor")
			}
		})
	}
}
