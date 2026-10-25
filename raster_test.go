package raster

import (
	"image"
	"image/color"
	"testing"

	"github.com/laix/layx/render/paint"
)

func translate(dx, dy float32) paint.Affine {
	return paint.Affine{A: 1, D: 1, E: dx, F: dy}
}

func scale(sx, sy float32) paint.Affine {
	return paint.Affine{A: sx, D: sy}
}

func alphaAt(r *Rasterizer, x, y int) uint8 {
	return r.Image().RGBAAt(x, y).A
}

func colorAt(r *Rasterizer, x, y int) paint.Color {
	c := r.Image().RGBAAt(x, y)
	return paint.Color{R: c.R, G: c.G, B: c.B, A: c.A}
}

func TestFillRectOpaque(t *testing.T) {
	r := New(4, 4)
	r.FillRect(paint.Rect{X: 1, Y: 1, Width: 2, Height: 2}, paint.Color{R: 0xff, A: 0xff})

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			got := alphaAt(r, x, y)
			want := uint8(0)
			if x >= 1 && x < 3 && y >= 1 && y < 3 {
				want = 0xff
			}
			if got != want {
				t.Fatalf("alpha at (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestFillRectAlphaBlend(t *testing.T) {
	r := New(2, 2)
	r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	r.FillRect(paint.Rect{Width: 2, Height: 2}, paint.Color{A: 0x80})

	// Premultiplied white underneath, 128/255 black on top.
	c := r.Image().RGBAAt(0, 0)
	if c.A != 0xff {
		t.Fatalf("alpha = %d, want 255", c.A)
	}
	if c.R < 0x7d || c.R > 0x81 {
		t.Fatalf("red = %d, want about 127", c.R)
	}
}

func TestFillRectSubpixelCoverage(t *testing.T) {
	r := New(4, 2)
	r.FillRect(paint.Rect{X: 0.5, Y: 0, Width: 1, Height: 2}, paint.Color{A: 0xff})

	want := []uint8{0x80, 0x80, 0, 0}
	for x, w := range want {
		if got := alphaAt(r, x, 0); got != w {
			t.Fatalf("alpha at x=%d = %d, want %d", x, got, w)
		}
	}
}

func TestFillRectTransformed(t *testing.T) {
	r := New(8, 8)
	r.Transform(paint.Affine{A: 2, D: 2, E: 2, F: 1})
	r.FillRect(paint.Rect{Width: 2, Height: 2}, paint.Color{A: 0xff})

	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			in := x >= 2 && x < 6 && y >= 1 && y < 5
			if got := alphaAt(r, x, y) == 0xff; got != in {
				t.Fatalf("pixel (%d,%d) alpha %d, inside=%v", x, y, alphaAt(r, x, y), in)
			}
		}
	}
}

func TestTransformComposes(t *testing.T) {
	r := New(20, 20)
	r.Transform(translate(10, 0))
	r.Transform(translate(0, 5))
	r.FillRect(paint.Rect{Width: 2, Height: 2}, paint.Color{A: 0xff})

	if alphaAt(r, 9, 4) != 0 {
		t.Fatal("draw before the composed origin")
	}
	if alphaAt(r, 10, 5) != 0xff {
		t.Fatal("missing the composed origin")
	}
}

func TestSaveRestoreState(t *testing.T) {
	r := New(20, 20)
	r.Transform(translate(5, 0))
	r.Save()
	r.Transform(translate(5, 0))
	r.FillRect(paint.Rect{Width: 2, Height: 2}, paint.Color{A: 0xff})
	r.Restore()
	r.FillRect(paint.Rect{Y: 5, Width: 2, Height: 2}, paint.Color{A: 0xff})

	if alphaAt(r, 10, 0) != 0xff {
		t.Fatal("translated fill missing after save/restore")
	}
	if alphaAt(r, 5, 5) != 0xff {
		t.Fatal("restored transform not applied")
	}
	if alphaAt(r, 0, 0) != 0 {
		t.Fatal("transform was not restored")
	}
}

func TestClipRect(t *testing.T) {
	r := New(6, 6)
	r.Save()
	r.ClipRect(paint.Rect{X: 2, Y: 2, Width: 2, Height: 2})
	r.FillRect(paint.Rect{Width: 6, Height: 6}, paint.Color{A: 0xff})
	r.Restore()
	r.FillRect(paint.Rect{X: 0, Y: 4, Width: 2, Height: 2}, paint.Color{A: 0xff})

	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			in := (x >= 2 && x < 4 && y >= 2 && y < 4) || (x < 2 && y >= 4)
			if got := alphaAt(r, x, y) == 0xff; got != in {
				t.Fatalf("pixel (%d,%d) alpha %d, inside=%v", x, y, alphaAt(r, x, y), in)
			}
		}
	}
}

