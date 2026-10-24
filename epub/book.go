package epub

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig in imageLoader
	_ "image/jpeg" // DecodeConfig in imageLoader
	_ "image/png"  // DecodeConfig in imageLoader
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/laix/layx/render"
)

// Chapter is one spine item: a title for the UI and the original XHTML.
type Chapter struct {
	Title string
	XHTML []byte
}

// TOCEntry is one row of the book's navigation document, in reading order.
//
// Chapter is the index into Book.Chapters the entry points at, or -1 when the
// target is outside the reading order (the cover, a linear="no" item, a note
// page). Anchor is the render anchor to hand to Layout.Locate: it names a node
// inside that chapter, not just its start. Href keeps the archive path and
// fragment for debugging and for hosts that resolve links themselves.
type TOCEntry struct {
	Title   string
	Depth   int // 0-based nesting level
	Href    string
	Chapter int
	Anchor  render.AnchorID
}

// imageEntry assigns a stable ID to a manifest image before its bytes are
// needed. The ID is what a render node and the painter share.
type imageEntry struct {
	id render.ImageID
}

// Book is an EPUB opened for reading.
type Book struct {
	Title    string
	Language string
	Chapters []Chapter

	// Cover is the ID of the cover image and HasCover reports whether the
	// book has one. The UI shows a cover on its own, before the first page.
	Cover    render.ImageID
	HasCover bool

	path      string
	paths     []string // archive path of each chapter, parallel to Chapters
	images    map[string]imageEntry
	coverHref string

	// Toc is the navigation document flattened into reading order, with
	// anchors resolved to chapter nodes. anchorIDs holds, per chapter, the
	// element id -> anchor mapping the converter must install; it covers both
	// navigation targets and the targets of in-book links.
	Toc         []TOCEntry
	anchorIDs   []map[string]render.AnchorID
	nextAnchor  render.AnchorID
	linkTargets map[string]LinkTarget
}

