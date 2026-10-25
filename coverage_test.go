package raster

import (
	"testing"
)

func TestCoverageRectSubpixel(t *testing.T) {
	c := NewCoverage()
	c.Reset(4, 2)
	c.MoveTo(0.5, 0)
	c.LineTo(1.5, 0)
	c.LineTo(1.5, 2)
	c.LineTo(0.5, 2)
	cov := c.Alpha()

	for y := 0; y < 2; y++ {
		row := cov[y*4 : y*4+4]
		want := []uint8{0x80, 0x80, 0, 0}
		for x, w := range want {
			if row[x] != w {
				t.Fatalf("row %d pixel %d = %d, want %d", y, x, row[x], w)
			}
		}
	}
}

func TestCoverageHole(t *testing.T) {
	c := NewCoverage()
	c.Reset(4, 4)
	// Outer contour clockwise in device space.
	c.MoveTo(0, 0)
	c.LineTo(4, 0)
	c.LineTo(4, 4)
	c.LineTo(0, 4)
	// Inner contour the other way: a hole under the non-zero rule.
	c.MoveTo(1, 1)
	c.LineTo(1, 3)
	c.LineTo(3, 3)
	c.LineTo(3, 1)
	cov := c.Alpha()

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			inner := x >= 1 && x < 3 && y >= 1 && y < 3
			got := cov[y*4+x]
			if inner && got != 0 {
				t.Fatalf("hole pixel (%d,%d) = %d, want 0", x, y, got)
			}
			if !inner && got != 0xff {
				t.Fatalf("ring pixel (%d,%d) = %d, want 255", x, y, got)
			}
		}
	}
}

func TestCoverageCurve(t *testing.T) {
	c := NewCoverage()
	const size = 20
	c.Reset(size, size)
	// A circle of radius 7 centred at (10,10), four cubic quarter arcs.
	r := float32(7)
	k := float32(0.5522847498307936 * 7)
	cx, cy := float32(10), float32(10)
	c.MoveTo(cx+r, cy)
	c.CubeTo(cx+r, cy+k, cx+k, cy+r, cx, cy+r)
	c.CubeTo(cx-k, cy+r, cx-r, cy+k, cx-r, cy)
	c.CubeTo(cx-r, cy-k, cx-k, cy-r, cx, cy-r)
	c.CubeTo(cx+k, cy-r, cx+r, cy-k, cx+r, cy)
	cov := c.Alpha()

	if got := cov[10*size+10]; got != 0xff {
		t.Fatalf("centre = %d, want 255", got)
	}
	if got := cov[0]; got != 0 {
		t.Fatalf("corner = %d, want 0", got)
	}
	// A circle is symmetric about both axes through its centre.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if a, b := cov[y*size+x], cov[y*size+(size-1-x)]; a != b {
				t.Fatalf("not left/right symmetric at (%d,%d): %d vs %d", x, y, a, b)
			}
			if a, b := cov[y*size+x], cov[(size-1-y)*size+x]; a != b {
				t.Fatalf("not top/bottom symmetric at (%d,%d): %d vs %d", x, y, a, b)
			}
		}
	}
}

func TestCoverageReuse(t *testing.T) {
	c := NewCoverage()
	c.Reset(4, 2)
	c.MoveTo(0, 0)
	c.LineTo(2, 0)
	c.LineTo(2, 2)
	c.LineTo(0, 2)
	if got := c.Alpha()[0]; got != 0xff {
		t.Fatalf("left rect pixel = %d, want 255", got)
	}

	c.Reset(4, 2)
	c.MoveTo(2, 0)
	c.LineTo(4, 0)
	c.LineTo(4, 2)
	c.LineTo(2, 2)
	cov := c.Alpha()
	if cov[0] != 0 {
		t.Fatalf("stale coverage at x=0: %d", cov[0])
	}
	if cov[3] != 0xff {
		t.Fatalf("right rect pixel = %d, want 255", cov[3])
	}
}

