package epub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laic7092/layx/render"
)

// testBook builds a minimal EPUB 3 archive for the diagnostics tests: chapters
// in spine order, extra manifest items (stylesheets, images) through item, and
// the package document assembled by write.
type testBook struct {
	title    string
	manifest strings.Builder
	spine    strings.Builder
	files    map[string][]byte
	nextID   int
}

func newTestBook(title string) *testBook {
	return &testBook{
		title: title,
		files: map[string][]byte{
			"META-INF/container.xml": []byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		},
	}
}

// item adds one manifest entry. A nil data argument leaves the archive entry
// out, which models a book whose manifest references a file it does not carry.
func (b *testBook) item(href, mediaType, props string, data []byte) string {
	b.nextID++
	id := fmt.Sprintf("i%d", b.nextID)
	fmt.Fprintf(&b.manifest, `<item id="%s" href="%s" media-type="%s"`, id, href, mediaType)
	if props != "" {
		fmt.Fprintf(&b.manifest, ` properties="%s"`, props)
	}
	b.manifest.WriteString(`/>`)
	if data != nil {
		b.files["OEBPS/"+href] = data
	}
	return id
}

// spineRef appends one spine reference by manifest id.
func (b *testBook) spineRef(id string) *testBook {
	fmt.Fprintf(&b.spine, `<itemref idref="%s"/>`, id)
	return b
}

// chapter adds one XHTML manifest item and its spine reference.
func (b *testBook) chapter(href, xhtml string) *testBook {
	return b.spineRef(b.item(href, "application/xhtml+xml", "", []byte(xhtml)))
}

// write assembles the package document and writes the archive to a temporary
// file.
func (b *testBook) write(t *testing.T) string {
	t.Helper()
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">` +
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + b.title +
		`</dc:title><dc:language>en</dc:language></metadata>` +
		`<manifest>` + b.manifest.String() + `</manifest>` +
		`<spine>` + b.spine.String() + `</spine></package>`
	b.files["OEBPS/content.opf"] = []byte(opf)
	file := filepath.Join(t.TempDir(), "diagnostics.epub")
	writeEPUB(t, file, b.files)
	return file
}

// openAndBuild opens the crafted archive and builds it, returning the Book, the
// issues pushed through Options.OnIssue and the issues pulled from
// Book.Issues.
func openAndBuild(t *testing.T, file string) (*Book, []render.Issue, []render.Issue) {
	t.Helper()
	b, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var pushed []render.Issue
	_, _, err = b.BuildWith(18, Options{OnIssue: func(i render.Issue) {
		pushed = append(pushed, i)
	}})
	if err != nil {
		t.Fatalf("BuildWith: %v", err)
	}
	return b, pushed, b.Issues()
}

