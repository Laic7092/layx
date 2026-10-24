package epub

import (
	"io"
	"path"
	"strings"
)

// styleRef is one place a chapter gets author CSS from: a linked stylesheet or
// a <style> block.
type styleRef struct {
	href string // archive path, resolved against the chapter
	css  string // inline <style> content
}

// collectStyleRefs walks markup in document order and collects stylesheet
// links and <style> blocks. The order matters: later rules win at equal
// specificity.
func collectStyleRefs(n *hnode, out *[]styleRef) {
	switch n.tag {
	case "":
		return
	case "link":
		if hasRelStylesheet(attr(n, "rel")) {
			if href := strings.TrimSpace(attr(n, "href")); href != "" {
				*out = append(*out, styleRef{href: href})
			}
		}
		return
	case "style":
		if css := textContent(n); strings.TrimSpace(css) != "" {
			*out = append(*out, styleRef{css: css})
		}
		return
	}
	for _, child := range n.children {
		if child.tag != "" {
			collectStyleRefs(child, out)
		}
	}
}

// hasRelStylesheet reports whether a link rel token list names a stylesheet.
func hasRelStylesheet(rel string) bool {
	for _, token := range strings.Fields(rel) {
		if strings.EqualFold(token, "stylesheet") {
			return true
		}
	}
	return false
}

// chapterRules returns the author rules that apply to one chapter, with
// @imports expanded in place. Linked sheets are parsed once per Build even
// when every chapter links the same file.
func (l *imageLoader) chapterRules(tree *hnode, base string) []cssRule {
	var refs []styleRef
	collectStyleRefs(tree, &refs)
	if len(refs) == 0 {
		return nil
	}
	var rules []cssRule
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.href != "" {
			l.appendSheet(resolve(base, ref.href), 0, seen, &rules)
			continue
		}
		l.appendParts(parseCSS(ref.css), base, 0, seen, &rules)
	}
	return rules
}

// appendSheet flattens one stylesheet into rules. depth caps @import chains at
// the two levels CSS allows in practice; seen breaks cycles.
func (l *imageLoader) appendSheet(name string, depth int, seen map[string]bool, out *[]cssRule) {
	if depth > 2 {
		return
	}
	name = path.Clean(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "" || name == "." || seen[name] {
		return
	}
	seen[name] = true
	sheet, ok := l.sheets[name]
	if !ok {
		if data, found := l.readFile(name); found {
			sheet = parseCSS(string(data))
		}
		l.sheets[name] = sheet
	}
	l.appendParts(sheet, name, depth, seen, out)
}

func (l *imageLoader) appendParts(sheet cssSheet, sheetPath string, depth int, seen map[string]bool, out *[]cssRule) {
	for _, part := range sheet.parts {
		if part.importHref != "" {
			l.appendSheet(resolve(sheetPath, part.importHref), depth+1, seen, out)
			continue
		}
		*out = append(*out, part.rule)
	}
}

// readFile reads one archive entry by name, falling back to a lower-cased
// match because some converters disagree with themselves about case.
func (l *imageLoader) readFile(name string) ([]byte, bool) {
	if l.files == nil {
		return nil, false
	}
	name = path.Clean(strings.TrimPrefix(name, "/"))
	f, ok := l.files[name]
	if !ok && l.lower != nil {
		f, ok = l.lower[strings.ToLower(name)]
	}
	if !ok {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	return data, true
}
