// Command genepub writes the synthetic EPUB samples the epub module's sample
// tests use, so the samples can be regenerated from this repository instead of
// being copied from somewhere else.
//
// From the repository root:
//
//	go run ./tools/genepub
//
// From the epub module directory:
//
//	cd epub && go run ../tools/genepub -out testdata/samples
//
// The output is deterministic: the zip entry order, timestamps and contents are
// fixed, and the PNG encoder is deterministic for a given Go release, so
// regenerating an unchanged generator produces byte-identical files. The
// generated *.epub files are gitignored build products, not sources; the tests
// skip when they are absent.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"time"
)

// defaultOut is where the epub module expects the samples when the command runs
// from the repository root.
const defaultOut = "epub/testdata/samples"

// fixedTime stamps every zip entry. A real modification time would make the
// generated file differ on every run for no reason.
var fixedTime = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

func main() {
	out := flag.String("out", defaultOut, "directory to write the sample EPUB files to")
	flag.Parse()

	dir := *out
	if *out == defaultOut {
		// When run from inside the epub module the default (repository-root
		// relative) path does not exist; use the module-local sample directory
		// instead of creating a nested epub/testdata/samples.
		if _, err := os.Stat(filepath.FromSlash(defaultOut)); err != nil {
			if fi, err := os.Stat(filepath.FromSlash("testdata/samples")); err == nil && fi.IsDir() {
				dir = "testdata/samples"
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("genepub: create %s: %v", dir, err)
	}

	samples := []sample{
		// The rich sample exercises everything the converter can drop: a table
		// with spans, an image, ruby, a vector-only SVG, MathML and @font-face.
		{name: "layx-synthetic.epub", files: syntheticBook()},
		// The clean sample has nothing to report: it is the control case for
		// hosts that want to check their diagnostics plumbing stays quiet.
		{name: "layx-clean.epub", files: cleanBook()},
	}
	for _, s := range samples {
		path := filepath.Join(dir, s.name)
		if err := writeEPUB(path, s.files); err != nil {
			log.Fatalf("genepub: %v", err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			log.Fatalf("genepub: %v", err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, fi.Size())
	}
	fmt.Println("verify with: cd epub && go test ./... -run TestSamplesOpen -count=1")
}

// sampleFile is one archive entry. store marks entries that must not be
// deflated (the mimetype, per the OCF spec).
type sampleFile struct {
	name  string
	data  []byte
	store bool
}

type sample struct {
	name  string
	files []sampleFile
}

// writeEPUB writes the entries in order, so the mimetype stays the first,
// stored entry the EPUB container spec requires.
func writeEPUB(path string, files []sampleFile) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	zw := zip.NewWriter(f)
	for _, e := range files {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: fixedTime}
		if e.store {
			h.Method = zip.Store
			h.Modified = fixedTime
		}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			f.Close()
			return fmt.Errorf("add %s: %w", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			f.Close()
			return fmt.Errorf("write %s: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return fmt.Errorf("close zip %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func containerXML() []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`)
}

// syntheticBook is the sample that covers the converter's dropped-content
// diagnostics: a linearized table, an inline image, a flattened ruby, a
// vector-only SVG, a MathML formula, a font-face rule and print page rules.
func syntheticBook() []sampleFile {
	const css = `@font-face {
  font-family: "Sample Serif";
  src: url(fonts/sample.otf) format("opentype");
}
@page { margin: 2cm }
body { margin: 0; font-size: 18px }
h1 { font-size: 1.6em }
p { text-indent: 2em }
table { margin-bottom: 1em }
td, th { padding: 0.2em }
`
	return []sampleFile{
		{name: "mimetype", data: []byte("application/epub+zip"), store: true},
		{name: "META-INF/container.xml", data: containerXML()},
		{name: "OEBPS/content.opf", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">urn:uuid:layx-synthetic-sample</dc:identifier>
    <dc:title>layx synthetic sample</dc:title>
    <dc:language>en</dc:language>
    <dc:creator>layx</dc:creator>
    <meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="pic" href="pic.png" media-type="image/png"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch3" href="ch3.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
    <itemref idref="ch3"/>
  </spine>
</package>
`)},
		{name: "OEBPS/nav.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Contents</title></head>
<body>
<nav epub:type="toc" id="toc">
  <h1>Contents</h1>
  <ol>
    <li><a href="ch1.xhtml#top">Chapter 1: text and image</a></li>
    <li><a href="ch2.xhtml#top">Chapter 2: table</a></li>
    <li><a href="ch3.xhtml#top">Chapter 3: ruby and preformatted text</a>
      <ol><li><a href="ch3.xhtml#note">A section</a></li></ol>
    </li>
  </ol>
</nav>
</body>
</html>
`)},
		{name: "OEBPS/toc.ncx", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="urn:uuid:layx-synthetic-sample"/></head>
  <docTitle><text>layx synthetic sample</text></docTitle>
  <navMap>
    <navPoint id="n1" playOrder="1"><navLabel><text>Chapter 1: text and image</text></navLabel><content src="ch1.xhtml#top"/></navPoint>
    <navPoint id="n2" playOrder="2"><navLabel><text>Chapter 2: table</text></navLabel><content src="ch2.xhtml#top"/></navPoint>
    <navPoint id="n3" playOrder="3"><navLabel><text>Chapter 3: ruby and preformatted text</text></navLabel><content src="ch3.xhtml#top"/>
      <navPoint id="n4" playOrder="4"><navLabel><text>A section</text></navLabel><content src="ch3.xhtml#note"/></navPoint>
    </navPoint>
  </navMap>
</ncx>
`)},
		{name: "OEBPS/style.css", data: []byte(css)},
		{name: "OEBPS/pic.png", data: swatchPNG()},
		{name: "OEBPS/ch1.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 1</title><link rel="stylesheet" type="text/css" href="style.css"/></head>
<body>
<h1 id="top">Chapter 1: text and image</h1>
<p>This sample is generated by tools/genepub so the epub tests do not depend on
a book copied from another repository. It is deliberately small.</p>
<p>The next paragraph embeds a two by three pixel PNG:
<img src="pic.png" alt="a small colour swatch" width="2" height="3"/> and the
text keeps flowing after it.</p>
<p>An <a href="ch2.xhtml#top">in-book link</a> and an
<a href="https://example.org/outside">external link</a> live here too.</p>
</body>
</html>
`)},
		{name: "OEBPS/ch2.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 2</title><link rel="stylesheet" type="text/css" href="style.css"/></head>
<body>
<h1 id="top">Chapter 2: table</h1>
<p>The render IR has no grid, so a table is linearized:</p>
<table>
  <caption>Sample table</caption>
  <colgroup><col width="40%"/></colgroup>
  <thead><tr><th colspan="2">Header</th></tr></thead>
  <tbody>
    <tr><td>first cell</td><td>second cell</td></tr>
    <tr><td rowspan="2">tall cell</td><td>right one</td></tr>
    <tr><td>right two</td></tr>
  </tbody>
</table>
<blockquote><p>A quoted paragraph keeps its block margins.</p></blockquote>
<ol><li>first item</li><li>second item</li></ol>
</body>
</html>
`)},
		{name: "OEBPS/ch3.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 3</title><link rel="stylesheet" type="text/css" href="style.css"/></head>
<body>
<h1 id="top">Chapter 3: ruby and preformatted text</h1>
<p>Ruby: <ruby>漢<rp>(</rp><rt>かん</rt><rp>)</rp></ruby> is flattened to text.</p>
<pre>  indented
	tab-separated</pre>
<hr/>
<p>An inline vector-only SVG is not renderable:</p>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><path d="M0 0 L10 10"/></svg>
<p>Neither is MathML:</p>
<math xmlns="http://www.w3.org/1998/Math/MathML"><mi>x</mi><mo>=</mo><mn>1</mn></math>
<p id="note">This paragraph is a navigation target.</p>
</body>
</html>
`)},
	}
}

// cleanBook is a sample with no dropped content: a test using it can assert an
// empty issue list.
func cleanBook() []sampleFile {
	return []sampleFile{
		{name: "mimetype", data: []byte("application/epub+zip"), store: true},
		{name: "META-INF/container.xml", data: containerXML()},
		{name: "OEBPS/content.opf", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">urn:uuid:layx-clean-sample</dc:identifier>
    <dc:title>layx clean sample</dc:title>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>
`)},
		{name: "OEBPS/nav.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Contents</title></head>
<body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">Only chapter</a></li></ol></nav></body>
</html>
`)},
		{name: "OEBPS/ch1.xhtml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Only chapter</title></head>
<body>
<h1>Only chapter</h1>
<p>Plain paragraphs and a list. Nothing here needs to be dropped.</p>
<ul><li>one</li><li>two</li></ul>
</body>
</html>
`)},
	}
}

// swatchPNG returns a deterministic two by three PNG.
func swatchPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(0x20 + x*0x40),
				G: uint8(0x60 + y*0x30),
				B: 0xa0,
				A: 0xff,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// png.Encode on an in-memory RGBA cannot fail; a panic here is a
		// programming error, not a runtime condition.
		panic(err)
	}
	return buf.Bytes()
}
