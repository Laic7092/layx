package render

import (
	"context"
	"fmt"

	"github.com/go-text/typesetting/language"
)

type itemKind uint8

const (
	itemLine itemKind = iota
	itemImage
)

type flowItem struct {
	kind itemKind

	para       *paragraph
	start, end int32
	x, y       float32
	width      float32
	avail      float32
	height     float32
	baseline   float32
	last       bool
	align      TextAlign

	paraLine  int32
	paraLines int32

	breakBefore  bool
	breakAfter   bool
	keepTogether bool
	keepGroup    int32
	widows       int
	orphans      int

	node  NodeID
	image ImageID
	link  string
}

type unit struct {
	items        []flowItem
	paras        []*paragraph
	xOffset      float32
	height       float32
	marginTop    float32
	marginBottom float32
	breakBefore  bool
	breakAfter   bool
	keepTogether bool
}

type blockBox struct {
	items        []flowItem
	paras        []*paragraph
	style        Style
	width        float32
	height       float32
	xOffset      float32
	marginTop    float32
	marginBottom float32
	breakBefore  bool
	breakAfter   bool
	keepTogether bool
}

// resolveStyle fills document defaults into an already-cascaded style.
func resolveStyle(st Style, typo Typography) Style {
	return inheritStyle(Style{}, st, typo)
}

// inheritStyle applies CSS-like inheritance for typographic fields while
// keeping box properties (margins, break flags) local to the node.
func inheritStyle(parent, child Style, typo Typography) Style {
	out := child
	if out.FontFamily == "" {
		out.FontFamily = parent.FontFamily
	}
	if out.FontWeight == 0 {
		out.FontWeight = parent.FontWeight
	}
	if out.FontSlant == SlantNone {
		out.FontSlant = parent.FontSlant
	}
	if out.FontSize <= 0 {
		out.FontSize = parent.FontSize
	}
	if out.LineHeight <= 0 {
		out.LineHeight = parent.LineHeight
	}
	if out.LetterSpacing == 0 {
		out.LetterSpacing = parent.LetterSpacing
	}
	if out.Color.Transparent() {
		out.Color = parent.Color
	}
	if out.TextAlign == AlignDefault {
		out.TextAlign = parent.TextAlign
	}
	if out.FirstLineIndent == 0 {
		out.FirstLineIndent = parent.FirstLineIndent
	}

	if out.FontFamily == "" {
		out.FontFamily = typo.FontFamily
	}
	if out.FontWeight == 0 {
		out.FontWeight = 400
	}
	if out.FontSize <= 0 {
		out.FontSize = typo.FontSize
	}
	if out.LineHeight <= 0 {
		out.LineHeight = typo.LineHeight
	}
	if out.LetterSpacing == 0 {
		out.LetterSpacing = typo.LetterSpacing
	}
	if out.Color.Transparent() {
		out.Color = typo.Color
	}
	if out.TextAlign == AlignDefault || out.TextAlign > AlignJustify {
		out.TextAlign = typo.TextAlign
	}
	return out
}

type tokenKind uint8

const (
	tokenText tokenKind = iota
	tokenImage
	tokenBreak
)

type inlineToken struct {
	kind  tokenKind
	node  NodeID
	style Style
	text  []rune
	image ImageID
	w, h  float32
	link  string
	lang  string
}

func (lc *layoutContext) flattenInline(nodes []Node, ids []NodeID, parent Style, parentLang string, out *[]inlineToken) error {
	for _, id := range ids {
		if id < 0 || int(id) >= len(nodes) {
			return fmt.Errorf("%w: inline child %d out of range", ErrInvalidDocument, id)
		}
		n := &nodes[id]
		st := lc.inlineStyle(n, parent)
		lang := n.Lang
		if lang == "" {
			lang = parentLang
		}
		switch n.Kind {
		case NodeText:
			if n.Text != "" {
				*out = append(*out, inlineToken{
					kind: tokenText, node: id, style: st,
					text: []rune(n.Text), link: n.Link, lang: lang,
				})
			}
		case NodeBreak:
			*out = append(*out, inlineToken{kind: tokenBreak})
		case NodeImage:
			*out = append(*out, inlineToken{
				kind: tokenImage, node: id, style: st,
				image: n.Image, w: n.Width, h: n.Height, link: n.Link, lang: lang,
			})
		case NodeInline:
			if err := lc.flattenInline(nodes, n.Children, st, lang, out); err != nil {
				return err
			}
		case NodeBlock:
			return fmt.Errorf("%w: block node %d inside an inline context", ErrInvalidDocument, id)
		default:
			return fmt.Errorf("%w: unknown node kind %d", ErrInvalidDocument, n.Kind)
		}
	}
	return nil
}

