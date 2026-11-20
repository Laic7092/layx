package render

import (
	"context"
	"testing"
	"time"

	"github.com/laic7092/layx/render/paint"
)

func statsTestOptions(fs *FontSet) LayoutOptions {
	return LayoutOptions{
		Viewport: Viewport{Width: 420, Height: 600},
		Typography: Typography{
			FontFamily: "Test Serif",
			FontSize:   16,
			LineHeight: 1.5,
			Color:      paint.Color{A: 0xff},
		},
		Fonts:   fs,
		Margins: paint.Insets{Top: 24, Right: 24, Bottom: 24, Left: 24},
	}
}

func TestEngineStats(t *testing.T) {
	doc := testDocument(t, "")
	fs := testFontSet(t)
	opts := statsTestOptions(fs)
	ctx := context.Background()

	// Measure off: no clock reads, no phase timings.
	off := mustEngine(t, 2)
	if _, err := off.Layout(ctx, doc, opts); err != nil {
		t.Fatalf("Layout (measure off): %v", err)
	}
	if got := off.Stats(); got.Layouts != 0 || got.Total != 0 || got.Keys != 0 || got.Break != 0 {
		t.Fatalf("Stats reported timings with Measure off: %+v", got)
	}

	on, err := New(Options{Workers: 2, Measure: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	layout, err := on.Layout(ctx, doc, opts)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	s := on.Stats()
	if s.Layouts != 1 {
		t.Fatalf("Layouts = %d, want 1", s.Layouts)
	}
	if s.Shapes == 0 {
		t.Fatalf("Shapes = 0, want > 0")
	}
	var stageSum time.Duration
	for name, d := range map[string]time.Duration{
		"Total":    s.Total,
		"Keys":     s.Keys,
		"Break":    s.Break,
		"Stack":    s.Stack,
		"Paginate": s.Paginate,
		"Assemble": s.Assemble,
	} {
		if d <= 0 {
			t.Fatalf("phase %s = %v, want > 0 (%+v)", name, d, s)
		}
		if name != "Total" {
			stageSum += d
		}
	}
	if stageSum > s.Total {
		t.Fatalf("stage sum %v exceeds Total %v", stageSum, s.Total)
	}
	if len(s.Busy) != on.Workers() {
		t.Fatalf("Busy has %d slots, want %d", len(s.Busy), on.Workers())
	}
	if s.Imbalance < 0 || s.Imbalance > 1 {
		t.Fatalf("Imbalance = %v, want [0,1]", s.Imbalance)
	}
	if s.Tasks == 0 {
		t.Fatal("Tasks = 0, want > 0")
	}

	// Relayout counts as another layout and must not build display lists.
	if _, err := layout.Relayout(ctx, opts); err != nil {
		t.Fatalf("Relayout: %v", err)
	}
	if got := on.Stats().Layouts; got != 2 {
		t.Fatalf("Layouts after Relayout = %d, want 2", got)
	}
	if got := on.Stats().Pages; got != 0 {
		t.Fatalf("pages built during Layout: %d", got)
	}

	// Display lists are built lazily and timed separately.
	before := on.Stats().Display
	if layout.Page(0) == nil {
		t.Fatal("Page(0) = nil")
	}
	s = on.Stats()
	if s.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", s.Pages)
	}
	if s.Display <= before {
		t.Fatalf("Display did not grow: %v -> %v", before, s.Display)
	}

	// Scroll mode times tile construction.
	on.ResetStats()
	scroll := opts
	scroll.Mode = ModeScroll
	sl, err := on.Layout(ctx, doc, scroll)
	if err != nil {
		t.Fatalf("scroll Layout: %v", err)
	}
	if sl.TileCount() == 0 {
		t.Fatal("TileCount = 0")
	}
	if sl.Tile(0) == nil {
		t.Fatal("Tile(0) = nil")
	}
	s = on.Stats()
	if s.Tiles != 1 || s.Display <= 0 {
		t.Fatalf("tile stats: %+v", s)
	}
	if s.Layouts != 1 || s.Shapes == 0 {
		t.Fatalf("layout stats after reset: %+v", s)
	}

	on.ResetStats()
	s = on.Stats()
	if s.Layouts != 0 || s.Total != 0 || s.Shapes != 0 || s.Tasks != 0 || s.Pages != 0 || s.Tiles != 0 {
		t.Fatalf("ResetStats left counters: %+v", s)
	}
	if s.Imbalance != 0 {
		t.Fatalf("ResetStats left balance counters: %+v", s)
	}
	for i, d := range s.Busy {
		if d != 0 {
			t.Fatalf("ResetStats left Busy[%d] = %v", i, d)
		}
	}
}
