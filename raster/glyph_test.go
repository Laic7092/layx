package raster

import (
	"bytes"
	"math"
	"os"
	"testing"

	"github.com/go-text/typesetting/font"
	"github.com/laic7092/layx/render/paint"
)

func testFontData(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		t.Skipf("test font unavailable: %v", err)
	}
	return data
}

func glyphID(t testing.TB, data []byte, r rune) uint32 {
	t.Helper()
	face, err := font.ParseTTF(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	gid, ok := face.NominalGlyph(r)
	if !ok {
		t.Fatalf("font has no glyph for %q", r)
	}
	return uint32(gid)
}

func drawTestGlyph(t *testing.T, data []byte, size, x, y float32, bold bool, m paint.Affine) *Rasterizer {
	t.Helper()
	r := New(200, 200)
	r.Transform(m)
	r.DrawGlyphs(paint.GlyphRun{
		Font: &paint.Font{Family: "test", Data: data},
		Size: size,
		Color: paint.Color{
			A: 0xff,
		},
		X:           x,
		Y:           y,
		EmulateBold: bold,
		Glyphs: []paint.Glyph{{
			ID: glyphID(t, data, 'H'),
		}},
	})
	return r
}

// ink returns the number of non-transparent pixels and the total alpha, so a
// test can compare shape weight without depending on a golden image.
func ink(r *Rasterizer) (pixels int, alpha int) {
	pix := r.Image().Pix
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 0 {
			pixels++
			alpha += int(pix[i])
		}
	}
	return pixels, alpha
}

func TestDrawGlyph(t *testing.T) {
	data := testFontData(t)
	r := drawTestGlyph(t, data, 96, 20, 150, false, paint.Identity())

	pixels, _ := ink(r)
	if pixels < 1000 {
		t.Fatalf("glyph covered %d pixels, want an 'H' at 96px", pixels)
	}
	if alphaAt(r, 0, 0) != 0 {
		t.Fatal("glyph drew at the surface origin")
	}
}

func TestDrawGlyphIsDeterministic(t *testing.T) {
	data := testFontData(t)
	a := drawTestGlyph(t, data, 33.3, 20.25, 100.5, false, paint.Identity())
	b := drawTestGlyph(t, data, 33.3, 20.25, 100.5, false, paint.Identity())
	if !bytes.Equal(a.Image().Pix, b.Image().Pix) {
		t.Fatal("two identical draws produced different pixels")
	}
}

func TestDrawGlyphBold(t *testing.T) {
	data := testFontData(t)
	normal := drawTestGlyph(t, data, 64, 20, 120, false, paint.Identity())
	bold := drawTestGlyph(t, data, 64, 20, 120, true, paint.Identity())

	np, na := ink(normal)
	bp, ba := ink(bold)
	if bp <= np || ba <= na {
		t.Fatalf("bold glyph is not heavier: normal=%d/%d bold=%d/%d", np, na, bp, ba)
	}
}

func TestDrawGlyphRotated(t *testing.T) {
	data := testFontData(t)
	angle := float32(math.Pi / 6)
	m := paint.Affine{
		A: float32(math.Cos(float64(angle))),
		B: float32(math.Sin(float64(angle))),
		C: -float32(math.Sin(float64(angle))),
		D: float32(math.Cos(float64(angle))),
		E: 150,
		F: 40,
	}
	r := drawTestGlyph(t, data, 48, 0, 0, false, m)
	if pixels, _ := ink(r); pixels < 300 {
		t.Fatalf("rotated glyph covered %d pixels", pixels)
	}
}

func TestDrawGlyphsMissingFontAndEmptyRun(t *testing.T) {
	r := New(10, 10)
	r.DrawGlyphs(paint.GlyphRun{Size: 10, Color: paint.Color{A: 0xff}})
	r.DrawGlyphs(paint.GlyphRun{
		Font:   &paint.Font{},
		Size:   10,
		Color:  paint.Color{A: 0xff},
		Glyphs: []paint.Glyph{{ID: 1}},
	})
	if pixels, _ := ink(r); pixels != 0 {
		t.Fatalf("empty runs drew %d pixels", pixels)
	}
}

func TestGlyphFaceIndexFallback(t *testing.T) {
	data := testFontData(t)
	// Index 9 does not exist in a single-face font; the rasterizer falls back
	// to face 0 instead of failing.
	r := New(100, 100)
	r.DrawGlyphs(paint.GlyphRun{
		Font: &paint.Font{Data: data, Index: 9},
		Size: 40,
		X:    10,
		Y:    70,
		Color: paint.Color{
			A: 0xff,
		},
		Glyphs: []paint.Glyph{{ID: glyphID(t, data, 'A')}},
	})
	if pixels, _ := ink(r); pixels == 0 {
		t.Fatal("font index fallback drew nothing")
	}
}

func BenchmarkDrawGlyphs(b *testing.B) {
	data, err := os.ReadFile("testdata/DejaVuSerif.ttf")
	if err != nil {
		b.Skipf("test font unavailable: %v", err)
	}
	id := glyphID(b, data, 'H')
	f := &paint.Font{Family: "test", Data: data}
	run := paint.GlyphRun{
		Font: f, Size: 18, Color: paint.Color{A: 0xff}, X: 10, Y: 20,
		Glyphs: make([]paint.Glyph, 40),
	}
	for i := range run.Glyphs {
		run.Glyphs[i] = paint.Glyph{ID: id, X: float32(i) * 11}
	}
	r := New(480, 40)
	b.ReportAllocs()
	for b.Loop() {
		r.Reset()
		r.DrawGlyphs(run)
	}
}