// mustIssue returns the first issue of the given code whose detail contains
// every want substring (case-insensitive), and fails the test otherwise.
func mustIssue(t *testing.T, issues []render.Issue, code render.IssueCode, want ...string) render.Issue {
	t.Helper()
	for _, is := range issues {
		if is.Code != code {
			continue
		}
		detail := strings.ToLower(is.Detail)
		ok := true
		for _, w := range want {
			if !strings.Contains(detail, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			return is
		}
	}
	t.Fatalf("no %v issue containing %q in %v", code, want, issues)
	return render.Issue{}
}

// findIssue is mustIssue's non-failing form.
func findIssue(issues []render.Issue, code render.IssueCode, want ...string) (render.Issue, bool) {
	for _, is := range issues {
		if is.Code != code {
			continue
		}
		detail := strings.ToLower(is.Detail)
		ok := true
		for _, w := range want {
			if !strings.Contains(detail, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			return is, true
		}
	}
	return render.Issue{}, false
}

// sameIssues reports whether two diagnostic lists are element-wise equal.
func sameIssues(a, b []render.Issue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A book with nothing to drop must stay silent: no callback, nil Issues.
func TestDiagnosticsCleanBook(t *testing.T) {
	bk := newTestBook("clean")
	bk.chapter("ch1.xhtml", `<html><body><h1>Clean</h1><p>Just text, nothing to drop.</p></body></html>`)
	file := bk.write(t)

	b, pushed, issues := openAndBuild(t, file)
	if len(issues) != 0 {
		t.Fatalf("clean book reported %v", issues)
	}
	if len(pushed) != 0 {
		t.Fatalf("clean book pushed %v through OnIssue", pushed)
	}
	if got := b.Issues(); got != nil {
		t.Fatalf("clean book Issues() = %v, want nil", got)
	}
}

// A <table> cannot be represented as a grid: the converter linearizes it and
// must say so, including the colspan it had to ignore and the chapter path.
func TestDiagnosticsTableLinearized(t *testing.T) {
	bk := newTestBook("table")
	bk.chapter("ch1.xhtml", `<html><body>
<table>
  <caption>Cap</caption>
  <tr><th colspan="2">Head</th></tr>
  <tr><td>a</td><td>b</td></tr>
</table>
<p>after</p>
</body></html>`)
	_, pushed, issues := openAndBuild(t, bk.write(t))
	if !sameIssues(pushed, issues) {
		t.Fatalf("callback list %v differs from Issues %v", pushed, issues)
	}

	linearized := mustIssue(t, issues, render.IssueDroppedContent, "table", "linearized")
	if linearized.Chapter != "0" {
		t.Errorf("Chapter = %q, want the render chapter ID %q", linearized.Chapter, "0")
	}
	if linearized.Node == render.InvalidNode {
		t.Error("table issue should point at the block node the table became")
	}
	span := mustIssue(t, issues, render.IssueDroppedContent, "colspan")
	if !strings.Contains(span.Detail, "OEBPS/ch1.xhtml") {
		t.Errorf("colspan detail %q lacks the chapter archive path", span.Detail)
	}
}

// An image the converter cannot resolve is dropped from the page; the host
// must be able to tell the user which source went missing.
func TestDiagnosticsDroppedImages(t *testing.T) {
	t.Run("missing-file", func(t *testing.T) {
		bk := newTestBook("image")
		bk.chapter("ch1.xhtml", `<html><body><p><img src="missing.png" width="10" height="10"/></p></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))
		img := mustIssue(t, issues, render.IssueDroppedContent, "missing.png")
		if !strings.Contains(img.Detail, "<img") {
			t.Errorf("image detail %q does not name the element", img.Detail)
		}
	})
	t.Run("unreadable-image", func(t *testing.T) {
		bk := newTestBook("image")
		// In the manifest, but the archive entry is empty: no decodable bytes.
		bk.item("pic.png", "image/png", "", []byte{})
		bk.chapter("ch1.xhtml", `<html><body><p>The picture:</p><p><img src="pic.png"/></p></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))
		mustIssue(t, issues, render.IssueDroppedContent, "pic.png")
	})
	t.Run("unreadable-cover", func(t *testing.T) {
		bk := newTestBook("cover")
		bk.item("cover.png", "image/png", "cover-image", nil) // manifest only
		bk.chapter("ch1.xhtml", `<html><body><p>text</p></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))
		mustIssue(t, issues, render.IssueDroppedContent, "cover.png")
	})
	t.Run("vector-only-svg", func(t *testing.T) {
		bk := newTestBook("svg")
		bk.chapter("ch1.xhtml", `<html><body><svg viewBox="0 0 10 10"><path d="M0 0 L10 10"/></svg></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))
		mustIssue(t, issues, render.IssueDroppedContent, "<svg>")
	})
}

// Ruby annotations are flattened into inline text, so the text survives but the
// ruby layout does not: the converter reports the loss instead of leaving it
// invisible.
func TestDiagnosticsRuby(t *testing.T) {
	bk := newTestBook("ruby")
	bk.chapter("ch1.xhtml", `<html><body><p><ruby>漢<rt>かん</rt></ruby></p></body></html>`)
	_, _, issues := openAndBuild(t, bk.write(t))
	ruby := mustIssue(t, issues, render.IssueDroppedContent, "ruby")
	if ruby.Chapter != "0" {
		t.Errorf("Chapter = %q, want %q", ruby.Chapter, "0")
	}
}

// Embedded fonts, print page rules and selectors the matcher cannot honour are
// all silent degradation in a CSS parser; each has to surface as an issue, and
// a stylesheet the archive does not carry must too.
func TestDiagnosticsCSS(t *testing.T) {
	t.Run("at-rules-and-selectors", func(t *testing.T) {
		bk := newTestBook("css")
		bk.item("book.css", "text/css", "", []byte(`
@charset "utf-8";
@namespace url(http://www.w3.org/1999/xhtml);
@font-face { font-family: "Book Font"; src: url(book.ttf) format("truetype") }
@page { margin: 1cm }
p::before { content: "x" }
p { text-indent: 2em }
`))
		bk.chapter("ch1.xhtml", `<html><head><link rel="stylesheet" href="book.css"/></head>`+
			`<body><p>styled</p></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))

		font := mustIssue(t, issues, render.IssueDroppedContent, "@font-face", "Book Font")
		if !strings.Contains(font.Detail, "book.css") {
			t.Errorf("@font-face detail %q lacks the stylesheet path", font.Detail)
		}
		mustIssue(t, issues, render.IssueDroppedContent, "@page")
		mustIssue(t, issues, render.IssueDroppedContent, "@namespace")
		mustIssue(t, issues, render.IssueDroppedContent, "selectors are supported")
		if is, ok := findIssue(issues, render.IssueDroppedContent, "@charset"); ok {
			t.Errorf("@charset is metadata, not dropped content: %v", is)
		}
	})
	t.Run("missing-stylesheet", func(t *testing.T) {
		bk := newTestBook("css")
		bk.chapter("ch1.xhtml", `<html><head><link rel="stylesheet" href="gone.css"/></head>`+
			`<body><p>unstyled</p></body></html>`)
		_, _, issues := openAndBuild(t, bk.write(t))
		mustIssue(t, issues, render.IssueDroppedContent, "gone.css", "missing")
	})
}

// Issues returns a copy: mutating the returned slice must not change what the
// next call reports.
func TestDiagnosticsIssuesCopy(t *testing.T) {
	bk := newTestBook("copy")
	bk.chapter("ch1.xhtml", `<html><body><table><tr><td colspan="2">x</td></tr></table></body></html>`)
	b, _, issues := openAndBuild(t, bk.write(t))
	if len(issues) == 0 {
		t.Fatal("expected table issues")
	}

	got := b.Issues()
	got[0].Detail = "mutated"
	if again := b.Issues(); again[0].Detail == "mutated" {
		t.Fatal("Issues() handed out its internal slice")
	}
	got = append(got, render.Issue{Detail: "extra"})
	if again := b.Issues(); len(again) != len(issues) {
		t.Fatalf("appending to the returned slice changed the stored list: %d vs %d", len(again), len(issues))
	}
}

// OnIssue delivers every issue once per build, on the caller's goroutine, and
// keeps working across repeated builds of the same Book.
func TestDiagnosticsCallbackPerBuild(t *testing.T) {
	bk := newTestBook("callback")
	bk.chapter("ch1.xhtml", `<html><body><table><tr><td colspan="2">x</td></tr></table></body></html>`)
	file := bk.write(t)
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}

	var calls []render.Issue
	opts := Options{OnIssue: func(i render.Issue) { calls = append(calls, i) }}
	if _, _, err := b.BuildWith(18, opts); err != nil {
		t.Fatal(err)
	}
	first := b.Issues()
	if len(first) == 0 {
		t.Fatal("expected issues from the table")
	}
	if len(calls) != len(first) {
		t.Fatalf("callback ran %d times for %d issues", len(calls), len(first))
	}
	if !sameIssues(calls, first) {
		t.Fatalf("callback list %v differs from Issues %v", calls, first)
	}

	// A second build starts a new list; the callback runs once per issue again.
	calls = nil
	if _, _, err := b.BuildWith(18, opts); err != nil {
		t.Fatal(err)
	}
	if len(calls) != len(first) {
		t.Fatalf("second build delivered %d issues, want %d", len(calls), len(first))
	}

	// Building without the callback must not deliver anything.
	calls = nil
	if _, _, err := b.BuildWith(18, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("build without OnIssue delivered %d issues", len(calls))
	}
}

// A spine item the archive cannot provide is skipped by Open; the host learns
// about it from Issues and from the next build's callback.
func TestDiagnosticsOpenTimeIssue(t *testing.T) {
	bk := newTestBook("open issues")
	bk.chapter("ch1.xhtml", `<html><body><p>readable</p></body></html>`)
	bk.spineRef(bk.item("ch2.xhtml", "application/xhtml+xml", "", nil)) // manifest only
	file := bk.write(t)

	b, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters = %d, want the one readable item", len(b.Chapters))
	}
	pre := b.Issues() // useful before the first Build
	mustIssue(t, pre, render.IssueDroppedContent, "ch2.xhtml")

	var pushed []render.Issue
	if _, _, err := b.BuildWith(18, Options{OnIssue: func(i render.Issue) {
		pushed = append(pushed, i)
	}}); err != nil {
		t.Fatal(err)
	}
	// The open-time finding is folded into every build's list.
	mustIssue(t, pushed, render.IssueDroppedContent, "ch2.xhtml")
	mustIssue(t, b.Issues(), render.IssueDroppedContent, "ch2.xhtml")
}

// The issue list is capped: a pathological book reports the cap plus one
// IssueSuppressed summary instead of letting diagnostics become the memory hog.
func TestDiagnosticsCap(t *testing.T) {
	bk := newTestBook("cap")
	var body strings.Builder
	body.WriteString(`<html><body>`)
	for i := 0; i < maxReportedIssues+76; i++ {
		fmt.Fprintf(&body, `<p><img src="missing-%d.png"/></p>`, i)
	}
	body.WriteString(`</body></html>`)
	bk.chapter("ch1.xhtml", body.String())

	_, pushed, issues := openAndBuild(t, bk.write(t))
	if len(issues) != maxReportedIssues+1 {
		t.Fatalf("issue list = %d entries, want %d plus the suppressed summary", len(issues), maxReportedIssues)
	}
	if last := issues[len(issues)-1]; last.Code != render.IssueSuppressed {
		t.Fatalf("last issue code = %v, want IssueSuppressed", last.Code)
	}
	if !strings.Contains(issues[len(issues)-1].Detail, "76 further") {
		t.Errorf("suppressed summary %q does not count the hidden issues", issues[len(issues)-1].Detail)
	}
	if len(pushed) != len(issues) {
		t.Fatalf("callback delivered %d issues, want %d", len(pushed), len(issues))
	}
}

// The generated samples have to agree with the diagnostics contract: the clean
// one reports nothing, the synthetic one exercises the drops. Both files are
// build products, so the test skips when tools/genepub has not been run.
func TestDiagnosticsGeneratedSamples(t *testing.T) {
	const (
		cleanPath = "testdata/samples/layx-clean.epub"
		richPath  = "testdata/samples/layx-synthetic.epub"
	)
	if _, err := os.Stat(cleanPath); err != nil {
		t.Skip("generated samples are missing; run go run ./tools/genepub from the repository root")
	}
	if _, err := os.Stat(richPath); err != nil {
		t.Skip("generated samples are missing; run go run ./tools/genepub from the repository root")
	}

	_, cleanPushed, cleanIssues := openAndBuild(t, cleanPath)
	if len(cleanIssues) != 0 || len(cleanPushed) != 0 {
		t.Fatalf("clean sample reported %v (pushed %v)", cleanIssues, cleanPushed)
	}

	_, _, richIssues := openAndBuild(t, richPath)
	mustIssue(t, richIssues, render.IssueDroppedContent, "table", "linearized")
	mustIssue(t, richIssues, render.IssueDroppedContent, "@font-face")
	mustIssue(t, richIssues, render.IssueDroppedContent, "ruby")
	mustIssue(t, richIssues, render.IssueDroppedContent, "<svg>")
	mustIssue(t, richIssues, render.IssueDroppedContent, "<math>")
}
