package render

import (
	"testing"

	"github.com/laix/layx/render/paint"
)

// clipPainter is a posPainter that also reports a visible region. It counts the
// calls that actually reached the backend so a test can tell culling apart
// from "the painter ignored it".
type clipPainter struct {
	posPainter
	visible               paint.Rect
	fills, images, glyphs int
}

func (p *clipPainter) Visible() paint.Rect { return p.visible }

func (p *clipPainter) FillRect(paint.Rect, paint.Color) { p.fills++ }

func (p *clipPainter) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) { p.images++ }

func (p *clipPainter) DrawGlyphs(run paint.GlyphRun) {
	p.glyphs++
	p.posPainter.DrawGlyphs(run)
}

func retainedRuns(ops []drawOp, visible paint.Rect) []paint.GlyphRun {
	var out []paint.GlyphRun
	for i := range ops {
		op := &ops[i]
		if op.kind != opGlyphs || !replayOp(op, visible) {
			continue
		}
		out = append(out, op.run)
	}
	return out
}

func TestPageDrawCullsOutsideVisible(t *testing.T) {
	l := testLayout(t, testDocument(t, ""), 1)
	if l.PageCount() == 0 {
		t.Fatal("test layout produced no pages")
	}
	p := l.Page(0)
	all := &posPainter{}
	p.Draw(all)
	if len(all.runs) < 10 {
		t.Fatalf("page has only %d glyph runs; test needs a full page", len(all.runs))
	}

	visible := paint.Rect{X: 0, Y: 0, Width: p.Size().Width, Height: p.Size().Height / 2}
	cp := &clipPainter{visible: visible}
	p.Draw(cp)

	want := retainedRuns(p.ops, visible)
	if cp.glyphs != len(want) {
		t.Fatalf("drew %d glyph runs, want %d", cp.glyphs, len(want))
	}
	if len(want) == 0 {
		t.Fatal("top half of the page kept no glyph runs")
	}
	if len(want) >= len(all.runs) {
		t.Fatalf("culling kept all %d runs; the test page is too short", len(want))
	}
	for i := range want {
		if cp.runs[i].X != want[i].X || cp.runs[i].Y != want[i].Y {
			t.Fatalf("run %d at (%.2f,%.2f), want (%.2f,%.2f)",
				i, cp.runs[i].X, cp.runs[i].Y, want[i].X, want[i].Y)
		}
	}
}

func TestEmptyVisibleDrawsNothing(t *testing.T) {
	l := testLayout(t, testDocument(t, ""), 1)
	p := l.Page(0)
	cp := &clipPainter{} // zero Rect means nothing is visible
	p.Draw(cp)
	if cp.glyphs != 0 || cp.fills != 0 || cp.images != 0 {
		t.Fatalf("empty visible region drew glyphs=%d fills=%d images=%d",
			cp.glyphs, cp.fills, cp.images)
	}
}

func TestFullVisibleMatchesUnclipped(t *testing.T) {
	l := testLayout(t, testDocument(t, ""), 1)
	p := l.Page(0)
	all := &posPainter{}
	p.Draw(all)
	full := &clipPainter{visible: paint.Rect{
		X: 0, Y: 0, Width: p.Size().Width, Height: p.Size().Height,
	}}
	p.Draw(full)
	if len(full.runs) != len(all.runs) {
		t.Fatalf("full visible drew %d runs, unclipped drew %d", len(full.runs), len(all.runs))
	}
}

func TestTileDrawCullsOutsideVisible(t *testing.T) {
	doc := multiChapterDoc(t, 3)
	l := layoutOptions(t, mustEngine(t, 1), doc, scrollOptions(t, ModeScroll, 300))
	if l.TileCount() < 2 {
		t.Fatalf("test layout produced %d tiles; need at least 2", l.TileCount())
	}
	tile := l.Tile(1)
	all := &posPainter{}
	tile.Draw(all)
	if len(all.runs) == 0 {
		t.Fatal("tile has no glyph runs")
	}

	visible := paint.Rect{X: 0, Y: 150, Width: tile.Size().Width, Height: 150}
	cp := &clipPainter{visible: visible}
	tile.Draw(cp)

	want := retainedRuns(tile.ops, visible)
	if cp.glyphs != len(want) {
		t.Fatalf("drew %d glyph runs, want %d", cp.glyphs, len(want))
	}
	if len(want) == 0 || len(want) >= len(all.runs) {
		t.Fatalf("culling kept %d of %d runs; want a strict subset", len(want), len(all.runs))
	}
}

func TestGlyphOpsHaveFootprints(t *testing.T) {
	l := testLayout(t, testDocument(t, ""), 1)
	for n := 0; n < l.PageCount(); n++ {
		p := l.Page(n)
		for i := range p.ops {
			if op := &p.ops[i]; op.kind == opGlyphs && op.rect.Empty() {
				t.Fatalf("page %d op %d is a glyph op without a rectangle", n, i)
			}
		}
	}
}

func TestTileHeight(t *testing.T) {
	doc := multiChapterDoc(t, 1)

	// Zero resolves to the viewport height, and TileHeight reports the resolved
	// value rather than the option the caller passed.
	opts := scrollOptions(t, ModeScroll, 0)
	l := layoutOptions(t, mustEngine(t, 1), doc, opts)
	if got, want := l.TileHeight(), opts.Viewport.Height; got != want {
		t.Fatalf("TileHeight() = %v, want %v", got, want)
	}

	opts.TileHeight = 97
	l = layoutOptions(t, mustEngine(t, 1), doc, opts)
	if got := l.TileHeight(); got != 97 {
		t.Fatalf("TileHeight() = %v, want 97", got)
	}

	paged := layoutOptions(t, mustEngine(t, 1), doc, scrollOptions(t, ModePaged, 0))
	if got := paged.TileHeight(); got != 0 {
		t.Fatalf("paged TileHeight() = %v, want 0", got)
	}
}
