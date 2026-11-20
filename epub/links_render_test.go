package epub

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// The point of Node.Link is that render turns it into Page.Links with hit
// rectangles. This test drives the real engine once (when its test font is
// present) so the hand-off is not just asserted on paper.
func TestLinksReachPageLinks(t *testing.T) {
	font, err := os.ReadFile("../render/testdata/DejaVuSerif.ttf")
	if err != nil {
		t.Skipf("render test font unavailable: %v", err)
	}
	file := filepath.Join(t.TempDir(), "links.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>links</dc:title><dc:language>en</dc:language></metadata><manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`),
		"OEBPS/ch1.xhtml":        []byte(`<html><body><h2 id="s1">A heading</h2><p>See <a href="#s1">the heading</a> and <a href="https://example.com/x">the web</a>.</p></body></html>`),
	})

	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := render.NewFontSet(render.FontFace{Family: "book", Weight: 400, Data: font})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: 500, Height: 600},
		Typography: render.Typography{
			FontFamily: "book", FontSize: 18, LineHeight: 1.5,
			Color: paint.Color{A: 0xff},
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page")
	}
	urls := map[string]bool{}
	for _, l := range page.Links() {
		urls[l.URL] = true
	}
	for _, want := range []string{"OEBPS/ch1.xhtml#s1", "https://example.com/x"} {
		if !urls[want] {
			t.Errorf("Page.Links lacks %q; got %v", want, urls)
		}
	}
}
