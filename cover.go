package raster

import (
	"slices"

	"github.com/laix/layx/render/paint"
)

// coverageSamples is the number of vertical sub-scanlines per pixel row.
// Horizontal coverage is analytic, so the total is 8 samples per pixel with
// exact x areas; that is enough to keep text edges smooth without a mask the
// size of the surface for every glyph.
const coverageSamples = 8

// edge is one side of a contour, already in device space. dir is +1 for a
// downward edge and -1 for an upward one; the non-zero winding rule only needs
// consistent signs.
type edge struct {
	x0, y0, x1, y1 float32
	// ymin and ymax are y0 and y1 sorted; dxdy is dx/dy, so a scanline's
	// crossing needs one multiply instead of a division. xc is this edge's x
	// at the row's middle, filled in per row for the crossing sort.
	ymin, ymax float32
	dxdy       float32
	xc         float32
	dir        int8
}

type crossing struct {
	x   float32
	dir int8
}

// shapeEdges flattens every contour of s into edges. Horizontal edges are
// skipped: a scanline never crosses one.
func shapeEdges(s *shape, edges []edge) []edge {
	for i := range s.starts {
		start, end := s.contourRange(i)
		if end-start < 3 {
			continue
		}
		for j := start; j < end; j++ {
			k := j + 1
			if k == end {
				k = start
			}
			a, b := s.pts[j], s.pts[k]
			if a.Y == b.Y || !finitePoint(a) || !finitePoint(b) {
				continue
			}
			e := edge{x0: a.X, y0: a.Y, x1: b.X, y1: b.Y, dir: 1}
			if b.Y < a.Y {
				e.dir = -1
			}
			e.ymin, e.ymax = a.Y, b.Y
			if e.ymin > e.ymax {
				e.ymin, e.ymax = e.ymax, e.ymin
			}
			e.dxdy = (b.X - a.X) / (b.Y - a.Y)
			edges = append(edges, e)
		}
	}
	return edges
}

// coverageScratch is the reusable working set of one rasterizer. Keeping it in
// one struct lets the active list and the crossings keep their capacity from
// one fill to the next.
type coverageScratch struct {
	cov    []float32
	active []edge
	cross  []crossing
}

// rasterizeCoverage fills s.cov with the fraction of each pixel in area
// covered by the non-zero winding of edges, one value per pixel in row-major
// order.
//
// Each pixel row is sampled at coverageSamples sub-scanlines; horizontal
// coverage is exact for each span. An active edge list keeps the work
// proportional to the edges that reach the row (a glyph has hundreds of edges
// but only a few cross any one scanline), and the crossings are sorted once per
// row, by the row's middle, instead of once per sub-scanline: within a pixel
// row the outline almost never crosses itself, so the order holds for every
// sample and each sample only drops the edges that do not reach it.
func rasterizeCoverage(edges []edge, area pixArea, s *coverageScratch) {
	n := area.width() * area.height()
	if cap(s.cov) < n {
		s.cov = make([]float32, n)
	}
	s.cov = s.cov[:n]
	for i := range s.cov {
		s.cov[i] = 0
	}
	s.active = s.active[:0]
	if len(edges) == 0 {
		return
	}

	slices.SortFunc(edges, func(a, b edge) int {
		switch {
		case a.ymin < b.ymin:
			return -1
		case a.ymin > b.ymin:
			return 1
		}
		return 0
	})

	const weight = 1 / float32(coverageSamples)
	width := area.width()
	next := 0
	cross := s.cross[:0]

	for row := area.Y0; row < area.Y1; row++ {
		top := float32(row)
		bottom := top + 1
		for next < len(edges) && edges[next].ymin < bottom {
			s.active = append(s.active, edges[next])
			next++
		}
		if len(s.active) > 0 {
			kept := 0
			for _, e := range s.active {
				if e.ymax > top {
					s.active[kept] = e
					kept++
				}
			}
			s.active = s.active[:kept]
		}
		if len(s.active) < 2 {
			continue
		}

		mid := top + 0.5
		for i := range s.active {
			e := &s.active[i]
			e.xc = e.x0 + (mid-e.y0)*e.dxdy
		}
		sortActiveByX(s.active)

		rowCov := s.cov[(row-area.Y0)*width : (row-area.Y0)*width+width]
		for sample := 0; sample < coverageSamples; sample++ {
			y := top + (float32(sample)+0.5)*weight
			cross = cross[:0]
			for i := range s.active {
				e := &s.active[i]
				if y < e.ymin || y >= e.ymax {
					continue
				}
				cross = append(cross, crossing{x: e.x0 + (y-e.y0)*e.dxdy, dir: e.dir})
			}
			if len(cross) < 2 {
				continue
			}
			// The row's order holds unless the outline crosses itself inside
			// this row; check instead of trusting, and fix it if it does not.
			sorted := true
			for i := 1; i < len(cross); i++ {
				if cross[i].x < cross[i-1].x {
					sorted = false
					break
				}
			}
			if !sorted {
				sortCrossings(cross)
			}
			wind := 0
			for i := 0; i+1 < len(cross); i++ {
				wind += int(cross[i].dir)
				if wind == 0 {
					continue
				}
				addSpan(rowCov, cross[i].x-float32(area.X0), cross[i+1].x-float32(area.X0), weight)
			}
		}
	}
	s.cross = cross[:0]
}

