package raster

import "github.com/laix/layx/render/paint"

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
	dir            int8
}

type crossing struct {
	x   float32
	dir int8
}

// edgesFor flattens every contour of s into edges, reusing the rasterizer's
// scratch slice.
func (r *Rasterizer) edgesFor(s *shape) []edge {
	edges := r.edges[:0]
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
				continue // horizontal edges never cross a scanline
			}
			e := edge{x0: a.X, y0: a.Y, x1: b.X, y1: b.Y, dir: 1}
			if b.Y < a.Y {
				e.dir = -1
			}
			edges = append(edges, e)
		}
	}
	r.edges = edges
	return edges
}

// coverage returns the fraction of each pixel in area covered by the non-zero
// winding of edges, one value per pixel in row-major order.
func (r *Rasterizer) coverage(edges []edge, area pixArea) []float32 {
	n := area.width() * area.height()
	if cap(r.cov) < n {
		r.cov = make([]float32, n)
	}
	cov := r.cov[:n]
	for i := range cov {
		cov[i] = 0
	}
	if len(edges) == 0 {
		return cov
	}

	const weight = 1 / float32(coverageSamples)
	width := area.width()
	cross := r.cross[:0]

	for row := area.Y0; row < area.Y1; row++ {
		rowOff := (row - area.Y0) * width
		rowCov := cov[rowOff : rowOff+width]
		for sample := 0; sample < coverageSamples; sample++ {
			y := float32(row) + (float32(sample)+0.5)*weight
			cross = cross[:0]
			for i := range edges {
				e := &edges[i]
				y0, y1 := e.y0, e.y1
				if y0 > y1 {
					y0, y1 = y1, y0
				}
				if y < y0 || y >= y1 {
					continue
				}
				x := e.x0 + (y-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
				cross = append(cross, crossing{x: x, dir: e.dir})
			}
			if len(cross) < 2 {
				continue
			}
			sortCrossings(cross)
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
	r.cross = cross[:0]
	return cov
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
