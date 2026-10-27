package raster

import (
	"bytes"
	"math"
	"os"
	"testing"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// supersample is the ground truth a coverage is measured against: ss by ss point
// samples of the flattened contours in every pixel, which converges to the area
// the rasterizer integrates as ss grows.
func supersample(s *shape, w, h, ss int) []uint8 {
	out := make([]uint8, w*h)
	inv := 1.0 / float32(ss)
	step := 255 / float32(ss*ss)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var hits float32
			for j := 0; j < ss; j++ {
				py := float32(y) + inv*(float32(j)+0.5)
				for i := 0; i < ss; i++ {
					px := float32(x) + inv*(float32(i)+0.5)
					if windingAt(s, px, py) != 0 {
						hits++
					}
				}
			}
			out[y*w+x] = uint8(hits * step)
		}
	}
	return out
}

// TestGlyphFidelity holds real glyph coverage to how far it is from the area it
// approximates, at a size where the difference is visible: the same measure
// FreeType's own grayscale rasterizer scores 0.12/255 on at 34 px (see
// reference/freetype), so anything near that is a rasterizer whose edges are as
// good as the platform's, and the exact-area integration is what gets it to
// 0.08.
func TestGlyphFidelity(t *testing.T) {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		t.Skip(err)
	}
	faces, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	face := faces[0]
	scale := float32(34) / float32(face.Upem())

	const (
		w, h = 64, 64
		pen  = 12
		ss   = 16
	)
	var cov Coverage
	var sumErr, sumInk, sumTruth float64
	var n int
	for _, r := range []rune("AaBbGgOoQqRrSsWw&0123456789$@%") {
		gid, _ := face.NominalGlyph(r)
		out, ok := face.GlyphDataOutline(gid)
		if !ok {
			continue
		}
		cov.Reset(w, h)
		build := func(c *Coverage) {
			pt := func(p ot.SegmentPoint) (float32, float32) {
				return float32(pen) + float32(p.X)*scale, float32(pen) - float32(p.Y)*scale
			}
			for _, seg := range out.Segments {
				args := seg.ArgsSlice()
				switch seg.Op {
				case ot.SegmentOpMoveTo:
					c.MoveTo(pt(args[0]))
				case ot.SegmentOpLineTo:
					c.LineTo(pt(args[0]))
				case ot.SegmentOpQuadTo:
					bx, by := pt(args[0])
					cx, cy := pt(args[1])
					c.QuadTo(bx, by, cx, cy)
				case ot.SegmentOpCubeTo:
					bx, by := pt(args[0])
					cx, cy := pt(args[1])
					dx, dy := pt(args[2])
					c.CubeTo(bx, by, cx, cy, dx, dy)
				}
			}
		}
		build(&cov)
		got := cov.Alpha()
		want := supersample(&cov.shape, w, h, ss)
		for i := range want {
			sumErr += math.Abs(float64(got[i]) - float64(want[i]))
			sumInk += float64(got[i])
			sumTruth += float64(want[i])
		}
		n += len(want)
	}
	if n == 0 {
		t.Fatal("no glyphs")
	}
	meanErr := sumErr / float64(n)
	if meanErr > 0.35 {
		t.Errorf("coverage is %.2f/255 from the area it integrates, want under 0.35", meanErr)
	}
	if ratio := sumInk / sumTruth; ratio < 0.99 || ratio > 1.01 {
		t.Errorf("coverage carries %.4f of the ink the area does, want within 1%%", ratio)
	}
	t.Logf("%d px: mean |err| %.3f/255, ink %.4f of the area", n, meanErr, sumInk/sumTruth)
}
