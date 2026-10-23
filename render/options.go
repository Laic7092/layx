package render

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/laix/layx/render/paint"
)

// Options configures an Engine.
type Options struct {
	// Workers is the maximum number of layout workers. 0 means GOMAXPROCS.
	Workers int
	// PageCache is the number of rendered pages cached per Layout. 0 uses 4.
	PageCache int
	// TileCache is the number of scroll-mode tiles cached per Layout. 0 uses 8.
	TileCache int
	// LineCache is the maximum number of shaped lines retained per Layout.
	// 0 uses 256. The cache is bounded, so it never grows with book size.
	LineCache int
	Limits    Limits
}

// Limits bounds the resources consumed by a single Layout call. A zero field
// uses the corresponding default.
type Limits struct {
	MaxNodes       int
	MaxDepth       int
	MaxTextRunes   int
	MaxImagePixels int64
}

// DefaultLimits returns the limits used for zero-valued Limits fields.
func DefaultLimits() Limits {
	return Limits{
		MaxNodes:       1 << 20,
		MaxDepth:       256,
		MaxTextRunes:   20_000_000,
		MaxImagePixels: 64 << 20,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxNodes <= 0 {
		l.MaxNodes = d.MaxNodes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxTextRunes <= 0 {
		l.MaxTextRunes = d.MaxTextRunes
	}
	if l.MaxImagePixels <= 0 {
		l.MaxImagePixels = d.MaxImagePixels
	}
	return l
}

// Viewport is the page or window size in CSS pixels.
type Viewport struct {
	Width, Height float32
}

// Typography carries document-wide defaults. Per-node styles override them.
type Typography struct {
	FontFamily    string
	FontSize      float32
	LineHeight    float32
	LetterSpacing float32
	// WordSpacing is added to word-separating spaces (U+0020 / U+00A0). Zero
	// is the CSS initial value.
	WordSpacing float32
	// ParagraphGap is inserted between top-level block units when their own
	// margins are zero.
	ParagraphGap float32
	TextAlign    TextAlign
	Color        paint.Color
	// Background is painted behind each page. A transparent value means the
	// host is responsible for the page background.
	Background paint.Color
	// Rules carries the document-wide line-breaking and pagination policies.
	// Its zero value is the spec default: no optional typography, and no
	// widow/orphan protection. See TextRules.
	Rules TextRules
}

func (t Typography) withDefaults() Typography {
	if t.FontFamily == "" {
		t.FontFamily = "default"
	}
	if t.FontSize <= 0 {
		t.FontSize = 18
	}
	if t.LineHeight <= 0 {
		t.LineHeight = 1.5
	}
	if t.TextAlign == AlignDefault || t.TextAlign > AlignJustify {
		t.TextAlign = AlignLeft
	}
	if t.Color.Transparent() {
		t.Color = paint.Color{R: 0, G: 0, B: 0, A: 0xff}
	}
	// Rules is intentionally left alone: its zero value is already the spec
	// default. In particular a zero Widows/Orphans disables the protection
	// instead of being lifted to the old 2/2.
	return t
}

// Mode selects how the continuous flow is exposed.
type Mode uint8

const (
	// ModePaged slices the flow into pages. It is the default and the only
	// mode that supports PageCount/Page.
	ModePaged Mode = iota
	// ModeScroll exposes the whole book as one continuous content stream,
	// sliced into fixed-height tiles by Tile.
	ModeScroll
)

// LayoutOptions describes one layout variant.
type LayoutOptions struct {
	Viewport   Viewport
	Typography Typography
	Fonts      *FontSet
	// Margins are page margins in CSS pixels. In scroll mode the left and
	// right values still inset content; the top and bottom values are page
	// viewport insets and do not appear in content coordinates.
	Margins paint.Insets
	// Mode selects paged (default) or scrolling output. It does not affect
	// line breaking or vertical stacking, so toggling it reuses cached flow.
	Mode Mode
	// TileHeight is the height of a scroll tile in content pixels. Zero uses
	// Viewport.Height. It is ignored in paged mode.
	TileHeight float32
	// Hyphenator supplies intra-word break points for blocks whose style sets
	// Hyphenate. Nil disables hyphenation, and the zero value keeps the old
	// line breaking. Its Name participates in BreakKey.
	Hyphenator Hyphenator
}

func (o LayoutOptions) validate() (LayoutOptions, error) {
	if o.Viewport.Width <= 0 || o.Viewport.Height <= 0 {
		return o, fmt.Errorf("%w: viewport must be positive", ErrInvalidOptions)
	}
	if o.Mode > ModeScroll {
		return o, fmt.Errorf("%w: unknown mode %d", ErrInvalidOptions, o.Mode)
	}
	if o.TileHeight < 0 {
		return o, fmt.Errorf("%w: tile height must not be negative", ErrInvalidOptions)
	}
	contentW := o.Viewport.Width - o.Margins.Left - o.Margins.Right
	contentH := o.Viewport.Height - o.Margins.Top - o.Margins.Bottom
	if contentW <= 0 || contentH <= 0 {
		return o, fmt.Errorf("%w: margins leave no content box", ErrInvalidOptions)
	}
	if o.Margins.Left < 0 || o.Margins.Right < 0 || o.Margins.Top < 0 || o.Margins.Bottom < 0 {
		return o, fmt.Errorf("%w: margins must not be negative", ErrInvalidOptions)
	}
	if o.Fonts == nil {
		return o, fmt.Errorf("%w: Fonts is required", ErrNoFonts)
	}
	o.Typography = o.Typography.withDefaults()
	return o, nil
}

func contentWidth(o LayoutOptions) float32 {
	return o.Viewport.Width - o.Margins.Left - o.Margins.Right
}

func contentHeight(o LayoutOptions) float32 {
	return o.Viewport.Height - o.Margins.Top - o.Margins.Bottom
}

// Engine owns layout workers and configuration. It holds no document state.
type Engine struct {
	workers   int
	pageCache int
	tileCache int
	lineCache int
	limits    Limits

	// shapes counts shaping calls. Layout variants that reuse cached line boxes
	// must not increment it; tests use it to prove the cache works.
	shapes atomic.Int64
}

// New creates an Engine.
func New(opts Options) (*Engine, error) {
	if opts.Workers < 0 {
		return nil, fmt.Errorf("%w: negative worker count", ErrInvalidOptions)
	}
	if opts.PageCache < 0 {
		return nil, fmt.Errorf("%w: negative page cache size", ErrInvalidOptions)
	}
	if opts.TileCache < 0 {
		return nil, fmt.Errorf("%w: negative tile cache size", ErrInvalidOptions)
	}
	if opts.LineCache < 0 {
		return nil, fmt.Errorf("%w: negative line cache size", ErrInvalidOptions)
	}
	workers := opts.Workers
	if workers == 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < 1 {
		workers = 1
	}
	pageCache := opts.PageCache
	if pageCache == 0 {
		pageCache = 4
	}
	tileCache := opts.TileCache
	if tileCache == 0 {
		tileCache = 8
	}
	lineCache := opts.LineCache
	if lineCache == 0 {
		lineCache = 256
	}
	return &Engine{
		workers:   workers,
		pageCache: pageCache,
		tileCache: tileCache,
		lineCache: lineCache,
		limits:    opts.Limits.withDefaults(),
	}, nil
}

// Workers returns the configured worker count.
func (e *Engine) Workers() int { return e.workers }
