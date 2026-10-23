package render

import (
	"github.com/laix/layx/render/paint"
)

type drawOpKind uint8

const (
	opFill drawOpKind = iota
	opImage
	opGlyphs
)

type drawOp struct {
	kind  drawOpKind
	rect  paint.Rect
	color paint.Color
	image paint.ImageID
	run   paint.GlyphRun
}

// Page is an immutable, cached page. Draw may be called concurrently as long
// as each goroutine uses its own Painter.
type Page struct {
	layout  *Layout
	number  int
	chapter int
	top     float32
	ops     []drawOp
	links   []Link
}

// Number returns the zero-based global page number.
func (p *Page) Number() int { return p.number }

// Size returns the page viewport.
func (p *Page) Size() paint.Size {
	return paint.Size{Width: p.layout.opts.Viewport.Width, Height: p.layout.opts.Viewport.Height}
}

// Draw replays the page display list into painter.
func (p *Page) Draw(painter paint.Painter) {
	if painter == nil {
		return
	}
	painter.Save()
	painter.ClipRect(paint.Rect{X: 0, Y: 0, Width: p.Size().Width, Height: p.Size().Height})
	for i := range p.ops {
		op := &p.ops[i]
		switch op.kind {
		case opFill:
			painter.FillRect(op.rect, op.color)
		case opImage:
			painter.DrawImage(op.image, op.rect, paint.ImageOptions{})
		case opGlyphs:
			painter.DrawGlyphs(op.run)
		}
	}
	painter.Restore()
}

// Links returns the host links on this page.
func (p *Page) Links() []Link {
	out := make([]Link, len(p.links))
	copy(out, p.links)
	return out
}

// Page returns page n, or nil if n is out of range. The page is rendered on
// first access and cached according to Options.PageCache.
func (l *Layout) Page(n int) *Page {
	if n < 0 || n >= len(l.pages) {
		return nil
	}
	limit := l.engine.pageCache
	if limit > 0 {
		l.cacheMu.Lock()
		if p, ok := l.cache[n]; ok {
			l.cacheMu.Unlock()
			return p
		}
		l.cacheMu.Unlock()
	}
	p := l.renderPage(n)
	if limit <= 0 {
		return p
	}
	l.cacheMu.Lock()
	if existing, ok := l.cache[n]; ok {
		l.cacheMu.Unlock()
		return existing
	}
	for len(l.cache) >= limit && len(l.cacheOrder) > 0 {
		old := l.cacheOrder[0]
		l.cacheOrder = l.cacheOrder[1:]
		delete(l.cache, old)
	}
	l.cache[n] = p
	l.cacheOrder = append(l.cacheOrder, n)
	l.cacheMu.Unlock()
	return p
}

func (l *Layout) renderPage(n int) *Page {
	info := &l.pages[n]
	cf := &l.chapters[info.chapter]
	w := l.pool.Get().(*textWorker)
	defer l.pool.Put(w)

	p := &Page{layout: l, number: n, chapter: info.chapter, top: info.top}
	if bg := l.opts.Typography.Background; !bg.Transparent() {
		p.ops = append(p.ops, drawOp{
			kind:  opFill,
			rect:  paint.Rect{X: 0, Y: 0, Width: l.opts.Viewport.Width, Height: l.opts.Viewport.Height},
			color: bg,
		})
	}
	dx := l.opts.Margins.Left
	dy := l.opts.Margins.Top - info.top
	for i := info.first; i < info.last && i < len(cf.items); i++ {
		it := &cf.items[i]
		itemY := dy + it.y
		switch it.kind {
		case itemImage:
			r := paint.Rect{X: dx + it.x, Y: itemY, Width: it.width, Height: it.height}
			p.ops = append(p.ops, drawOp{kind: opImage, rect: r, image: it.image})
			if it.link != "" {
				p.links = append(p.links, Link{Rect: r, URL: it.link})
			}
		case itemLine:
			runs, _ := buildLine(w, it)
			for j := range runs {
				runs[j].X += dx
				// buildLine returns baselines relative to the line box, so the
				// line's own offset within the chapter flow has to be added.
				runs[j].Y += dy + it.y
				p.ops = append(p.ops, drawOp{kind: opGlyphs, run: runs[j]})
			}
			for si := range it.para.segs {
				seg := &it.para.segs[si]
				if seg.link == "" {
					continue
				}
				if int(seg.end) <= int(it.start) || int(seg.start) >= int(it.end) {
					continue
				}
				x0, x1 := lineRuneX(w, it, int(seg.start), int(seg.end))
				p.links = append(p.links, Link{
					Rect: paint.Rect{X: dx + it.x + x0, Y: itemY, Width: x1 - x0, Height: it.height},
					URL:  seg.link,
				})
			}
		}
	}
	return p
}

type glyphPos struct {
	runeIndex int
	x0, x1    float32
}

func isJustifySpace(r rune) bool {
	switch r {
	case ' ', '\u00a0', '\u3000':
		return true
	}
	return false
}