func TestCoverageEmpty(t *testing.T) {
	c := NewCoverage()
	c.Reset(2, 2)
	for i, v := range c.Alpha() {
		if v != 0 {
			t.Fatalf("pixel %d = %d with no path", i, v)
		}
	}
	c.Reset(0, 0)
	if cov := c.Alpha(); cov != nil {
		t.Fatalf("empty size returned %v", cov)
	}
}

// windingAt is the non-zero winding number of s at (x,y), the reference the
// rasterizer's coverage is checked against.
func windingAt(s *shape, x, y float32) int {
	w := 0
	for i := range s.starts {
		start, end := s.contourRange(i)
		for j := start; j < end; j++ {
			k := j + 1
			if k == end {
				k = start
			}
			a, b := s.pts[j], s.pts[k]
			if a.Y <= y {
				if b.Y > y && (b.X-a.X)*(y-a.Y)-(x-a.X)*(b.Y-a.Y) > 0 {
					w++
				}
			} else if b.Y <= y && (b.X-a.X)*(y-a.Y)-(x-a.X)*(b.Y-a.Y) < 0 {
				w--
			}
		}
	}
	return w
}

// TestCoverageMatchesSupersampling checks a few shapes against a brute-force
// 16x16 point sample per pixel. It is coarse on purpose: the rasterizer's
// vertical sampling is 8 sub-scanlines, so this test catches a wrong span,
// direction or winding, not the last bit of antialiasing.
func TestCoverageMatchesSupersampling(t *testing.T) {
	build := map[string]func(*Coverage){
		"triangle": func(c *Coverage) {
			c.MoveTo(2.5, 2)
			c.LineTo(20, 5.5)
			c.LineTo(5, 21)
		},
		"hole": func(c *Coverage) {
			c.MoveTo(2, 2)
			c.LineTo(20, 2)
			c.LineTo(20, 20)
			c.LineTo(2, 20)
			c.MoveTo(6, 6)
			c.LineTo(6, 14)
			c.LineTo(14, 14)
			c.LineTo(14, 6)
		},
		"circle": func(c *Coverage) {
			const r = 8
			k := float32(0.5522847498307936 * r)
			c.MoveTo(12+r, 12)
			c.CubeTo(12+r, 12+k, 12+k, 12+r, 12, 12+r)
			c.CubeTo(12-k, 12+r, 12-r, 12+k, 12-r, 12)
			c.CubeTo(12-r, 12-k, 12-k, 12-r, 12, 12-r)
			c.CubeTo(12+k, 12-r, 12+r, 12-k, 12+r, 12)
		},
	}

	const size = 24
	const ss = 16
	for name, fn := range build {
		t.Run(name, func(t *testing.T) {
			c := NewCoverage()
			c.Reset(size, size)
			fn(c)
			cov := append([]uint8(nil), c.Alpha()...)

			var totalDiff float64
			for py := 0; py < size; py++ {
				for px := 0; px < size; px++ {
					inside := 0
					for sy := 0; sy < ss; sy++ {
						for sx := 0; sx < ss; sx++ {
							x := float32(px) + (float32(sx)+0.5)/ss
							y := float32(py) + (float32(sy)+0.5)/ss
							if windingAt(&c.shape, x, y) != 0 {
								inside++
							}
						}
					}
					want := float64(inside) * 255 / (ss * ss)
					diff := float64(cov[py*size+px]) - want
					if diff < 0 {
						diff = -diff
					}
					totalDiff += diff
					if diff > 24 {
						t.Fatalf("pixel (%d,%d) = %d, supersampling says %.0f", px, py, cov[py*size+px], want)
					}
				}
			}
			if mean := totalDiff / (size * size); mean > 3 {
				t.Fatalf("mean difference %.2f of 255 from supersampling", mean)
			}
		})
	}
}