// openEPUB reads an EPUB file and returns its reading order. The archive
// stays closed: Book.Build reopens it only when it needs image bytes, so keep
// the file where it was opened from.
func openEPUB(file string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("epub: open %s: %w", file, err)
	}
	defer zr.Close()

	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[path.Clean(f.Name)] = f
	}
	read := func(name string) ([]byte, error) {
		name = path.Clean(strings.TrimPrefix(name, "/"))
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("epub: %s is missing from the archive", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("epub: read %s: %w", name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	pkg, opfPath, err := readPackage(files)
	if err != nil {
		return nil, err
	}

	b := &Book{Language: firstNonEmpty(pkg.Metadata.Language), path: abs, images: map[string]imageEntry{}}
	for _, t := range pkg.Metadata.Title {
		if strings.TrimSpace(t) != "" {
			b.Title = strings.TrimSpace(t)
			break
		}
	}

	type item struct {
		href       string
		mediaType  string
		properties string
	}
	manifest := make(map[string]item, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		manifest[it.ID] = item{
			href:       resolve(opfPath, it.Href),
			mediaType:  it.MediaType,
			properties: it.Properties,
		}
	}

	// Image IDs are stable for the life of the Book, even though the bytes
	// are read lazily by Build.
	nextImage := render.ImageID(0)
	for _, it := range pkg.Manifest.Items {
		if !strings.HasPrefix(strings.ToLower(it.MediaType), "image/") {
			continue
		}
		href := resolve(opfPath, it.Href)
		if _, ok := b.images[href]; ok {
			continue
		}
		b.images[href] = imageEntry{id: nextImage}
		nextImage++
	}

	// Cover discovery, in the order EPUB itself defines it.
	b.coverHref = pkg.coverHref(opfPath, func(href string) bool {
		_, ok := b.images[href]
		return ok
	}, read)
	if e, ok := b.images[b.coverHref]; ok {
		b.Cover, b.HasCover = e.id, true
	}

	// Navigation document, kept as a tree: order, nesting and fragments all
	// matter. The first source with entries wins; NCX is the EPUB 2 fallback.
	var navNodes []tocNode
	for _, it := range pkg.Manifest.Items {
		if !hasProperty(it.Properties, "nav") {
			continue
		}
		href := resolve(opfPath, it.Href)
		data, err := read(href)
		if err != nil {
			continue
		}
		if navNodes = parseNavTOC(data, href); len(navNodes) > 0 {
			break
		}
	}
	if len(navNodes) == 0 && pkg.Spine.Toc != "" {
		if it, ok := manifest[pkg.Spine.Toc]; ok {
			if data, err := read(it.href); err == nil {
				navNodes = parseNCX(data, it.href)
			}
		}
	}
	tocTitles := map[string]string{}
	collectTOCTitles(navNodes, tocTitles)

	seen := make(map[string]bool)
	var chapterPaths []string
	for _, ref := range pkg.Spine.ItemRef {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		it, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}
		if it.mediaType != "" && !isHTMLMediaType(it.mediaType) {
			continue
		}
		if seen[it.href] {
			continue
		}
		seen[it.href] = true
		data, err := read(it.href)
		if err != nil {
			return nil, err
		}
		title := tocTitles[it.href]
		if title == "" {
			title = headingTitle(data)
		}
		if title == "" {
			title = fmt.Sprintf("第 %d 章", len(b.Chapters)+1)
		}
		b.Chapters = append(b.Chapters, Chapter{Title: title, XHTML: data})
		chapterPaths = append(chapterPaths, it.href)
	}

	// A converter that bundles no cover metadata still usually starts with a
	// page that is nothing but the cover image. Treat it as the cover.
	if !b.HasCover && len(b.Chapters) > 0 && imageOnly(b.Chapters[0].XHTML) {
		if href := firstImageHref(b.Chapters[0].XHTML); href != "" {
			p := resolve(chapterPaths[0], href)
			if e, ok := b.images[p]; ok {
				b.Cover, b.coverHref, b.HasCover = e.id, p, true
			}
		}
	}

	// A cover page is shown by the UI on its own, not as a page of text.
	if b.HasCover {
		for i := 0; i < len(b.Chapters) && i < 5; i++ {
			if !imageOnly(b.Chapters[i].XHTML) {
				continue
			}
			href := firstImageHref(b.Chapters[i].XHTML)
			if href == "" || resolve(chapterPaths[i], href) != b.coverHref {
				continue
			}
			b.Chapters = append(b.Chapters[:i], b.Chapters[i+1:]...)
			chapterPaths = append(chapterPaths[:i], chapterPaths[i+1:]...)
			break
		}
	}
	b.paths = chapterPaths
	b.attachTOC(navNodes, chapterPaths)
	b.prepareLinks(chapterPaths)
	if len(b.Chapters) == 0 {
		return nil, errors.New("epub: the spine has no readable chapters")
	}
	if b.Title == "" {
		b.Title = "未命名书籍"
	}
	return b, nil
}

// Document converts the whole book into one render document. Headings and
// indents are relative to a base font size of 18 CSS pixels; DocumentSized
// takes another base so the UI can change the reading size.
func (b *Book) Document() (*render.Document, error) {
	return b.DocumentSized(18)
}

// DocumentSized is Document with an explicit base font size.
func (b *Book) DocumentSized(base float32) (*render.Document, error) {
	doc, _, err := b.Build(base)
	return doc, err
}

// Build converts the whole book into one render document and returns the
// image bytes its chapters reference, keyed by the IDs the nodes carry. The
// caller hands those bytes to its painter once; the map is per build, while
// the IDs stay stable for the life of the Book. The archive is reopened here
// and closed before Build returns.
func (b *Book) Build(base float32) (*render.Document, map[render.ImageID][]byte, error) {
	return b.BuildWith(base, Options{})
}

// BuildWith is Build with the host's choices: a base font size for headings and
// indents, the colour link text is painted in, and whether the host owns ink
// and percentage lengths.
func (b *Book) BuildWith(base float32, o Options) (*render.Document, map[render.ImageID][]byte, error) {
	if base <= 0 {
		base = 18
	}
	if len(b.Chapters) == 0 {
		return nil, nil, errors.New("epub: no chapters to lay out")
	}

	load := &imageLoader{
		book:   b,
		out:    map[render.ImageID][]byte{},
		sizes:  map[render.ImageID][2]float32{},
		sheets: map[string]cssSheet{},
	}
	if b.path != "" {
		// The archive is reopened for image bytes and stylesheets. A book whose
		// markup is already in memory can still be built without it when it has
		// no images and no linked CSS.
		zr, err := zip.OpenReader(b.path)
		if err != nil {
			if len(b.images) > 0 {
				return nil, nil, fmt.Errorf("epub: reopen %s: %w", b.path, err)
			}
		} else {
			defer zr.Close()
			load.zip = zr
			load.files = make(map[string]*zip.File, len(zr.File))
			load.lower = make(map[string]*zip.File, len(zr.File))
			for _, f := range zr.File {
				name := path.Clean(f.Name)
				load.files[name] = f
				load.lower[strings.ToLower(name)] = f
			}
		}
	}

	// Convert every chapter before building the document: style IDs live in a
	// document-wide table, so the table must be complete before the first
	// AddChapter call.
	styles := newStyleRegistry()
	ua := uaRules(base, o.linkColor(), o.ruleColor())
	chapters := make([]render.Chapter, len(b.Chapters))
	for i, ch := range b.Chapters {
		basePath := ""
		if i < len(b.paths) {
			basePath = b.paths[i]
		}
		tree, err := parseTree(ch.XHTML)
		if err != nil {
			return nil, nil, fmt.Errorf("epub: chapter %d (%s): %w", i+1, ch.Title, err)
		}
		style := newCascadeWithUA(base, o, ua, load.chapterRules(tree, basePath))
		chapter, err := convertTree(tree, strconv.Itoa(i), render.AnchorID(i+1), load.forBase(basePath), b.chapterAnchors(i), basePath, style, styles)
		if err != nil {
			return nil, nil, fmt.Errorf("epub: chapter %d (%s): %w", i+1, ch.Title, err)
		}
		chapters[i] = chapter
	}

	builder := render.NewDocumentBuilder().
		SetStyles(styles.list()).
		SetMetadata(render.Metadata{
			Title:      b.Title,
			Language:   b.Language,
			Reflowable: true,
		})
	for i, chapter := range chapters {
		if err := builder.AddChapter(chapter); err != nil {
			return nil, nil, fmt.Errorf("epub: chapter %d (%s): %w", i+1, b.Chapters[i].Title, err)
		}
	}
	// The cover page is outside the reading flow, but its image is still
	// needed for the cover itself.
	if b.HasCover {
		load.load(b.coverHref, 0, 0)
	}
	doc, err := builder.Build()
	if err != nil {
		return nil, nil, err
	}
	return doc, load.out, nil
}

// imageLoader reads images from the reopened archive on demand and remembers
// what one build already read.
type imageLoader struct {
	book   *Book
	zip    *zip.ReadCloser
	files  map[string]*zip.File
	lower  map[string]*zip.File // lower-cased names, for inconsistent archives
	sheets map[string]cssSheet  // parsed stylesheets, one entry per archive path
	out    map[render.ImageID][]byte
	sizes  map[render.ImageID][2]float32 // intrinsic size of each loaded image
}

func (l *imageLoader) forBase(base string) ImageResolver {
	return func(href string, widthHint, heightHint float32) (render.ImageID, float32, float32, bool) {
		if strings.TrimSpace(href) == "" {
			return 0, 0, 0, false
		}
		return l.load(resolve(base, href), widthHint, heightHint)
	}
}

func (l *imageLoader) load(name string, widthHint, heightHint float32) (render.ImageID, float32, float32, bool) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	entry, ok := l.book.images[name]
	if !ok {
		return 0, 0, 0, false
	}
	if _, ok := l.out[entry.id]; ok {
		sz := l.sizes[entry.id]
		return entry.id, sz[0], sz[1], true
	}
	if l.files == nil {
		return 0, 0, 0, false
	}
	f, ok := l.files[name]
	if !ok {
		return 0, 0, 0, false
	}
	rc, err := f.Open()
	if err != nil {
		return 0, 0, 0, false
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return 0, 0, 0, false
	}
	w, h := imageSize(data, widthHint, heightHint)
	if w <= 0 && h <= 0 {
		return 0, 0, 0, false
	}
	l.out[entry.id] = data
	l.sizes[entry.id] = [2]float32{w, h}
	return entry.id, w, h, true
}

