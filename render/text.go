package render

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"github.com/laix/layx/render/paint"
	"golang.org/x/image/math/fixed"
)

type paraSeg struct {
	node  NodeID
	style Style
	// start and end are rune offsets into paragraph.text.
	start, end int32
	lang       string
	link       string
}

type paragraph struct {
	text []rune
	segs []paraSeg
	// style is the block style that supplies alignment and indentation.
	style Style
}

func (p *paragraph) segAt(off int) *paraSeg {
	i := sort.Search(len(p.segs), func(i int) bool { return int(p.segs[i].end) > off })
	if i < len(p.segs) && int(p.segs[i].start) <= off {
		return &p.segs[i]
	}
	if len(p.segs) == 0 {
		return nil
	}
	if i >= len(p.segs) {
		return &p.segs[len(p.segs)-1]
	}
	return &p.segs[i]
}

type textWorker struct {
	shaper      shaping.HarfbuzzShaper
	wrapper     shaping.LineWrapper
	segmenter   shaping.Segmenter
	fs          *FontSet
	typo        Typography
	defaultLang language.Language
	faces       []*font.Face
	refs        []*paint.Font
	orders      map[fontKey][]int
}

type fontKey struct {
	family string
	weight int
	slant  Slant
}

func newTextWorker(fs *FontSet, typo Typography) *textWorker {
	if typo.FontSize <= 0 || typo.LineHeight <= 0 {
		typo = typo.withDefaults()
	}
	w := &textWorker{
		fs:     fs,
		typo:   typo,
		faces:  make([]*font.Face, len(fs.entries)),
		refs:   make([]*paint.Font, len(fs.entries)),
		orders: make(map[fontKey][]int),
	}
	for i := range fs.entries {
		w.faces[i] = font.NewFace(fs.entries[i].font)
		w.refs[i] = fs.entries[i].ref
	}
	w.shaper.SetFontCacheSize(8)
	return w
}

func (w *textWorker) order(st Style) []int {
	key := fontKey{family: st.FontFamily, weight: st.FontWeight, slant: st.FontSlant}
	if o, ok := w.orders[key]; ok {
		return o
	}
	o := w.fs.order(st.FontFamily, st.FontWeight, st.FontSlant)
	w.orders[key] = o
	return o
}

type workerFontmap struct {
	faces []*font.Face
	order []int
}

func (m workerFontmap) ResolveFace(r rune) *font.Face {
	for _, i := range m.order {
		if _, ok := m.faces[i].NominalGlyph(r); ok {
			return m.faces[i]
		}
	}
	if len(m.order) != 0 {
		return m.faces[m.order[0]]
	}
	return m.faces[0]
}

type shapedRun struct {
	out   shaping.Output
	style Style
	ref   *paint.Font
}

// shapeRange shapes paragraph runes [start,end). It deliberately does not
// retain the shaped output beyond the line wrapping / drawing operation: the
// layout itself stores only line boxes and rune ranges.
func (w *textWorker) shapeRange(p *paragraph, start, end int, defaultLang language.Language) []shapedRun {
	start = clampInt(start, 0, len(p.text))
	end = clampInt(end, start, len(p.text))
	if start == end {
		return nil
	}
	out := make([]shapedRun, 0, 16)
	for si := range p.segs {
		seg := &p.segs[si]
		s := maxInt(start, int(seg.start))
		e := minInt(end, int(seg.end))
		if s >= e {
			continue
		}
		lang := defaultLang
		if seg.lang != "" {
			lang = language.NewLanguage(seg.lang)
		}
		size := resolveFontSize(seg.style, w.typo)
		in := shaping.Input{
			Text:      p.text,
			RunStart:  s,
			RunEnd:    e,
			Direction: di.DirectionLTR,
			Size:      fixed.Int26_6(float64(size) * 64),
			Language:  lang,
		}
		fm := workerFontmap{faces: w.faces, order: w.order(seg.style)}
		for _, split := range w.segmenter.Split(in, fm) {
			shaped := w.shaper.ShapeNoExtents(split)
			out = append(out, shapedRun{out: shaped, style: seg.style, ref: w.refs[faceIndex(w.faces, shaped.Face)]})
		}
	}
	for i := range out {
		ls := fixed.Int26_6(float32(out[i].style.LetterSpacing) * 64)
		if ls != 0 {
			out[i].out.AddLetterSpacing(ls, i == 0, i == len(out)-1)
		}
	}
	return out
}