// buildLine shapes one line and returns drawable runs plus per-glyph positions
// relative to the line item's left edge.
func buildLine(w *textWorker, it *flowItem) ([]paint.GlyphRun, []glyphPos) {
	p := it.para
	if p == nil || int(it.end) <= int(it.start) {
		return nil, nil
	}
	runs := w.shapeRange(p, int(it.start), int(it.end), w.defaultLang)
	if len(runs) == 0 {
		return nil, nil
	}
	extra := float32(0)
	if it.align == AlignJustify && !it.last && it.avail > it.width {
		extra = it.avail - it.width
	}
	spaceCount := 0
	glyphCount := 0
	for i := range runs {
		for _, g := range runs[i].out.Glyphs {
			glyphCount++
			idx := g.TextIndex()
			if idx >= 0 && idx < len(p.text) && isJustifySpace(p.text[idx]) {
				spaceCount++
			}
		}
	}
	gapExtra := float32(0)
	if extra > 0 && glyphCount > 1 {
		gapExtra = extra / float32(glyphCount-1)
	}
	spaceExtra := float32(0)
	if extra > 0 && spaceCount > 0 {
		spaceExtra = extra / float32(spaceCount)
		gapExtra = 0
	}

	pen := it.x
	out := make([]paint.GlyphRun, 0, len(runs))
	pos := make([]glyphPos, 0, glyphCount)
	drawn := 0
	for i := range runs {
		sr := &runs[i]
		ref := sr.ref
		color := sr.style.Color
		if color.Transparent() {
			color = w.typo.Color
		}
		run := paint.GlyphRun{
			Font:  ref,
			Size:  float32(sr.out.Size) / 64,
			Color: color,
			X:     pen,
			Y:     it.baseline,
		}
		run.Glyphs = make([]paint.Glyph, 0, len(sr.out.Glyphs))
		for _, g := range sr.out.Glyphs {
			idx := g.TextIndex()
			gx := pen + float32(g.XOffset)/64
			adv := float32(g.Advance) / 64
			glyph := paint.Glyph{
				ID:      uint32(g.GlyphID),
				X:       gx - run.X,
				Y:       float32(g.YOffset) / 64,
				Advance: adv,
				Cluster: int32(idx - int(it.start)),
			}
			pos = append(pos, glyphPos{runeIndex: idx, x0: gx - it.x, x1: gx + adv - it.x})
			pen += adv
			drawn++
			if extra > 0 {
				if spaceExtra > 0 && idx >= 0 && idx < len(p.text) && isJustifySpace(p.text[idx]) {
					pen += spaceExtra
					glyph.Advance += spaceExtra
				} else if gapExtra > 0 && drawn < glyphCount {
					pen += gapExtra
					glyph.Advance += gapExtra
				}
			}
			run.Glyphs = append(run.Glyphs, glyph)
		}
		out = append(out, run)
	}
	return out, pos
}

// lineRuneX maps a paragraph rune range to line-local x coordinates. Positions
// that fall inside a glyph cluster snap to that cluster's edge.
func lineRuneX(w *textWorker, it *flowItem, lo, hi int) (float32, float32) {
	pos := lineGlyphPositions(w, it)
	if len(pos) == 0 {
		return 0, 0
	}
	x0 := pos[len(pos)-1].x1
	for _, p := range pos {
		if p.runeIndex >= lo {
			x0 = p.x0
			break
		}
	}
	x1 := x0
	for _, p := range pos {
		if p.runeIndex >= hi {
			break
		}
		if p.x1 > x1 {
			x1 = p.x1
		}
	}
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	return x0, x1
}

func lineGlyphPositions(w *textWorker, it *flowItem) []glyphPos {
	_, pos := buildLine(w, it)
	return pos
}

// HitTest maps a page coordinate to the nearest document location on this
// page.
func (p *Page) HitTest(pt paint.Point) (Location, bool) {
	l := p.layout
	info := &l.pages[p.number]
	cf := &l.chapters[info.chapter]
	x := pt.X - l.opts.Margins.Left
	y := pt.Y - l.opts.Margins.Top + info.top
	w := l.pool.Get().(*textWorker)
	defer l.pool.Put(w)

	best := -1
	bestDist := float32(1e30)
	for i := info.first; i < info.last && i < len(cf.items); i++ {
		it := &cf.items[i]
		width := it.width
		if it.kind == itemLine {
			width = it.avail
		}
		dx := axisDistance(x, it.x, it.x+width)
		dy := axisDistance(y, it.y, it.y+it.height)
		d := dx*dx + dy*dy
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	if best < 0 {
		return Location{}, false
	}
	it := &cf.items[best]
	if it.kind == itemImage {
		return Location{
			Chapter: info.chapter,
			Node:    it.node,
			Anchor:  cf.nodeAnchor[it.node],
		}, true
	}
	pos := lineGlyphPositions(w, it)
	if len(pos) == 0 {
		return Location{}, false
	}
	runeIndex := pos[len(pos)-1].runeIndex
	if x <= pos[0].x0+it.x {
		runeIndex = pos[0].runeIndex
	} else {
		for _, gp := range pos {
			if x < it.x+gp.x1 {
				runeIndex = gp.runeIndex
				break
			}
		}
	}
	seg := it.para.segAt(runeIndex)
	if seg == nil {
		return Location{}, false
	}
	off := runeIndex - int(seg.start)
	if off < 0 {
		off = 0
	}
	return Location{
		Chapter: info.chapter,
		Node:    seg.node,
		Anchor:  cf.nodeAnchor[seg.node],
		Offset:  off,
	}, true
}

func axisDistance(v, lo, hi float32) float32 {
	if v < lo {
		return lo - v
	}
	if v > hi {
		return v - hi
	}
	return 0
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
