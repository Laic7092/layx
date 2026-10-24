package render

import (
	"sync/atomic"
	"time"
)

// Stats is a snapshot of an Engine's cumulative phase timings.
//
// Only work performed with Options.Measure enabled is timed; Shapes is the
// exception and is always counted. Timings are wall-clock sums over every
// Layout and Relayout call since New or the last ResetStats.
//
// Total covers one whole Layout call. Keys, Break, Stack, Paginate and Assemble
// are its stages and together account for most of Total. Display is lazy
// Page/Tile display-list construction, which happens outside Layout, so it is
// reported separately and is not part of Total.
//
// Stats reads each counter independently: a snapshot taken while another
// goroutine is laying out may mix slightly different moments. Use it for
// benchmarks and diagnostics, not for control flow.
type Stats struct {
	// Layouts is the number of Layout and Relayout calls that passed option
	// validation and reached the keying stage.
	Layouts int64
	// Total is the wall time spent inside Engine.Layout and Layout.Relayout.
	Total time.Duration
	// Keys is the time spent deriving BreakKey, PageKey and PaintKey. It walks
	// the document but does no shaping.
	Keys time.Duration
	// Break is the time spent tokenizing, shaping, line breaking and scheduling
	// the parallel workers for chapters whose BreakKey missed.
	Break time.Duration
	// Stack is the time spent stacking cached line boxes (line height, paragraph
	// gap, margins) and resolving node and anchor placements.
	Stack time.Duration
	// Paginate is the time spent slicing the stacked flow into pages.
	Paginate time.Duration
	// Assemble is the time spent building chapter flows, merging anchors and
	// initializing scroll coordinates.
	Assemble time.Duration
	// Display is the time spent lazily building Page and Tile display lists.
	Display time.Duration
	// Shapes is the number of shaping calls, counted even when Measure is off.
	Shapes int64
	// Pages and Tiles are the number of display lists built so far. A build
	// raced by two goroutines counts twice.
	Pages int64
	Tiles int64
}

type statsCounters struct {
	layouts  atomic.Int64
	total    atomic.Int64
	keys     atomic.Int64
	brk      atomic.Int64
	stack    atomic.Int64
	paginate atomic.Int64
	assemble atomic.Int64
	display  atomic.Int64
	pages    atomic.Int64
	tiles    atomic.Int64
}

func (s *statsCounters) reset() {
	s.layouts.Store(0)
	s.total.Store(0)
	s.keys.Store(0)
	s.brk.Store(0)
	s.stack.Store(0)
	s.paginate.Store(0)
	s.assemble.Store(0)
	s.display.Store(0)
	s.pages.Store(0)
	s.tiles.Store(0)
}

// Stats returns a snapshot of the Engine's counters.
func (e *Engine) Stats() Stats {
	return Stats{
		Layouts:  e.stats.layouts.Load(),
		Total:    time.Duration(e.stats.total.Load()),
		Keys:     time.Duration(e.stats.keys.Load()),
		Break:    time.Duration(e.stats.brk.Load()),
		Stack:    time.Duration(e.stats.stack.Load()),
		Paginate: time.Duration(e.stats.paginate.Load()),
		Assemble: time.Duration(e.stats.assemble.Load()),
		Display:  time.Duration(e.stats.display.Load()),
		Shapes:   e.shapes.Load(),
		Pages:    e.stats.pages.Load(),
		Tiles:    e.stats.tiles.Load(),
	}
}

// ResetStats zeroes every counter, including Shapes. Call it between layouts
// rather than concurrently with Engine.Layout if you want a clean measurement
// window.
func (e *Engine) ResetStats() {
	e.stats.reset()
	e.shapes.Store(0)
}

// mark starts a phase timer. It returns the zero time when timing is off, so
// stop becomes a no-op and the hot path performs no clock read.
func (e *Engine) mark() time.Time {
	if !e.measure {
		return time.Time{}
	}
	return time.Now()
}

// stop records the elapsed time since start into c. A zero start (timing off)
// is ignored.
func (e *Engine) stop(start time.Time, c *atomic.Int64) {
	if start.IsZero() {
		return
	}
	c.Add(int64(time.Since(start)))
}

// notePage records one lazily built page display list. It is a defer target so
// the call site stays a single line.
func (e *Engine) notePage(start time.Time) {
	e.stats.display.Add(int64(time.Since(start)))
	e.stats.pages.Add(1)
}

// noteTile is notePage for scroll-mode tiles.
func (e *Engine) noteTile(start time.Time) {
	e.stats.display.Add(int64(time.Since(start)))
	e.stats.tiles.Add(1)
}