func TestClipRectIntersects(t *testing.T) {
	r := New(10, 10)
	r.ClipRect(paint.Rect{X: 0, Y: 0, Width: 6, Height: 10})
	r.ClipRect(paint.Rect{X: 4, Y: 0, Width: 6, Height: 10})
	r.FillRect(paint.Rect{Width: 10, Height: 10}, paint.Color{A: 0xff})

	for x := 0; x < 10; x++ {
		want := x >= 4 && x < 6
		if got := alphaAt(r, x, 5) == 0xff; got != want {
			t.Fatalf("x=%d in clip = %v, want %v", x, got, want)
		}
	}
}

func TestClipRectFractional(t *testing.T) {
	r := New(6, 2)
	r.ClipRect(paint.Rect{X: 1.5, Y: 0, Width: 3, Height: 2})
	r.FillRect(paint.Rect{Width: 6, Height: 2}, paint.Color{A: 0xff})

	want := []uint8{0, 0x80, 0xff, 0xff, 0x80, 0}
	for x, w := range want {
		if got := alphaAt(r, x, 0); got != w {
			t.Fatalf("alpha at x=%d = %d, want %d", x, got, w)
		}
	}
}

func TestClipRectRotated(t *testing.T) {
	r := New(40, 40)
	// Rotate the clip square by 45 degrees around (20,20), then fill the whole
	// surface: only the diamond survives.
	r.Transform(translate(20, 20))
	r.Transform(paint.Affine{A: 0.70710678, B: 0.70710678, C: -0.70710678, D: 0.70710678})
	r.ClipRect(paint.Rect{X: -10, Y: -10, Width: 20, Height: 20})
	r.FillRect(paint.Rect{X: -100, Y: -100, Width: 200, Height: 200}, paint.Color{A: 0xff})

	if alphaAt(r, 20, 20) != 0xff {
		t.Fatalf("center alpha = %d, want 255", alphaAt(r, 20, 20))
	}
	if alphaAt(r, 20, 10) != 0xff {
		t.Fatalf("inner pixel alpha = %d, want 255", alphaAt(r, 20, 10))
	}
	if alphaAt(r, 10, 10) != 0 {
		t.Fatalf("corner pixel alpha = %d, the rotated clip did not cut the corner", alphaAt(r, 10, 10))
	}
}

func TestClipMaskSurvivesRestore(t *testing.T) {
	r := New(8, 8)
	r.ClipRect(paint.Rect{X: 1.5, Y: 1.5, Width: 2, Height: 2})
	r.Save()
	r.ClipRect(paint.Rect{X: 2, Y: 2, Width: 1, Height: 1})
	r.Restore()
	r.FillRect(paint.Rect{Width: 8, Height: 8}, paint.Color{A: 0xff})

	if alphaAt(r, 4, 4) != 0 {
		t.Fatal("pixel outside the fractional clip is visible")
	}
	if alphaAt(r, 2, 2) != 0xff {
		t.Fatalf("pixel (2,2) alpha = %d, want a fully covered pixel", alphaAt(r, 2, 2))
	}
	if got := alphaAt(r, 3, 3); got < 60 || got > 68 {
		t.Fatalf("pixel (3,3) alpha = %d, want about 64 from the restored fractional clip", got)
	}
}

func TestVisibleFollowsTransform(t *testing.T) {
	r := New(200, 200)
	r.Transform(scale(2, 2))
	r.ClipRect(paint.Rect{X: 10, Y: 20, Width: 30, Height: 40})

	v := r.Visible()
	// The clip is 10..40 x 20..60 in user space; Visible is reported in the
	// painter's current space, padded by one device pixel (half a user unit
	// at this scale).
	if v.X < 9.4 || v.X > 10.1 || v.Y < 19.4 || v.Y > 20.1 {
		t.Fatalf("Visible origin = (%v,%v)", v.X, v.Y)
	}
	if v.X+v.Width < 40 || v.X+v.Width > 40.6 {
		t.Fatalf("Visible right edge = %v", v.X+v.Width)
	}
	if v.Y+v.Height < 60 || v.Y+v.Height > 60.6 {
		t.Fatalf("Visible bottom edge = %v", v.Y+v.Height)
	}
}

func TestVisibleEmptyAfterEmptyClip(t *testing.T) {
	r := New(10, 10)
	r.ClipRect(paint.Rect{})
	if v := r.Visible(); !v.Empty() {
		t.Fatalf("Visible = %+v, want empty", v)
	}
}

func TestFillRoundRect(t *testing.T) {
	r := New(20, 20)
	r.FillRoundRect(paint.Rect{X: 2, Y: 2, Width: 16, Height: 16}, 6, paint.Color{A: 0xff})

	if alphaAt(r, 2, 2) > 16 {
		t.Fatalf("corner alpha = %d, want a rounded corner", alphaAt(r, 2, 2))
	}
	if alphaAt(r, 10, 10) != 0xff {
		t.Fatal("center is not filled")
	}
	if alphaAt(r, 9, 3) != 0xff {
		t.Fatal("straight edge is not filled")
	}
}

