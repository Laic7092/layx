package raster

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func buildTestLayout(t *testing.T, mode render.Mode) *render.Layout {
	t.Helper()
	data := testFontData(t)
	faces, err := render.NewFontSet(render.FontFace{
		Family: "serif", Weight: 400, Data: data,
	})
	if err != nil {
		t.Fatalf("NewFontSet: %v", err)
	}

	styles := []render.Style{
		{},
		{
			FontFamily:   "serif",
			FontSize:     16,
			LineHeight:   1.5,
			Color:        paint.Color{A: 0xff},
			MarginBottom: 8,
		},
	}
	nodes := []render.Node{
		{Kind: render.NodeBlock, Style: 0, Children: []render.NodeID{1, 2}},
		{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{3}},
		{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{4}},
		{Kind: render.NodeText, Style: 0, Text: strings.Repeat("The quick brown fox jumps over the lazy dog. ", 25)},
		{Kind: render.NodeText, Style: 0, Text: "A second paragraph shows wrapping and a baseline."},
	}
	b := render.NewDocumentBuilder().SetStyles(styles)
	if err := b.AddChapter(render.Chapter{ID: "c", Root: 0, Nodes: nodes}); err != nil {
		t.Fatalf("AddChapter: %v", err)
	}
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eng, err := render.New(render.Options{})
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	layout, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: 320, Height: 480},
		Mode:     mode,
		Typography: render.Typography{
			FontFamily: "serif",
			FontSize:   16,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   faces,
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	return layout
}

func pagedLayout(t *testing.T) *render.Layout {
	t.Helper()
	layout := buildTestLayout(t, render.ModePaged)
	if layout.PageCount() == 0 {
		t.Fatal("layout has no pages")
	}
	return layout
}

func nonWhitePixels(r *Rasterizer) int {
	pix := r.Image().Pix
	n := 0
	for i := 0; i+3 < len(pix); i += 4 {
		if pix[i+3] != 0 && (pix[i] != 0xff || pix[i+1] != 0xff || pix[i+2] != 0xff) {
			n++
		}
	}
	return n
}

func clearWhite(r *Rasterizer) {
	r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
}

func TestPageDraw(t *testing.T) {
	layout := pagedLayout(t)
	page := layout.Page(0)
	if page == nil {
		t.Fatal("Page(0) is nil")
	}

	w, h := int(page.Size().Width), int(page.Size().Height)
	r := New(w, h)
	clearWhite(r)
	page.Draw(r)

	ink := nonWhitePixels(r)
	if ink < 200 {
		t.Fatalf("page drew %d text pixels, want a readable page", ink)
	}

	// Drawing again must be pure replay.
	first := append([]byte(nil), r.Image().Pix...)
	clearWhite(r)
	page.Draw(r)
	if !bytes.Equal(first, r.Image().Pix) {
		t.Fatal("redrawing the same page produced different pixels")
	}

	path := filepath.Join(t.TempDir(), "page.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, r.Image()); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() < 1000 {
		t.Fatalf("png output looks empty: %v size=%v", err, st)
	}
}

// TestPageDrawSpread draws the page into the right half of a wider surface
// through a translate. If Visible() were reported in the wrong space, replay
// culling would drop the page's ops.
func TestPageDrawSpread(t *testing.T) {
	layout := pagedLayout(t)
	page := layout.Page(0)
	if page == nil {
		t.Fatal("Page(0) is nil")
	}

	w, h := int(page.Size().Width), int(page.Size().Height)
	r := New(2*w, h)
	clearWhite(r)
	r.Transform(translate(float32(w), 0))
	page.Draw(r)

	pix := r.Image().Pix
	left := 0
	right := 0
	stride := r.Image().Stride
	for y := 0; y < h; y++ {
		row := pix[y*stride : y*stride+w*4]
		for i := 0; i+3 < len(row); i += 4 {
			if row[i+3] != 0 && (row[i] != 0xff || row[i+1] != 0xff || row[i+2] != 0xff) {
				left++
			}
		}
		row = pix[y*stride+w*4 : (y+1)*stride]
		for i := 0; i+3 < len(row); i += 4 {
			if row[i+3] != 0 && (row[i] != 0xff || row[i+1] != 0xff || row[i+2] != 0xff) {
				right++
			}
		}
	}
	if left != 0 {
		t.Fatalf("translated page drew %d pixels in the left half", left)
	}
	if right < 200 {
		t.Fatalf("translated page drew %d pixels in the right half", right)
	}
}

func TestTileDraw(t *testing.T) {
	layout := buildTestLayout(t, render.ModeScroll)
	if layout.TileCount() == 0 {
		t.Fatal("scroll layout has no tiles")
	}
	tile := layout.Tile(0)
	if tile == nil {
		t.Fatal("Tile(0) is nil")
	}

	r := New(int(tile.Size().Width), int(tile.Size().Height))
	clearWhite(r)
	tile.Draw(r)
	if ink := nonWhitePixels(r); ink < 200 {
		t.Fatalf("tile drew %d text pixels, want a readable tile", ink)
	}
}
