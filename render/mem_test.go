package render

import (
	"context"
	"runtime"
	"testing"

	"github.com/laix/layx/render/paint"
)

// liveBytes returns the heap that is live after a collection.
func liveBytes() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestLayoutMemoryPerRune is the memory budget gate. The layout of a book must
// stay within a few bytes per rune: it stores line boxes, item tops and block
// boxes, never the text, never a shaped glyph and never a duplicate flow item.
//
// The budget is deliberately loose (it is a regression gate, not a benchmark);
// the measured value is logged so a change can be compared. Run with -v.
func TestLayoutMemoryPerRune(t *testing.T) {
	if testing.Short() {
		t.Skip("memory probe skipped in -short")
	}
	const runes = 1_000_000
	const budget = 6.0 // bytes per rune; the engine currently uses ~3.9

	doc, fs := cjkBenchDoc(t, runes)
	e, err := New(Options{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	opts := LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts: fs, Margins: paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
	}
	// Warm every allocation the engine keeps outside the layout (worker pool,
	// font faces, caches) with a first discarded layout, so the measurement is
	// the marginal cost of a layout.
	if _, err := e.Layout(context.Background(), doc, opts); err != nil {
		t.Fatal(err)
	}
	before := liveBytes()
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	after := liveBytes()
	if l.PageCount() == 0 {
		t.Fatal("no pages")
	}
	perRune := float64(after-before) / runes
	t.Logf("layout of %d runes: live +%d KB = %.2f B/rune (budget %.1f)", runes, (after-before)>>10, perRune, budget)
	if perRune > budget {
		t.Fatalf("layout uses %.2f B/rune, over the %.1f B/rune budget", perRune, budget)
	}
	runtime.KeepAlive(doc)
	runtime.KeepAlive(l)
}