func TestStrokeRect(t *testing.T) {
	r := New(20, 20)
	r.StrokeRect(paint.Rect{X: 2, Y: 2, Width: 16, Height: 16}, paint.Color{A: 0xff}, 2)

	if alphaAt(r, 2, 10) != 0xff {
		t.Fatalf("left edge alpha = %d", alphaAt(r, 2, 10))
	}
	if alphaAt(r, 9, 9) != 0 {
		t.Fatalf("center alpha = %d, want transparent", alphaAt(r, 9, 9))
	}
	if alphaAt(r, 0, 10) != 0 {
		t.Fatalf("outside alpha = %d", alphaAt(r, 0, 10))
	}
}

func TestDrawImageIdentity(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	src.SetNRGBA(1, 0, color.NRGBA{G: 0xff, A: 0xff})
	src.SetNRGBA(0, 1, color.NRGBA{B: 0xff, A: 0xff})
	src.SetNRGBA(1, 1, color.NRGBA{R: 0xff, G: 0xff, A: 0xff})

	r := New(4, 4)
	r.SetImage(7, src)
	r.DrawImage(7, paint.Rect{Width: 2, Height: 2}, paint.ImageOptions{})

	want := [4][4]uint8{
		{0xff, 0, 0, 0xff},    // red
		{0, 0xff, 0, 0xff},    // green
		{0, 0, 0xff, 0xff},    // blue
		{0xff, 0xff, 0, 0xff}, // yellow
	}
	got := [4]color.RGBA{
		r.Image().RGBAAt(0, 0), r.Image().RGBAAt(1, 0),
		r.Image().RGBAAt(0, 1), r.Image().RGBAAt(1, 1),
	}
	for i := range want {
		if got[i].R != want[i][0] || got[i].G != want[i][1] || got[i].B != want[i][2] || got[i].A != want[i][3] {
			t.Fatalf("texel %d = %+v, want %v", i, got[i], want[i])
		}
	}
	if alphaAt(r, 3, 3) != 0 {
		t.Fatal("image drew outside its destination rectangle")
	}
}

func TestDrawImageScaled(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{A: 0xff})

	r := New(4, 4)
	r.SetImage(1, src)
	r.DrawImage(1, paint.Rect{X: 1, Y: 1, Width: 2, Height: 2}, paint.ImageOptions{})

	if alphaAt(r, 1, 1) != 0xff || alphaAt(r, 2, 2) != 0xff {
		t.Fatal("scaled image did not cover its destination")
	}
	if alphaAt(r, 0, 0) != 0 || alphaAt(r, 3, 3) != 0 {
		t.Fatal("scaled image drew outside its destination")
	}
}

func TestUnknownImageAndEmptyOps(t *testing.T) {
	r := New(2, 2)
	r.DrawImage(99, paint.Rect{Width: 2, Height: 2}, paint.ImageOptions{})
	r.FillRect(paint.Rect{}, paint.Color{A: 0xff})
	r.StrokeRect(paint.Rect{Width: 2, Height: 2}, paint.Color{A: 0xff}, 0)
	if alphaAt(r, 0, 0) != 0 {
		t.Fatal("empty ops painted pixels")
	}
}

func TestClearAndReset(t *testing.T) {
	r := New(3, 3)
	r.Clear(paint.Color{R: 0xff, A: 0xff})
	if alphaAt(r, 0, 0) != 0xff {
		t.Fatal("Clear did not fill")
	}
	r.Transform(translate(100, 0))
	r.Reset()
	if alphaAt(r, 0, 0) != 0 {
		t.Fatal("Reset did not clear")
	}
	r.FillRect(paint.Rect{Width: 1, Height: 1}, paint.Color{A: 0xff})
	if alphaAt(r, 0, 0) != 0xff {
		t.Fatal("Reset did not restore the transform")
	}
}

func TestNewFromImageOffsets(t *testing.T) {
	img := image.NewRGBA(image.Rect(3, 7, 5, 9))
	r := NewFromImage(img)
	r.FillRect(paint.Rect{Width: 1, Height: 1}, paint.Color{A: 0xff})
	if img.RGBAAt(3, 7).A != 0xff {
		t.Fatal("drawing did not land at the image bounds origin")
	}
}

func TestColorAt(t *testing.T) {
	r := New(1, 1)
	r.FillRect(paint.Rect{Width: 1, Height: 1}, paint.Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff})
	c := colorAt(r, 0, 0)
	if c.R != 0x11 || c.G != 0x22 || c.B != 0x33 || c.A != 0xff {
		t.Fatalf("color = %+v", c)
	}
}
