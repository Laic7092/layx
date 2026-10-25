package raster

import (
	"bytes"
	"testing"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render/paint"
)

var toneInk = paint.Color{A: 0xff}

// inkMass is the sum of a surface's darkness: how much ink a page carries, which
// is what a change of weight moves.
func inkMass(r *Rasterizer) int {
	pix := r.Image().Pix
	m := 0
	for i := 0; i+3 < len(pix); i += 4 {
		m += 255 - int(pix[i])
	}
	return m
}

func TestToneFixedPoints(t *testing.T) {
	inks := []paint.Color{
		toneInk,
		{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		{R: 0x40, G: 0x30, B: 0x20, A: 0xff},
	}
	for _, ink := range inks {
		for _, opt := range []ToneOptions{{}, {Grow: 0.1}, {Grow: 0.1, SyntheticBold: true}} {
			tone := NewTone(ink, opt)
			if got := tone.Coverage(0); got != 0 {
				t.Errorf("ink %v opt %+v: coverage 0 -> %d, want the paper left alone", ink, opt, got)
			}
			if got := tone.Coverage(255); got != 255 {
				t.Errorf("ink %v opt %+v: coverage 255 -> %d, want the ink left alone", ink, opt, got)
			}
		}
	}
}

func TestToneMonotone(t *testing.T) {
	tone := NewTone(toneInk, ToneOptions{Grow: 0.1})
	for a := 1; a < 256; a++ {
		if tone.Coverage(uint8(a)) < tone.Coverage(uint8(a-1)) {
			t.Fatalf("coverage %d mapped below %d: the curve must not invert", a, a-1)
		}
	}
}

// TestToneGrowth checks the two halves of the correction separately: the contrast
// lifts the middle of the curve, and the growth lifts it further. Pale ink gets no
// contrast — a night paper's text is already light on dark — so it stays put.
func TestToneGrowth(t *testing.T) {
	half := func(tone *Tone) uint8 { return tone.Coverage(128) }

	plain := half(NewTone(toneInk, ToneOptions{}))
	if plain <= 128 {
		t.Errorf("black at half coverage = %d, want the contrast curve above the raw 128", plain)
	}
	grown := half(NewTone(toneInk, ToneOptions{Grow: 0.1}))
	if grown <= plain {
		t.Errorf("growth at half coverage = %d, want above the contrast-only %d", grown, plain)
	}
	if bold := half(NewTone(toneInk, ToneOptions{Grow: 0.1, SyntheticBold: true})); bold != plain {
		t.Errorf("synthetic bold at half coverage = %d, want the contrast-only %d: a faked face is already wider", bold, plain)
	}
	// Pale ink keeps the growth and loses the contrast, so it lands between the
	// raw 128 and the black ink that gets both halves of the correction.
	pale := half(NewTone(paint.Color{R: 0xe8, G: 0xe8, B: 0xe8, A: 0xff}, ToneOptions{Grow: 0.1}))
	if pale <= 128 || pale >= grown {
		t.Errorf("pale ink at half coverage = %d, want above 128 and below black's %d: no contrast for light ink",
			pale, grown)
	}
}

func TestToneApplyMatchesCoverage(t *testing.T) {
	tone := NewTone(toneInk, ToneOptions{Grow: 0.1})
	cov := make([]uint8, 256)
	for i := range cov {
		cov[i] = uint8(i)
	}
	tone.Apply(cov)
	for i, v := range cov {
		if want := tone.Coverage(uint8(i)); v != want {
			t.Fatalf("Apply[%d] = %d, want %d", i, v, want)
		}
	}
	var nilTone *Tone
	nilTone.Apply(cov) // must not panic
	if cov[128] != tone.Coverage(128) {
		t.Fatal("a nil tone must leave coverage alone")
	}
}

// coverageOf draws gid through a Coverage exactly as a host's glyph atlas would:
// the outline in font units, mapped to the device grid, corrected by tone.
func coverageOf(t *testing.T, c *Coverage, data []byte, gid uint32, scale, x, y float32) []uint8 {
	t.Helper()
	face, err := font.ParseTTF(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	out, ok := face.GlyphDataOutline(font.GID(gid))
	if !ok {
		t.Fatalf("no outline for gid %d", gid)
	}
	pt := func(p ot.SegmentPoint) (float32, float32) {
		return x + p.X*scale, y - p.Y*scale
	}
	for i := range out.Segments {
		seg := &out.Segments[i]
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
	return c.Alpha()
}

// TestToneCoverageMatchesDrawGlyphs holds the two ways a host gets a corrected
// glyph to the same ink: Coverage for a host's own atlas, DrawGlyphs for a page.
// Same outline, same size, same place, so the bitmaps must be the same ones.
func TestToneCoverageMatchesDrawGlyphs(t *testing.T) {
	data := testFontData(t)
	gid := glyphID(t, data, 'H')
	face, err := font.ParseTTF(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse font: %v", err)
	}
	const size, x, y, w, h = 30, 20, 40, 120, 120
	scale := size / float32(face.Upem())
	tone := NewTone(toneInk, ToneOptions{Grow: 0.1})

	c := NewCoverage()
	c.Reset(w, h)
	c.SetTone(tone)
	fromCoverage := append([]uint8(nil), coverageOf(t, c, data, gid, scale, x, y)...)

	r := New(w, h)
	r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	r.ToneFor = func(paint.GlyphRun) *Tone { return tone }
	r.DrawGlyphs(paint.GlyphRun{
		Font:   &paint.Font{Family: "test", Data: data},
		Size:   size,
		Color:  toneInk,
		X:      x,
		Y:      y,
		Glyphs: []paint.Glyph{{ID: gid}},
	})

	pix := r.Image().Pix
	stride := r.Image().Stride
	var maxDiff int
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			a := int(fromCoverage[py*w+px])
			b := 255 - int(pix[py*stride+px*4])
			if d := absInt(a - b); d > maxDiff {
				maxDiff = d
			}
		}
	}
	if maxDiff > 2 {
		t.Fatalf("atlas coverage and DrawGlyphs differ by %d of 255: they must print the same ink", maxDiff)
	}
}

// TestDrawGlyphsToneWeighsMore checks the point of the correction end to end: the
// same run with the ink tone carries more ink than without, and a host that wants
// raw coverage gets exactly that by leaving ToneFor nil.
func TestDrawGlyphsToneWeighsMore(t *testing.T) {
	data := testFontData(t)
	gid := glyphID(t, data, 'H')
	run := paint.GlyphRun{
		Font:   &paint.Font{Family: "test", Data: data},
		Size:   24,
		Color:  toneInk,
		X:      20,
		Y:      60,
		Glyphs: []paint.Glyph{{ID: gid}},
	}

	draw := func(toneFor func(paint.GlyphRun) *Tone) int {
		r := New(120, 120)
		r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
		r.ToneFor = toneFor
		r.DrawGlyphs(run)
		return inkMass(r)
	}

	raw := draw(nil)
	if raw == 0 {
		t.Fatal("the raw run drew no ink")
	}
	if same := draw(func(paint.GlyphRun) *Tone { return nil }); same != raw {
		t.Fatalf("a ToneFor returning nil drew %d of ink, want the raw %d", same, raw)
	}
	tone := NewTone(toneInk, ToneOptions{Grow: 0.1})
	corrected := draw(func(paint.GlyphRun) *Tone { return tone })
	if corrected <= raw {
		t.Fatalf("corrected run carries %d of ink, want more than the raw %d", corrected, raw)
	}
	if ratio := float64(corrected) / float64(raw); ratio > 1.35 {
		t.Fatalf("corrected run carries %.2fx the raw ink, want a correction and not a blot", ratio)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
