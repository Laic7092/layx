package render

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/laic7092/layx/render/paint"
)

func scrollBenchOptions(fs *FontSet, mode Mode) LayoutOptions {
	return LayoutOptions{
		Viewport: Viewport{Width: 600, Height: 900},
		Typography: Typography{
			FontFamily: "CJK", FontSize: 18, LineHeight: 1.6,
			Color: paint.Color{A: 0xff},
		},
		Fonts:      fs,
		Margins:    paint.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
		Mode:       mode,
		TileHeight: 900,
	}
}

func benchScrollLayout(b *testing.B, totalRunes, workers int) {
	doc, fs := cjkBenchDoc(b, totalRunes)
	e, err := New(Options{Workers: workers, PageCache: 4, TileCache: 8, LineCache: 256})
	if err != nil {
		b.Fatal(err)
	}
	opts := scrollBenchOptions(fs, ModeScroll)
	b.SetBytes(int64(totalRunes))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := e.Layout(context.Background(), doc, opts)
		if err != nil {
			b.Fatal(err)
		}
		if l.TileCount() == 0 {
			b.Fatal("no tiles")
		}
	}
}

// BenchmarkScrollLayout5M is the acceptance benchmark for the whole-book
// scroll layout: the flow is the same as paged mode, so this should be close
// to BenchmarkLayout5M (paging is O(lines) on top of the same break cache).
func BenchmarkScrollLayout5M(b *testing.B) {
	b.Run("workers=1", func(b *testing.B) { benchScrollLayout(b, 5_000_000, 1) })
	b.Run("workers=8", func(b *testing.B) { benchScrollLayout(b, 5_000_000, 8) })
}

func BenchmarkScrollLayout200K(b *testing.B) {
	benchScrollLayout(b, 200_000, 8)
}

func tileBenchSetup(b *testing.B, runes int) (*Layout, LayoutOptions, *Engine) {
	b.Helper()
	doc, fs := cjkBenchDoc(b, runes)
	e, err := New(Options{Workers: 8, PageCache: 4, TileCache: 8, LineCache: 256})
	if err != nil {
		b.Fatal(err)
	}
	opts := scrollBenchOptions(fs, ModeScroll)
	opts.Viewport = Viewport{Width: 640, Height: 900}
	l, err := e.Layout(context.Background(), doc, opts)
	if err != nil {
		b.Fatal(err)
	}
	if l.TileCount() < 200 && runes >= 200_000 {
		b.Fatalf("only %d tiles, need at least 200 for the benchmark", l.TileCount())
	}
	return l, opts, e
}

// BenchmarkTileCold640x900 measures a cold tile build: a fresh Layout (empty
// tile and line caches) is created with Relayout, then one tile is built. The
// timer only covers Tile, and p50/p95 are reported as custom metrics.
func BenchmarkTileCold640x900(b *testing.B) {
	l, opts, _ := tileBenchSetup(b, 200_000)
	ctx := context.Background()
	durs := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		fresh, err := l.Relayout(ctx, opts)
		if err != nil {
			b.Fatal(err)
		}
		idx := i % fresh.TileCount()
		b.StartTimer()
		start := time.Now()
		if fresh.Tile(idx) == nil {
			b.Fatalf("tile %d is nil", idx)
		}
		durs = append(durs, time.Since(start))
		b.StopTimer()
	}
	b.StopTimer()
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	if len(durs) > 0 {
		p50 := durs[len(durs)*50/100]
		p95 := durs[len(durs)*95/100]
		b.ReportMetric(float64(p50.Nanoseconds()), "p50-ns/tile")
		b.ReportMetric(float64(p95.Nanoseconds()), "p95-ns/tile")
	}
}

// BenchmarkTileCached measures the tile cache hit path.
func BenchmarkTileCached(b *testing.B) {
	l, _, _ := tileBenchSetup(b, 200_000)
	l.Tile(5)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if l.Tile(5) == nil {
			b.Fatal("cached tile is nil")
		}
	}
}

// BenchmarkTiles200 averages the build cost of 200 consecutive tiles from a
// fresh Layout. Relayout is outside the timer; the line cache starts empty,
// which matches the first scroll of a new layout variant.
func BenchmarkTiles200(b *testing.B) {
	l, opts, _ := tileBenchSetup(b, 500_000)
	ctx := context.Background()
	var total time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		fresh, err := l.Relayout(ctx, opts)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		start := time.Now()
		for k := 0; k < 200; k++ {
			if fresh.Tile(k) == nil {
				b.Fatalf("tile %d is nil", k)
			}
		}
		total += time.Since(start)
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(total.Nanoseconds())/float64(b.N*200), "ns/tile")
	}
}