// imageSize prefers the encoded image's own size and falls back to the hints
// the markup gave.
func imageSize(data []byte, widthHint, heightHint float32) (float32, float32) {
	w, h := widthHint, heightHint
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
		w, h = float32(cfg.Width), float32(cfg.Height)
	}
	return w, h
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasProperty(props, want string) bool {
	for _, p := range strings.Fields(props) {
		if p == want {
			return true
		}
	}
	return false
}

func isHTMLMediaType(mt string) bool {
	switch strings.ToLower(mt) {
	case "application/xhtml+xml", "text/html", "application/x-dtbook+xml":
		return true
	}
	return false
}

// resolve joins href to the directory of base and strips any fragment.
func resolve(base, href string) string {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	if href == "" {
		return path.Clean(base)
	}
	href = strings.TrimPrefix(href, "/")
	// Zip paths have no root: every href is relative to the document that
	// names it, and a leading slash only means "from the archive root".
	return path.Clean(path.Join(path.Dir(base), href))
}

// splitHref resolves href against base and keeps the fragment, which is the
// part that names a section inside a spine item.
func splitHref(base, href string) (path, frag string) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", ""
	}
	if i := strings.IndexByte(href, '#'); i >= 0 {
		frag = href[i+1:]
		href = href[:i]
		if unescaped, err := url.PathUnescape(frag); err == nil {
			frag = unescaped
		}
	}
	return resolve(base, href), frag
}

