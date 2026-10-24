package epub

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetadata(t *testing.T) {
	meta, err := ReadMetadata("testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "最小的书" || meta.Language != "zh-CN" {
		t.Errorf("title %q language %q", meta.Title, meta.Language)
	}
	if len(meta.Cover) != 0 {
		t.Errorf("a book without a cover reported %d bytes", len(meta.Cover))
	}
}

// The cover is found through the manifest's cover-image property, and its
// media type comes from the manifest.
func TestReadMetadataCover(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cover.epub")
	writeImageEPUB(t, file)

	meta, err := ReadMetadata(file)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "有图的书" {
		t.Errorf("title %q", meta.Title)
	}
	if len(meta.Cover) == 0 || meta.CoverType != "image/png" {
		t.Fatalf("cover = %d bytes of %q", len(meta.Cover), meta.CoverType)
	}
}

// A legacy EPUB 2 book points at the cover through <meta name="cover"> and the
// guide, which may name a wrapper page instead of the image.
func TestReadMetadataLegacyCover(t *testing.T) {
	file := filepath.Join(t.TempDir(), "legacy.epub")
	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 0x20, G: 0x60, B: 0xc0, A: 0xff})
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf": []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>旧书</dc:title><dc:creator>某人</dc:creator><dc:language>zh</dc:language>
    <meta name="cover" content="cover-page"/>
  </metadata>
  <manifest><item id="cover-page" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="pic" href="pic.png" media-type="image/png"/></manifest>
  <spine toc="ncx"><itemref idref="ch1"/></spine>
  <guide><reference type="cover" href="cover.xhtml"/></guide>
</package>`),
		"OEBPS/cover.xhtml": []byte(`<html><body><img src="pic.png"/></body></html>`),
		"OEBPS/ch1.xhtml":   []byte(`<html><body><p>正文。</p></body></html>`),
		"OEBPS/pic.png":     pic.Bytes(),
	})

	meta, err := ReadMetadata(file)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "旧书" || meta.Creator != "某人" || meta.Language != "zh" {
		t.Errorf("metadata = %+v", meta)
	}
	if len(meta.Cover) == 0 {
		t.Fatal("the cover behind the wrapper page was not found")
	}
	if meta.CoverType != "image/png" {
		t.Errorf("cover type %q", meta.CoverType)
	}
}

func TestReadMetadataRejectsNonArchive(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-book.epub")
	if err := os.WriteFile(file, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadata(file); !errors.Is(err, ErrNotArchive) {
		t.Errorf("err = %v, want ErrNotArchive", err)
	}
}
