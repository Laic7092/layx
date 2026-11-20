package epub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// These benchmarks separate the two costs a scrolling reader pays:
//
//	Open/Build          this package: archive, OPF, nav, XHTML -> Document
//	Engine.Layout       render: shaping and line breaking in ModeScroll
//
// The layout half needs a CJK font; it is skipped when the system has none.

const benchViewportW, benchViewportH float32 = 620, 700

func synthEPUB(b testing.TB, chapters, paragraphs int) string {
	b.Helper()
	dir := b.TempDir()
	file := filepath.Join(dir, "synthetic.epub")
	entries := map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
	}
	var manifest, spine, nav strings.Builder
	para := "这是一段用于测量排版与解析性能的中文文字，包含标点符号，以及一些 English words 和数字 12345。"
	for i := 0; i < chapters; i++ {
		name := fmt.Sprintf("ch%04d.xhtml", i)
		fmt.Fprintf(&manifest, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, name)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		fmt.Fprintf(&nav, `<li><a href="%s">第 %d 章</a></li>`, name, i+1)
		var body strings.Builder
		fmt.Fprintf(&body, "<html><body><h1>第 %d 章</h1>", i+1)
		for p := 0; p < paragraphs; p++ {
			fmt.Fprintf(&body, "<p>%s</p>", para)
		}
		body.WriteString("</body></html>")
		entries["OEBPS/"+name] = []byte(body.String())
	}
	entries["OEBPS/nav.xhtml"] = []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol>` + nav.String() + `</ol></nav></body></html>`)
	entries["OEBPS/content.opf"] = []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>合成书</dc:title><dc:language>zh</dc:language></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`)
	writeEPUB(b, file, entries)
	return file
}

func benchCJKFont(b *testing.B) *render.FontSet {
	b.Helper()
	for _, p := range []string{
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		fs, err := render.NewFontSet(render.FontFace{Family: "book", Weight: 400, Data: data})
		if err == nil {
			return fs
		}
	}
	b.Skip("no CJK font; layout benchmark skipped")
	return nil
}

func benchLayout(b *testing.B, doc *render.Document, fonts *render.FontSet, mode render.Mode) {
	b.Helper()
	opts := render.LayoutOptions{
		Viewport: render.Viewport{Width: benchViewportW, Height: benchViewportH},
		Typography: render.Typography{
			FontFamily: "book", FontSize: 17, LineHeight: 1.18,
			TextAlign: render.AlignJustify,
			Color:     paint.Color{A: 0xff},
		},
		Fonts:   fonts,
		Margins: paint.Insets{Top: 8, Right: 27, Bottom: 38, Left: 27},
		Mode:    mode,
	}
	if mode == render.ModeScroll {
		opts.TileHeight = benchViewportH
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng, err := render.New(render.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := eng.Layout(context.Background(), doc, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAQ(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Open(sampleBook(b, "testdata/samples/aq-zhuan.epub")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildAQ(b *testing.B) {
	bk, err := Open(sampleBook(b, "testdata/samples/aq-zhuan.epub"))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := bk.Build(17); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenSynthetic(b *testing.B) {
	for _, chapters := range []int{100, 500} {
		b.Run(fmt.Sprintf("chapters=%d", chapters), func(b *testing.B) {
			file := synthEPUB(b, chapters, 20)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Open(file); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBuildSynthetic(b *testing.B) {
	for _, chapters := range []int{100, 500} {
		b.Run(fmt.Sprintf("chapters=%d", chapters), func(b *testing.B) {
			file := synthEPUB(b, chapters, 20)
			bk, err := Open(file)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := bk.Build(17); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLayoutAQ(b *testing.B) {
	fonts := benchCJKFont(b)
	bk, err := Open(sampleBook(b, "testdata/samples/aq-zhuan.epub"))
	if err != nil {
		b.Fatal(err)
	}
	doc, _, err := bk.Build(17)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("scroll", func(b *testing.B) { benchLayout(b, doc, fonts, render.ModeScroll) })
	b.Run("paged", func(b *testing.B) { benchLayout(b, doc, fonts, render.ModePaged) })
}

func BenchmarkLayoutSynthetic(b *testing.B) {
	fonts := benchCJKFont(b)
	file := synthEPUB(b, 200, 20)
	bk, err := Open(file)
	if err != nil {
		b.Fatal(err)
	}
	doc, _, err := bk.Build(17)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("scroll", func(b *testing.B) { benchLayout(b, doc, fonts, render.ModeScroll) })
	b.Run("paged", func(b *testing.B) { benchLayout(b, doc, fonts, render.ModePaged) })
}
