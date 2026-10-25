package raster

import (
	"math"

	"github.com/laix/layx/render/paint"
)

// edge is one side of a contour, already in device space. dir is +1 for a
// downward edge and -1 for an upward one; the non-zero winding rule only needs
// consistent signs.
type edge struct {
	x0, y0, x1, y1 float32
	// ymin and ymax are y0 and y1 sorted; dxdy is dx/dy, so a row's start x
	// needs one multiply instead of a division.
	ymin, ymax float32
	dxdy       float32
	dir        int8
}

// shapeEdges flattens every contour of s into edges. Horizontal edges are
// skipped: an edge that does not cross a row deposits nothing in it.
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

// coverageScratch is the reusable working set of one rasterizer: alpha is the
// coverage of the last path, and cells holds the signed areas it is summed
// from, one width+2 row per pixel row, already in units of 255 so the sum can
// be stored as a byte without a scale factor.
//
// A row of cells is width+2: cell 0 collects every crossing left of the
// surface, so the winding entering the left edge still counts, cell 1..width
// are the pixels, and the last cell is a sink for crossings right of the
// surface, which no pixel inside it is left of.
type coverageScratch struct {
	alpha []uint8
	cells []float32
}

// rasterizeCoverage fills s.alpha with the fraction of each pixel covered by
// the non-zero winding of edges, one byte per pixel in row-major order.
//
// It integrates instead of sampling. Every edge deposits, for each pixel row it
// crosses, a signed area spread over the pixels that row's segment spans: the
// sum over a row is the row overlap dy times the edge's direction, and each
// pixel's part is the area of the segment's trapezoid inside it. Rows are then
// summed left to right, which turns those areas into the winding number at each
// pixel's left edge; its magnitude clamped to one is the pixel's coverage.
// Horizontal antialiasing is therefore exact, vertical antialiasing falls out of
// the row overlap, and nothing is supersampled.
//
// The work is proportional to the rows each edge crosses plus the surface once,
// not to the rows times the surface: a glyph's hundreds of edges touch only a
// few rows each.
//
// tone, when not nil, corrects each pixel's coverage on the way out (see Tone):
// the ink's weight is decided once per glyph instead of by a pass over the
// finished bitmap.
func rasterizeCoverage(edges []edge, area pixArea, s *coverageScratch, tone *Tone) []uint8 {
	width, height := area.width(), area.height()
	n := width * height
	if n <= 0 {
		s.alpha = s.alpha[:0]
		return nil
	}
	if cap(s.alpha) < n {
		s.alpha = make([]uint8, n)
	}
	alpha := s.alpha[:n]
	s.alpha = alpha
	if len(edges) == 0 {
		clear(alpha)
		return alpha
	}

	// base is the index of column area.X0-1 in a row, the carry cell.
	stride := width + 2
	if cap(s.cells) < stride*height {
		s.cells = make([]float32, stride*height)
	}
	cells := s.cells[:stride*height]
	s.cells = cells
	clear(cells)
	base := area.X0 - 1
	sink := stride - 1

	for i := range edges {
		e := &edges[i]

		y0 := clampRow(int(math.Floor(float64(e.ymin))), area.Y0, area.Y1)
		y1 := clampRow(int(math.Ceil(float64(e.ymax))), area.Y0, area.Y1)
		if y0 >= y1 {
			continue
		}

		// x is the edge's x where it enters the first visible row, which is the
		// edge's own start on the top row and the row's top after that.
		start := float32(y0)
		if start < e.ymin {
			start = e.ymin
		}
		x := e.x0 + (start-e.y0)*e.dxdy
		// Areas are 255 times the covered fraction, so a pixel's winding sum is
		// already the byte the coverage wants.
		dir := float32(e.dir) * 255
		line := cells[(y0-area.Y0)*stride:]

		for row := y0; row < y1; row++ {
			top := float32(row)
			dy := minf(top+1, e.ymax) - maxf(top, e.ymin)
			xNext := x + dy*e.dxdy
			deposit(line, base, sink, dy*dir, x, xNext)
			x = xNext
			line = line[stride:]
		}
	}

	// One row of this shape is the winding at each pixel's left edge, so the
	// row's cells are summed in place. Converting to bytes here, rather than
	// into a float coverage buffer for the caller to walk again, is one pass
	// over the pixels instead of two, and the tone rides along in the same pass.
	var lut *[256]uint8
	if tone != nil {
		lut = &tone.lut
	}
	for row := 0; row < height; row++ {
		line := cells[row*stride : row*stride+stride]
		out := alpha[row*width : row*width+width]
		acc := line[0]
		for x, v := range line[1 : 1+width] {
			acc += v
			// The magnitude of the winding, without a branch: clear the sign
			// bit. max(acc, -acc) is the same value but the builtin keeps the
			// NaN check, which costs a branch.
			a := math.Float32frombits(math.Float32bits(acc) & 0x7fffffff)
			if a > 255 {
				a = 255
			}
			v := uint8(a + 0.5)
			if lut != nil {
				v = lut[v]
			}
			out[x] = v
		}
	}
	return alpha
}