// isExternalHref reports whether href leaves the book: any URL with a scheme
// (http, https, mailto, tel, data) or a protocol-relative //host. Fragment-only
// and relative paths stay inside the archive.
func isExternalHref(href string) bool {
	href = strings.TrimSpace(href)
	if href == "" {
		return false
	}
	if strings.HasPrefix(href, "//") {
		return true
	}
	u, err := url.Parse(href)
	return err == nil && u.Scheme != ""
}

// collectTOCTitles keeps the first title written for each archive path, which
// is the chapter's label in the navigation document.
func collectTOCTitles(nodes []tocNode, dst map[string]string) {
	for _, n := range nodes {
		if n.Path != "" && n.Title != "" {
			if _, ok := dst[n.Path]; !ok {
				dst[n.Path] = n.Title
			}
		}
		collectTOCTitles(n.Children, dst)
	}
}

// attachTOC matches navigation targets to the spine and assigns an anchor to
// every fragment. Chapter roots keep their existing AnchorID (chapter index+1);
// fragment anchors start after them and stay stable for the life of the Book.
func (b *Book) attachTOC(nodes []tocNode, chapterPaths []string) {
	index := make(map[string]int, len(chapterPaths))
	for i, p := range chapterPaths {
		index[p] = i
	}
	b.anchorIDs = make([]map[string]render.AnchorID, len(b.Chapters))
	b.nextAnchor = render.AnchorID(len(b.Chapters) + 1)
	next := b.nextAnchor

	var walk func(nodes []tocNode, depth int)
	walk = func(nodes []tocNode, depth int) {
		for _, n := range nodes {
			e := TOCEntry{Title: n.Title, Depth: depth, Chapter: -1, Href: n.Path}
			if n.Frag != "" {
				e.Href += "#" + n.Frag
			}
			if ci, ok := index[n.Path]; ok && n.Path != "" {
				e.Chapter = ci
				if n.Frag == "" {
					e.Anchor = render.AnchorID(ci + 1)
				} else {
					if b.anchorIDs[ci] == nil {
						b.anchorIDs[ci] = map[string]render.AnchorID{}
					}
					if a, ok := b.anchorIDs[ci][n.Frag]; ok {
						e.Anchor = a
					} else {
						e.Anchor = next
						b.anchorIDs[ci][n.Frag] = next
						next++
					}
				}
			}
			if e.Title == "" && e.Chapter < 0 {
				// A bare grouping <li>: no row, children keep their depth.
				walk(n.Children, depth)
				continue
			}
			if e.Title == "" {
				e.Title = b.Chapters[e.Chapter].Title
			}
			b.Toc = append(b.Toc, e)
			walk(n.Children, depth+1)
		}
	}
	walk(nodes, 0)
	b.nextAnchor = next
}