// withStyle is used by the flow walker, which has access to the document.
func (lc *layoutContext) styleFor(n *Node) Style {
	return lc.doc.style(n.Style)
}

func (lc *layoutContext) inlineStyle(n *Node, parent Style) Style {
	return inheritStyle(parent, lc.styleFor(n), lc.typo)
}

func stackUnits(units []unit, defaultGap float32) (items []flowItem, paras []*paragraph, height float32) {
	cursor := float32(0)
	prevBottom := float32(0)
	for i := range units {
		u := &units[i]
		gap := u.marginTop
		if i > 0 {
			gap = maxF32(prevBottom, u.marginTop)
			if gap == 0 {
				gap = defaultGap
			}
		}
		cursor += gap
		base := len(items)
		group := int32(base)
		for _, it := range u.items {
			it.x += u.xOffset
			it.y += cursor
			if u.keepTogether {
				it.keepTogether = true
				it.keepGroup = group
			}
			items = append(items, it)
		}
		if len(items) > base {
			if u.breakBefore {
				items[base].breakBefore = true
			}
			if u.breakAfter {
				items[len(items)-1].breakAfter = true
			}
		}
		paras = append(paras, u.paras...)
		cursor += u.height
		prevBottom = u.marginBottom
	}
	return items, paras, cursor
}

func (lc *layoutContext) layoutBlock(
	ctx context.Context,
	nodes []Node,
	id NodeID,
	parentWidth float32,
	parentStyle Style,
) (*blockBox, error) {
	if id < 0 || int(id) >= len(nodes) {
		return nil, fmt.Errorf("%w: block node %d out of range", ErrInvalidDocument, id)
	}
	n := &nodes[id]
	st := inheritStyle(parentStyle, lc.styleFor(n), lc.typo)
	contentW := parentWidth - st.MarginLeft - st.MarginRight
	if contentW <= 0 {
		return nil, fmt.Errorf("%w: block %d has non-positive content width", ErrInvalidDocument, id)
	}
	box := &blockBox{
		style:        st,
		width:        contentW,
		xOffset:      st.MarginLeft,
		marginTop:    st.MarginTop,
		marginBottom: st.MarginBottom,
		breakBefore:  st.BreakBefore,
		breakAfter:   st.BreakAfter,
		keepTogether: st.KeepTogether,
	}

	var (
		units   []unit
		pending []inlineToken
	)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		u, err := lc.tokensToUnits(pending, contentW, st)
		if err != nil {
			return err
		}
		units = append(units, u...)
		pending = pending[:0]
		return nil
	}

	for _, child := range n.Children {
		if child < 0 || int(child) >= len(nodes) {
			return nil, fmt.Errorf("%w: child %d out of range", ErrInvalidDocument, child)
		}
		cn := &nodes[child]
		if cn.Kind == NodeBlock {
			if err := flush(); err != nil {
				return nil, err
			}
			cb, err := lc.layoutBlock(ctx, nodes, child, contentW, st)
			if err != nil {
				return nil, err
			}
			units = append(units, unit{
				items:        cb.items,
				paras:        cb.paras,
				xOffset:      cb.xOffset,
				height:       cb.height,
				marginTop:    cb.marginTop,
				marginBottom: cb.marginBottom,
				breakBefore:  cb.breakBefore,
				breakAfter:   cb.breakAfter,
				keepTogether: cb.keepTogether,
			})
			continue
		}
		if err := lc.flattenInline(nodes, []NodeID{child}, st, n.Lang, &pending); err != nil {
			return nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}

	box.items, box.paras, box.height = stackUnits(units, lc.typo.ParagraphGap)
	return box, nil
}