func faceIndex(faces []*font.Face, target *font.Face) int {
	for i, f := range faces {
		if f == target {
			return i
		}
	}
	return 0
}

type lineBox struct {
	para   *paragraph
	start  int32
	end    int32
	x      float32
	y      float32
	width  float32
	avail  float32
	height float32
	// baseline is the distance from the line top to the text baseline.
	baseline float32
	last     bool
	align    TextAlign
}

// wrapParagraph breaks p into lines of at most width pixels. It returns line
// boxes positioned relative to the paragraph origin (x/y start at zero).
func (w *textWorker) wrapParagraph(
	ctx context.Context,
	p *paragraph,
	width float32,
	typo Typography,
	blockStyle Style,
	defaultLang language.Language,
) ([]lineBox, error) {
	runs := w.shapeRange(p, 0, len(p.text), defaultLang)
	if len(runs) == 0 {
		return nil, nil
	}
	outs := make([]shaping.Output, len(runs))
	for i := range runs {
		outs[i] = runs[i].out
	}
	config := shaping.WrapConfig{
		Direction:   di.DirectionLTR,
		BreakPolicy: shaping.WhenNecessary,
	}
	w.wrapper.Prepare(config, p.text, shaping.NewSliceIterator(outs))

	align := blockStyle.TextAlign
	indent := blockStyle.FirstLineIndent
	if indent < 0 {
		indent = 0
	}
	lineHeight := blockStyle.LineHeight
	if lineHeight <= 0 {
		lineHeight = typo.LineHeight
	}

	lines := make([]lineBox, 0, len(p.text)/24+1)
	lineStart := 0
	first := true
	y := float32(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		maxW := width
		if first {
			maxW = width - indent
		}
		if maxW <= 0 {
			return nil, fmt.Errorf("%w: first-line indent leaves no line width", ErrInvalidOptions)
		}
		wrapped, done := w.wrapper.WrapNextLineF(fixed.Int26_6(float64(maxW) * 64))
		if wrapped.Line == nil {
			break
		}
		end := wrapped.NextLine
		if end < lineStart {
			end = lineStart
		}
		if end == lineStart && !done {
			// Defensive: the wrapper should always make progress. Rather than
			// spin forever on corrupt input, stop the paragraph here.
			break
		}

		var (
			lineWidth     float32
			ascent        float32
			descent       float32
			lineHeightMax float32
		)
		for _, out := range wrapped.Line {
			lineWidth += float32(out.Advance) / 64
			if a := float32(out.LineBounds.Ascent) / 64; a > ascent {
				ascent = a
			}
			if d := float32(-out.LineBounds.Descent) / 64; d > descent {
				descent = d
			}
			st := p.styleAt(out.Runes.Offset)
			size := resolveFontSize(st, typo)
			lh := st.LineHeight
			if lh <= 0 {
				lh = lineHeight
			}
			if h := size * lh; h > lineHeightMax {
				lineHeightMax = h
			}
		}
		height := lineHeightMax
		if minH := ascent + descent; height < minH {
			height = minH
		}
		leading := height - ascent - descent
		if leading < 0 {
			leading = 0
		}
		baseline := ascent + leading/2

		x := float32(0)
		switch align {
		case AlignRight:
			x = maxW - lineWidth
		case AlignCenter:
			x = (maxW - lineWidth) / 2
		}
		if first {
			x += indent
		}
		lines = append(lines, lineBox{
			para:     p,
			start:    int32(lineStart),
			end:      int32(end),
			x:        x,
			y:        y,
			width:    lineWidth,
			avail:    maxW,
			height:   height,
			baseline: baseline,
			last:     done,
			align:    align,
		})
		y += height
		lineStart = wrapped.NextLine
		first = false
		if done {
			break
		}
	}
	return lines, nil
}

func (p *paragraph) styleAt(off int) Style {
	if seg := p.segAt(off); seg != nil {
		return seg.style
	}
	return p.style
}

func resolveFontSize(st Style, typo Typography) float32 {
	if st.FontSize > 0 {
		return st.FontSize
	}
	if typo.FontSize > 0 {
		return typo.FontSize
	}
	return 18
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
