package render

import "github.com/laix/layx/render/paint"

// Slant selects an upright or italic face.
type Slant uint8

const (
	SlantNone Slant = iota
	SlantItalic
)

// TextAlign controls horizontal alignment within the line box.
type TextAlign uint8

const (
	// AlignDefault inherits Typography.TextAlign (which itself defaults to
	// left).
	AlignDefault TextAlign = iota
	AlignLeft
	AlignRight
	AlignCenter
	AlignJustify
)

// Style is an already cascaded set of layout properties.
//
// Zero values mean "use the document default" for font family, size, line
// height, color and weight. Margins and indentation are absolute CSS pixels;
// zero means zero.
type Style struct {
	FontFamily string
	FontWeight int
	FontSlant  Slant
	// FontSize is in CSS pixels. Zero uses Typography.FontSize.
	FontSize float32
	// LineHeight is a multiplier of FontSize. Zero uses Typography.LineHeight.
	LineHeight    float32
	LetterSpacing float32
	Color         paint.Color
	TextAlign     TextAlign
	// FirstLineIndent shifts the first line of a paragraph.
	FirstLineIndent float32

	MarginTop    float32
	MarginBottom float32
	MarginLeft   float32
	MarginRight  float32

	BreakBefore bool
	BreakAfter  bool
	// KeepTogether asks the paginator not to split this block if it fits on a
	// single page.
	KeepTogether bool
	// Widows and Orphans override LayoutOptions defaults for this block when
	// non-zero.
	Widows  int
	Orphans int
}