func (lc *layoutContext) layoutInlineRun(
	ctx context.Context,
	tokens []inlineToken,
	parent Style,
	width float32,
) (*blockBox, error) {
	units, err := lc.tokensToUnits(tokens, width, parent)
	if err != nil {
		return nil, err
	}
	items, paras, height := stackUnits(units, lc.typo.ParagraphGap)
	return &blockBox{
		items:  items,
		paras:  paras,
		style:  parent,
		width:  width,
		height: height,
	}, nil
}

func (lc *layoutContext) tokensToUnits(tokens []inlineToken, width float32, blockStyle Style) ([]unit, error) {
	var units []unit
	for i := 0; i < len(tokens); {
		tok := &tokens[i]
		switch tok.kind {
		case tokenBreak:
			i++
		case tokenImage:
			u, ok, err := lc.imageUnit(tok, width, blockStyle)
			if err != nil {
				return nil, err
			}
			if ok {
				units = append(units, u)
			}
			i++
		case tokenText:
			j := i
			for j < len(tokens) && tokens[j].kind == tokenText {
				j++
			}
			p := &paragraph{style: blockStyle}
			for k := i; k < j; k++ {
				t := &tokens[k]
				start := int32(len(p.text))
				p.text = append(p.text, t.text...)
				p.segs = append(p.segs, paraSeg{
					node: t.node, style: t.style,
					start: start, end: int32(len(p.text)),
					lang: t.lang, link: t.link,
				})
			}
			lines, err := lc.worker.wrapParagraph(lc.ctx, p, width, lc.typo, blockStyle, lc.defaultLang)
			if err != nil {
				return nil, err
			}
			if len(lines) > 0 {
				u := unit{paras: []*paragraph{p}}
				for li, ln := range lines {
					u.items = append(u.items, flowItem{
						kind:      itemLine,
						para:      p,
						start:     ln.start,
						end:       ln.end,
						x:         ln.x,
						y:         ln.y,
						width:     ln.width,
						avail:     ln.avail,
						height:    ln.height,
						baseline:  ln.baseline,
						last:      ln.last,
						align:     ln.align,
						paraLine:  int32(li),
						paraLines: int32(len(lines)),
						widows:    blockStyle.Widows,
						orphans:   blockStyle.Orphans,
					})
					u.height += ln.height
				}
				if j < len(tokens) && tokens[j].kind == tokenBreak {
					u.marginBottom = 0
				} else {
					u.marginBottom = lc.typo.ParagraphGap
				}
				units = append(units, u)
			}
			i = j
		}
	}
	return units, nil
}

func (lc *layoutContext) imageUnit(tok *inlineToken, width float32, blockStyle Style) (unit, bool, error) {
	if tok.w <= 0 && tok.h <= 0 {
		return unit{}, false, nil
	}
	w, h := tok.w, tok.h
	if w <= 0 && h > 0 {
		w = width
	}
	if w > width {
		if h > 0 {
			h *= width / w
		}
		w = width
	}
	if h <= 0 {
		h = w
	}
	pixels := int64(w * h)
	if lc.limits.MaxImagePixels > 0 && pixels > lc.limits.MaxImagePixels {
		return unit{}, false, fmt.Errorf("%w: image %d is %d pixels", ErrLimitExceeded, tok.node, pixels)
	}
	x := float32(0)
	align := blockStyle.TextAlign
	switch align {
	case AlignRight:
		x = width - w
	case AlignCenter:
		x = (width - w) / 2
	}
	it := flowItem{
		kind: itemImage, node: tok.node, image: tok.image,
		x: x, width: w, height: h, link: tok.link,
	}
	return unit{items: []flowItem{it}, height: h}, true, nil
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// layoutContext is the per-call state shared by layout tasks. Tasks only read
// these fields; their own textWorker is private.
type layoutContext struct {
	ctx         context.Context
	doc         *Document
	opts        LayoutOptions
	typo        Typography
	limits      Limits
	chapter     Chapter
	chapterIdx  int
	rootStyle   Style
	defaultLang language.Language
	worker      *textWorker
}
