package epub

import (
	"testing"

	"github.com/laic7092/layx/render"
)

// maxFuzzInput caps the bytes a fuzz body will process. The fuzzer can
// synthesize megabytes of input, but the interesting failures (parser edge
// cases, bad nesting, hostile attributes) show up far below this, and the cap
// keeps a CI fuzz run from spending its whole budget on one giant input.
const maxFuzzInput = 1 << 16

// FuzzConvertChapter feeds arbitrary bytes through the real XHTML → render IR
// path: parseTree, the cascade, the converter, anchors, image resolution and
// the diagnostic collector. A panic here is a crash in every host that opens an
// untrusted EPUB, so this target treats the converter as a trust boundary.
//
// The chapter the converter produces is also handed to the document builder,
// which validates the IR: a converter bug that emits an invalid chapter fails
// the fuzz target instead of surfacing as an error in a host much later.
func FuzzConvertChapter(f *testing.F) {
	seeds := []string{
		`<html><body><p>hello</p></body></html>`,
		`<html><body><table><caption>c</caption><tr><td colspan="2">x</td></tr></table></body></html>`,
		`<html><body><ruby>漢<rt>かん</rt></ruby><math><mi>x</mi></math></body></html>`,
		`<html><body><img src="missing.png"/><svg viewBox="0 0 2 2"><image xlink:href="pic.png"/></svg></body></html>`,
		`<html><body><div><span>unclosed<p>block in inline`,
		`<html><body><svg viewBox="0 0 1 1"><path d="M0 0"/></svg></body></html>`,
		`<html><body><video src="v.mp4"><p>fallback</p></video><input value="x"/></body></html>`,
		``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFuzzInput {
			return
		}
		// A real build has one cascade per chapter and one document-wide style
		// table; the resolver stands in for the archive lookup.
		style := newCascade(18, Options{}, nil)
		styles := newStyleRegistry()
		resolve := func(href string, width, height float32) (render.ImageID, float32, float32, bool) {
			if len(href)%4 == 0 {
				return 0, 0, 0, false
			}
			return 1, width, height, true
		}
		anchors := map[string]render.AnchorID{"a": 2, "b": 3}
		issues := newIssueCollector()
		ch, err := convertChapter(data, "fuzz", 1, resolve, anchors, "OEBPS/fuzz.xhtml", style, styles, issues)
		if err != nil {
			// parseTree reads from a bytes.Reader, which cannot fail: an error
			// here would mean the converter lost its in-memory guarantee.
			t.Fatalf("convertChapter: %v", err)
		}
		// The diagnostic list is part of the contract too: flushing it must
		// not panic and must respect the cap.
		if n := len(issues.flush()); n > maxReportedIssues+1 {
			t.Fatalf("issue list has %d entries, cap is %d plus one suppressed summary", n, maxReportedIssues)
		}
		builder := render.NewDocumentBuilder().SetStyles(styles.list())
		if err := builder.AddChapter(ch); err != nil {
			t.Fatalf("converter produced an invalid chapter: %v", err)
		}
		if _, err := builder.Build(); err != nil {
			t.Fatalf("converter produced an invalid document: %v", err)
		}
	})
}

// FuzzParseCSS feeds arbitrary stylesheet bytes through the real CSS parser and
// the cascade: the parser (including @font-face and at-rule reporting), then
// selector matching and declaration application against a fixed element tree.
// Stylesheets come from untrusted EPUBs, so a panic is a host crash.
func FuzzParseCSS(f *testing.F) {
	seeds := []string{
		`p { margin: 1em }`,
		`@font-face { font-family: "Book"; src: url(book.ttf) } body { color: #123456 }`,
		`@media (min-width: 10px) { p { font-size: 1.1em } } @page { margin: 1cm }`,
		`a:hover, p::before, :not(.x) { content: "x" }`,
		`/* unterminated comment`,
		`@import url(a.css); html body > p + p ~ span[data-x="1"] { text-indent: 2em }`,
		`@namespace svg url(http://www.w3.org/2000/svg);`,
		``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	tree, err := parseTree([]byte(`<html><body><h1 id="a" class="x">标题</h1>` +
		`<p class="x" data-x="1"><span>text</span></p><a href="ch2.xhtml#b">link</a></body></html>`))
	if err != nil {
		f.Fatalf("fixed document tree: %v", err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFuzzInput {
			return
		}
		issues := newIssueCollector()
		sheet := parseCSSWith(string(data), "fuzz.css", issues)
		_ = issues.flush()
		rules := make([]cssRule, 0, len(sheet.parts))
		for _, part := range sheet.parts {
			if part.importHref == "" {
				rules = append(rules, part.rule)
			}
		}
		// The fuzzed rules must not panic when matched and applied. The
		// converter is a convenient full exercise of the cascade; the markup is
		// fixed so only the stylesheet varies.
		style := newCascade(18, Options{}, rules)
		ch, err := convertTree(tree, "fuzz", 1, nil, nil, "OEBPS/fuzz.xhtml", style, newStyleRegistry(), issues)
		if err != nil {
			t.Fatalf("convertTree with fuzzed rules: %v", err)
		}
		if len(ch.Nodes) == 0 {
			t.Fatal("converter produced an empty chapter")
		}
	})
}

// FuzzParseNCX feeds arbitrary bytes to the XML navigation reader. The NCX is
// the EPUB 2 table of contents and arrives from the untrusted archive like
// everything else; it must never panic, whatever the XML contains.
func FuzzParseNCX(f *testing.F) {
	seeds := []string{
		`<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>` +
			`<navPoint><navLabel><text>Chapter</text></navLabel><content src="ch1.xhtml"/>` +
			`<navPoint><navLabel><text>Section</text></navLabel><content src="ch1.xhtml#s1"/></navPoint>` +
			`</navPoint></navMap></ncx>`,
		`<ncx><navMap/></ncx>`,
		`<?xml version="1.0"?><ncx><navMap><navPoint><content src="../x.xhtml#y"/></navPoint></navMap></ncx>`,
		`<?xml`,
		``,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFuzzInput {
			return
		}
		nodes := parseNCX(data, "OEBPS/toc.ncx")
		// Collecting titles walks the parsed tree; it must be total.
		collectTOCTitles(nodes, map[string]string{})
	})
}