func sortActiveByX(active []edge) {
	for i := 1; i < len(active); i++ {
		e := active[i]
		j := i - 1
		for j >= 0 && active[j].xc > e.xc {
			active[j+1] = active[j]
			j--
		}
		active[j+1] = e
	}
}

// edgesFor flattens s into the rasterizer's scratch slice.
func (r *Rasterizer) edgesFor(s *shape) []edge {
	r.edges = shapeEdges(s, r.edges[:0])
	return r.edges
}

// coverage rasterizes edges into the rasterizer's scratch slice.
func (r *Rasterizer) coverage(edges []edge, area pixArea) []float32 {
	rasterizeCoverage(edges, area, &r.scratch)
	return r.scratch.cov[:area.width()*area.height()]
}

func sortCrossings(xs []crossing) {
	for i := 1; i < len(xs); i++ {
		c := xs[i]
		j := i - 1
		for j >= 0 && xs[j].x > c.x {
			xs[j+1] = xs[j]
			j--
		}
		xs[j+1] = c
	}
}

// addSpan adds the horizontal coverage of [x0,x1) (in pixels relative to the
// row) to row, weighted by the sub-scanline share.
func addSpan(row []float32, x0, x1, weight float32) {
	if x1 <= x0 {
		return
	}
	n := len(row)
	if x0 < 0 {
		x0 = 0
	}
	if x1 > float32(n) {
		x1 = float32(n)
	}
	if x1 <= x0 {
		return
	}
	i0 := int(x0)
	i1 := int(x1)
	if i1 >= n {
		i1 = n - 1
	}
	if i0 == i1 {
		row[i0] += (x1 - x0) * weight
		return
	}
	row[i0] += (float32(i0+1) - x0) * weight
	for i := i0 + 1; i < i1; i++ {
		row[i] += weight
	}
	row[i1] += (x1 - float32(i1)) * weight
}

// fillShape rasterizes s with the non-zero winding rule and blends it into the
// surface through the current clip.
func (r *Rasterizer) fillShape(s *shape, c paint.Color) {
	if c.A == 0 || len(s.starts) == 0 {
		return
	}
	b, ok := s.bounds()
	if !ok {
		return
	}
	area := intersectArea(areaOf(b, r.w, r.h), r.state.clip.area)
	if area.empty() {
		return
	}
	r.blendCoverage(area, r.coverage(r.edgesFor(s), area), c)
}

// fillDeviceRect fills an axis-aligned device rectangle with exact per-pixel
// area coverage. Page backgrounds and borders take this path, so a full-page
// fill does not pay for scanline setup.
func (r *Rasterizer) fillDeviceRect(rect paint.Rect, c paint.Color) {
	area := intersectArea(areaOf(rect, r.w, r.h), r.state.clip.area)
	if area.empty() {
		return
	}
	for y := area.Y0; y < area.Y1; y++ {
		fy := spanOverlap(float32(y), float32(y+1), rect.Y, rect.Y+rect.Height)
		if fy <= 0 {
			continue
		}
		rowBase := y * r.w
		for x := area.X0; x < area.X1; x++ {
			fx := spanOverlap(float32(x), float32(x+1), rect.X, rect.X+rect.Width)
			if fx <= 0 {
				continue
			}
			a := uint32(fx*fy*255 + 0.5)
			if a == 0 {
				continue
			}
			if m := r.state.clip.mask; m != nil {
				a = a * uint32(m[rowBase+x]) / 255
				if a == 0 {
					continue
				}
			}
			r.blend(x, y, c, a)
		}
	}
}

// blendCoverage blends color c through a coverage buffer for area.
func (r *Rasterizer) blendCoverage(area pixArea, cov []float32, c paint.Color) {
	width := area.width()
	for y := area.Y0; y < area.Y1; y++ {
		rowOff := (y - area.Y0) * width
		rowBase := y * r.w
		for x := area.X0; x < area.X1; x++ {
			f := cov[rowOff+x-area.X0]
			if f <= 0 {
				continue
			}
			a := uint32(f*255 + 0.5)
			if a == 0 {
				continue
			}
			if m := r.state.clip.mask; m != nil {
				a = a * uint32(m[rowBase+x]) / 255
				if a == 0 {
					continue
				}
			}
			r.blend(x, y, c, a)
		}
	}
}

// blend composites a non-premultiplied color with 0..255 coverage a onto the
// premultiplied surface.
func (r *Rasterizer) blend(x, y int, c paint.Color, a uint32) {
	sa := a * uint32(c.A) / 255
	if sa == 0 {
		return
	}
	inv := 255 - sa
	i := r.img.PixOffset(x+r.ox, y+r.oy)
	p := r.img.Pix[i : i+4]

	p[0] = uint8((uint32(c.R)*sa + uint32(p[0])*inv + 127) / 255)
	p[1] = uint8((uint32(c.G)*sa + uint32(p[1])*inv + 127) / 255)
	p[2] = uint8((uint32(c.B)*sa + uint32(p[2])*inv + 127) / 255)
	p[3] = uint8((255*sa + uint32(p[3])*inv + 127) / 255)
}