// LinkTarget is where an in-book link goes. Anchor names the target element, or
// the chapter root when the fragment is missing.
type LinkTarget struct {
	Chapter int
	Anchor  render.AnchorID
}

// ResolveLink maps a Node.Link URL that points inside the book to a chapter and
// anchor. ok is false for external URLs and for files outside the reading order,
// which the host should hand to its own URL opener.
func (b *Book) ResolveLink(url string) (LinkTarget, bool) {
	t, ok := b.linkTargets[url]
	return t, ok
}

// linkRef is one internal href found in a chapter.
type linkRef struct {
	path, frag string
}

// prepareLinks scans every chapter once for element ids and in-book links, then
// gives each link target an anchor. Only ids that something actually points at
// get an anchor, so a book with thousands of decorative ids does not pay for
// them in every Layout; unreferenced ids stay plain markup.
func (b *Book) prepareLinks(chapterPaths []string) {
	index := make(map[string]int, len(chapterPaths))
	for i, p := range chapterPaths {
		index[p] = i
	}
	ids := make([]map[string]bool, len(b.Chapters))
	var targets []linkRef
	for i, ch := range b.Chapters {
		tree, err := parseTree(ch.XHTML)
		if err != nil {
			continue
		}
		ids[i] = map[string]bool{}
		base := ""
		if i < len(chapterPaths) {
			base = chapterPaths[i]
		}
		var walk func(*hnode)
		walk = func(n *hnode) {
			if id := attr(n, "id"); id != "" {
				ids[i][id] = true
			}
			if n.tag == "a" {
				if href := attr(n, "href"); href != "" && !isExternalHref(href) {
					p, frag := splitHref(base, href)
					if ci, ok := index[p]; ok {
						targets = append(targets, linkRef{path: chapterPaths[ci], frag: frag})
					}
				}
			}
			for _, child := range n.children {
				if child.tag != "" {
					walk(child)
				}
			}
		}
		walk(tree)
	}

	b.linkTargets = map[string]LinkTarget{}
	for _, t := range targets {
		ci, ok := index[t.path]
		if !ok {
			continue
		}
		lt := LinkTarget{Chapter: ci, Anchor: render.AnchorID(ci + 1)}
		if t.frag != "" && ids[ci][t.frag] {
			if a, ok := b.anchorIDs[ci][t.frag]; ok {
				lt.Anchor = a
			} else {
				if b.anchorIDs[ci] == nil {
					b.anchorIDs[ci] = map[string]render.AnchorID{}
				}
				a := b.nextAnchor
				b.nextAnchor++
				b.anchorIDs[ci][t.frag] = a
				lt.Anchor = a
			}
		}
		key := t.path
		if t.frag != "" {
			key += "#" + t.frag
		}
		b.linkTargets[key] = lt
	}
}

func (b *Book) chapterAnchors(i int) map[string]render.AnchorID {
	if i >= 0 && i < len(b.anchorIDs) {
		return b.anchorIDs[i]
	}
	return nil
}