// deposit spreads d, the signed vertical overlap of a segment running from x to
// xNext inside one pixel row, over the pixels of that row. d is measured in
// coverage units of 255, matching the cells.
func deposit(line []float32, base, sink int, d, x, xNext float32) {
	x0, x1 := x, xNext
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	x0i := int(math.Floor(float64(x0)))
	x0Floor := float32(x0i)
	x1i := int(math.Ceil(float64(x1)))
	x1Ceil := float32(x1i)

	if x1i <= x0i+1 {
		// The segment stays within one pixel column: its area is split at its
		// mean x.
		xmf := 0.5*(x+xNext) - x0Floor
		line[cellIndex(x0i, base, sink)] += d - d*xmf
		line[cellIndex(x0i+1, base, sink)] += d * xmf
		return
	}

	// A trapezoid over several pixels: each one's part is the area between the
	// segment and the bottom edge of the row.
	sc := 1 / (x1 - x0)
	x0f := x0 - x0Floor
	a0 := 0.5 * sc * (1 - x0f) * (1 - x0f)
	x1f := x1 - x1Ceil + 1
	am := 0.5 * sc * x1f * x1f

	line[cellIndex(x0i, base, sink)] += d * a0
	if x1i == x0i+2 {
		line[cellIndex(x0i+1, base, sink)] += d * (1 - a0 - am)
	} else {
		a1 := sc * (1.5 - x0f)
		line[cellIndex(x0i+1, base, sink)] += d * (a1 - a0)

		// The pixels in between all get the same share. The loop is clipped to
		// the row, so a segment running far outside the surface still only
		// costs its visible part.
		dTimesS := d * sc
		from := base
		if x0i+2 > from {
			from = x0i + 2
		}
		to := sink
		if x1i-1 < to {
			to = x1i - 1
		}
		for xi := from; xi < to; xi++ {
			line[xi-base] += dTimesS
		}

		a2 := a1 + sc*float32(x1i-x0i-3)
		line[cellIndex(x1i-1, base, sink)] += d * (1 - a2 - am)
	}
	line[cellIndex(x1i, base, sink)] += d * am
}

// cellIndex maps a pixel column to its cell in a row: 0 for the carry, the
// pixel itself in between, and the row's sink for anything right of the
// surface.
func cellIndex(xi, base, sink int) int {
	j := xi - base
	if j < 0 {
		return 0
	}
	if j > sink {
		return sink
	}
	return j
}

func clampRow(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// edgesFor flattens s into the rasterizer's scratch slice.
func (r *Rasterizer) edgesFor(s *shape) []edge {
	r.edges = shapeEdges(s, r.edges[:0])
	return r.edges
}

// coverage rasterizes edges into the rasterizer's scratch slice, corrected for
// the ink's tone when tone is not nil.
func (r *Rasterizer) coverage(edges []edge, area pixArea, tone *Tone) []uint8 {
	return rasterizeCoverage(edges, area, &r.scratch, tone)
}

// fillShape rasterizes s with the non-zero winding rule and blends it into the
// surface through the current clip.
func (r *Rasterizer) fillShape(s *shape, c paint.Color, tone *Tone) {
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
	r.blendCoverage(area, r.coverage(r.edgesFor(s), area, tone), c)
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
func (r *Rasterizer) blendCoverage(area pixArea, cov []uint8, c paint.Color) {
	width := area.width()
	for y := area.Y0; y < area.Y1; y++ {
		rowOff := (y - area.Y0) * width
		rowBase := y * r.w
		for x := area.X0; x < area.X1; x++ {
			a := uint32(cov[rowOff+x-area.X0])
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
