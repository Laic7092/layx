package epub

import (
	"bytes"
	"path/filepath"
	"testing"
)

// An entry larger than the reader keeps is rejected, not silently cut in half:
// a truncated cover used to be handed to the shelf as if it were whole.
func TestReadMetadataSkipsAnOversizedCover(t *testing.T) {
	file := filepath.Join(t.TempDir(), "huge-cover.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf": []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>大封面</dc:title></metadata>
  <manifest><item id="pic" href="pic.png" media-type="image/png" properties="cover-image"/></manifest>
  <spine/>
</package>`),
		"OEBPS/pic.png": bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, int(maxCoverBytes/4)+16),
	})
	meta, err := ReadMetadata(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Cover) != 0 {
		t.Errorf("kept a %d-byte cover over the %d-byte cap", len(meta.Cover), maxCoverBytes)
	}
	if meta.Title != "大封面" {
		t.Errorf("title %q, want the metadata title", meta.Title)
	}
}

// One unreadable spine item must not make the whole book unopenable: the
// remaining chapters are still worth reading.
func TestSpineSkipsAnUnreadableChapter(t *testing.T) {
	file := filepath.Join(t.TempDir(), "partial.epub")
	writeEPUB(t, file, map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf": []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>缺章</dc:title></metadata>
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/><itemref idref="ch2"/></spine>
</package>`),
		"OEBPS/ch1.xhtml": []byte(`<html><body><p>读不到的一章</p></body></html>`),
	})
	b, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters = %d, want the one readable spine item", len(b.Chapters))
	}
	if b.Chapters[0].Title == "" {
		t.Error("the surviving chapter has no title")
	}
}
