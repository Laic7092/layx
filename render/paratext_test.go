package render

// paraRunes materializes a paragraph's runes for white-box tests. The engine
// no longer retains them: they are materialized from the source strings on
// demand (see textWorker.paraText).
func paraRunes(p *paragraph) []rune {
	out := make([]rune, 0, p.runeLen)
	for i := range p.segs {
		for _, r := range p.segs[i].text {
			out = append(out, r)
		}
	}
	return out
}

// lineText returns the runes a line box covers.
func lineText(it *flowItem) string {
	if it.para == nil {
		return ""
	}
	return string(paraRunes(it.para)[it.start:it.end])
}

// itemsOf materializes a chapter's whole flow. Tests compare item by item;
// the engine itself stores only item tops (see chapterFlow).
func itemsOf(cf *chapterFlow) []flowItem {
	out := make([]flowItem, cf.count())
	for i := range out {
		out[i] = cf.item(i)
	}
	return out
}

// pageItemOf projects a hand-built flowItem for pagination tests.
func pageItemOf(it *flowItem) pageItem {
	return pageItem{
		y: it.y, height: it.height, para: it.para,
		paraLine: it.paraLine, paraLines: it.paraLines,
		breakBefore: it.breakBefore, breakAfter: it.breakAfter,
		keepTogether: it.keepTogether, keepGroup: it.keepGroup,
		widows: it.widows, orphans: it.orphans,
	}
}
