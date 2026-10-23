package render

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/go-text/typesetting/language"
	"github.com/laix/layx/render/paint"
)

// Location identifies a position inside a laid-out document.
//
// Node and Offset are valid for the lifetime of a Layout and are what hit
// testing, selection and search use. Anchor is the host anchor associated with
// Node (or NoAnchor) and is stable across relayouts; it is the value a host
// should persist and map back to its own CFI/bookmark representation.
//
// Locations returned by Locate and HitTest always have both Node and Anchor
// filled in. When constructing a location from a persisted anchor alone, set
// Node to InvalidNode: PageOf and Selection will resolve the anchor for you.
type Location struct {
	Chapter int
	Node    NodeID
	Anchor  AnchorID
	// Offset is a rune offset within Node's text.
	Offset int
}

// SelectionQuad is one highlighted rectangle. Rect is in page coordinates.
type SelectionQuad struct {
	Page int
	Rect paint.Rect
}

// Link is a host link found on a page.
type Link struct {
	Rect paint.Rect
	URL  string
}

type pageInfo struct {
	chapter     int
	first, last int
	top         float32
}

type paraPlacement struct {
	para      *paragraph
	firstItem int
	lineCount int
}

type anchorPos struct {
	chapter int
	node    NodeID
	offset  int
}

type chapterFlow struct {
	chapter Chapter
	items   []flowItem
	paras   []*paragraph
	// boxes are block border/background rectangles in chapter content
	// coordinates. Unstyled documents leave this empty.
	boxes []blockRect

	placements []paraPlacement
	nodePlace  map[NodeID]int32
	nodeFirst  map[NodeID]int32
	nodeAnchor map[NodeID]AnchorID
	anchors    map[AnchorID]anchorPos
	height     float32
	pages      []pageInfo
	firstPage  int
	contentTop float32
}

// Layout is an immutable layout variant. Pages are rendered lazily and cached
// up to Engine's PageCache limit; all exported methods are safe for concurrent
// use.
//
// A Layout produced by Relayout shares cached line boxes and page maps with
// the layout it came from. Only the three keys change:
//
//	BreakKey -> line boxes (shaping and line breaking)
//	PageKey  -> vertical positions and page boundaries
//	PaintKey -> display lists (colors), rebuilt lazily
type Layout struct {
	engine *Engine
	doc    *Document
	opts   LayoutOptions

	chapters []chapterFlow
	pages    []pageInfo
	anchors  map[AnchorID]anchorPos

	variants *variantCache
	// breakKeys has one entry per chapter: horizontal layout identity.
	breakKeys []keyHash
	// pageKeys has one entry per chapter: PageKey.
	pageKeys []keyHash
	// paintKey combines all PageKeys with the theme.
	paintKey keyHash

	pool *sync.Pool

	// lineCache is Layout-scoped: it dies with the variant, so evicting a
	// BreakKey/PageKey variant also releases its shaped lines. It is bounded
	// by Options.LineCache.
	lineCache *lineCache

	// Scroll-mode fields. chapterTop[i] is the content y of chapter i;
	// contentHeight is the end of the last chapter's flow.
	chapterTop    []float32
	contentHeight float32
	tileHeight    float32
	tileCount     int
	tileMu        sync.Mutex
	tileCache     map[int]*Tile
	tileOrder     []int

	cacheMu    sync.Mutex
	cache      map[int]*Page
	cacheOrder []int
}

// Layout computes a new layout variant for doc.
func (e *Engine) Layout(ctx context.Context, doc *Document, opts LayoutOptions) (*Layout, error) {
	return e.layoutDoc(ctx, doc, opts, nil)
}

// layoutDoc implements Layout and Relayout. A non-nil variants cache carries
// break and page results across Relayout calls for the same document.
func (e *Engine) layoutDoc(ctx context.Context, doc *Document, opts LayoutOptions, variants *variantCache) (*Layout, error) {
	if doc == nil {
		return nil, fmt.Errorf("%w: nil document", ErrInvalidDocument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts, err := opts.validate()
	if err != nil {
		return nil, err
	}
	nch := len(doc.chapters)
	if variants == nil {
		variants = newVariantCache(nch)
	}
	if !variants.limitsVerified() {
		if err := e.checkLimits(ctx, doc); err != nil {
			return nil, err
		}
		variants.markLimitsVerified()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	typo := opts.Typography
	defaultLang := language.NewLanguage(doc.meta.Language)

	// Pass 1: derive the three keys. Hashing walks the document but does no
	// shaping, so it is cheap even for a 5M-rune book.
	keyer := variants.newKeyer()
	contentH := contentHeight(opts)
	breakKeys := make([]keyHash, nch)
	pageKeys := make([]keyHash, nch)
	for ci := 0; ci < nch; ci++ {
		if ci%32 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		ch := &doc.chapters[ci]
		breakKeys[ci] = chapterBreakKey(keyer, doc, ch, opts, typo)
		pageKeys[ci] = pageKey(keyer, breakKeys[ci], typo, contentH)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	currentPaintKey := paintKey(keyer, pageKeys, typo)

	// Pass 2: find cached line boxes. Only chapters whose BreakKey is absent
	// need shaping.
	breaks := make([]*breakChapter, nch)
	needBreak := make([]bool, nch)
	anyBreak := false
	for ci := 0; ci < nch; ci++ {
		key := cacheKey{chapter: ci, fonts: opts.Fonts, key: breakKeys[ci]}
		if bc := variants.getBreak(key); bc != nil {
			breaks[ci] = bc
		} else {
			needBreak[ci] = true
			anyBreak = true
		}
	}

	pool := &sync.Pool{New: func() any {
		w := newTextWorker(opts.Fonts, typo)
		w.defaultLang = defaultLang
		w.shapes = &e.shapes
		return w
	}}

	if anyBreak {
		explicit := make([]map[NodeID]float32, nch)
		for ci := range doc.chapters {
			if needBreak[ci] {
				explicit[ci] = explicitLineHeights(doc, &doc.chapters[ci])
			}
		}
		plans, tasks, err := buildTasks(doc, opts, typo, defaultLang, needBreak)
		if err != nil {
			return nil, err
		}

		results := make([]*breakBox, len(tasks))
		errs := make([]error, len(tasks))

		workers := e.workers
		if workers > len(tasks) {
			workers = len(tasks)
		}
		if workers < 1 && len(tasks) > 0 {
			workers = 1
		}
		var next atomic.Int64
		var wg sync.WaitGroup
		for wi := 0; wi < workers; wi++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					if ctx.Err() != nil {
						return
					}
					idx := int(next.Add(1)) - 1
					if idx < 0 || idx >= len(tasks) {
						return
					}
					t := &tasks[idx]
					ch := &doc.chapters[t.chapter]
					w := pool.Get().(*textWorker)
					inline := &layoutContext{
						ctx:         ctx,
						doc:         doc,
						opts:        opts,
						typo:        typo,
						limits:      e.limits,
						chapter:     *ch,
						chapterIdx:  t.chapter,
						rootStyle:   t.rootStyle,
						defaultLang: defaultLang,
					}
					bc := &breakContext{
						ctx:         ctx,
						doc:         doc,
						typo:        typo,
						chapter:     *ch,
						defaultLang: defaultLang,
						limits:      e.limits,
						worker:      w,
						inline:      inline,
						explicitLH:  explicit[t.chapter],
					}
					var (
						box     *breakBox
						taskErr error
					)
					if t.node >= 0 {
						box, taskErr = bc.block(ch.Nodes, t.node, t.width, t.rootStyle)
					} else {
						box, taskErr = bc.inlineRun(t.tokens, t.style, t.width)
					}
					pool.Put(w)
					if taskErr != nil {
						errs[idx] = taskErr
						continue
					}
					results[idx] = box
				}
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i, err := range errs {
			if err != nil {
				return nil, fmt.Errorf("layout task %d: %w", i, err)
			}
		}
		for pi := range plans {
			plan := &plans[pi]
			units := make([]breakUnit, 0, len(plan.units))
			for _, ti := range plan.units {
				box := results[ti]
				if box == nil {
					return nil, fmt.Errorf("%w: chapter %q produced no layout", ErrInvalidDocument, doc.chapters[plan.chapter].ID)
				}
				u := unitFromBox(box)
				if tasks[ti].node < 0 {
					// A top-level inline task is not a block of its own: its box
					// is the chapter root. layChapter emits that root's border and
					// background once around the whole flow, so the wrapper must
					// not paint or re-apply root border offsets.
					cp := *box
					cp.style = Style{}
					u.box = &cp
				}
				units = append(units, u)
			}
			bc := &breakChapter{key: breakKeys[plan.chapter], units: units}
			breaks[plan.chapter] = bc
			variants.putBreak(cacheKey{chapter: plan.chapter, fonts: opts.Fonts, key: breakKeys[plan.chapter]}, bc)
		}
	}

	// Pass 3: reuse complete page maps when PageKey is unchanged, otherwise
	// re-stack the cached line boxes and re-paginate.
	laid := make([]*laidChapter, nch)
	needPage := make([]bool, nch)
	for ci := 0; ci < nch; ci++ {
		key := cacheKey{chapter: ci, fonts: opts.Fonts, key: pageKeys[ci]}
		if lc := variants.getPage(key); lc != nil {
			laid[ci] = lc
		} else {
			needPage[ci] = true
		}
	}
	for ci := 0; ci < nch; ci++ {
		if !needPage[ci] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ch := &doc.chapters[ci]
		root := &ch.Nodes[ch.Root]
		rootStyle := resolveStyle(doc.style(root.Style), typo)
		lc, err := e.layChapter(ctx, ch, ci, breaks[ci], rootStyle, typo, contentH, contentWidth(opts))
		if err != nil {
			return nil, err
		}
		laid[ci] = lc
		variants.putPage(cacheKey{chapter: ci, fonts: opts.Fonts, key: pageKeys[ci]}, lc)
	}

	l := &Layout{
		engine:    e,
		doc:       doc,
		opts:      opts,
		pool:      pool,
		cache:     make(map[int]*Page),
		anchors:   make(map[AnchorID]anchorPos),
		variants:  variants,
		breakKeys: breakKeys,
		pageKeys:  pageKeys,
		paintKey:  currentPaintKey,
		lineCache: newLineCache(e.lineCache),
	}
	l.chapters = make([]chapterFlow, nch)
	for ci := 0; ci < nch; ci++ {
		ch := &doc.chapters[ci]
		root := &ch.Nodes[ch.Root]
		rootStyle := resolveStyle(doc.style(root.Style), typo)
		lc := laid[ci]
		cf := chapterFlow{
			chapter:    *ch,
			items:      lc.items,
			paras:      lc.paras,
			boxes:      lc.boxes,
			placements: lc.placements,
			nodePlace:  lc.nodePlace,
			nodeFirst:  lc.nodeFirst,
			nodeAnchor: lc.nodeAnchor,
			anchors:    lc.anchors,
			height:     lc.height,
			pages:      lc.pages,
			firstPage:  len(l.pages),
			contentTop: rootStyle.MarginTop,
		}
		l.pages = append(l.pages, lc.pages...)
		for id, pos := range lc.anchors {
			if _, exists := l.anchors[id]; !exists {
				l.anchors[id] = pos
			}
		}
		l.chapters[ci] = cf
	}
	l.initScroll()
	return l, nil
}

// layChapter turns cached line boxes into flow items, positions them with the
// current vertical typography, and paginates. It does not shape.
func (e *Engine) layChapter(
	ctx context.Context,
	ch *Chapter,
	ci int,
	bc *breakChapter,
	rootStyle Style,
	typo Typography,
	contentH float32,
	availW float32,
) (*laidChapter, error) {
	items, paras, height, boxes := stackBreakUnits(bc.units, typo.ParagraphGap, typo)
	bt, _, bb, bl := borderWidths(rootStyle)
	xShift := rootStyle.MarginLeft + bl
	yShift := rootStyle.MarginTop + bt
	for i := range items {
		items[i].x += xShift
		items[i].y += yShift
	}
	for i := range boxes {
		boxes[i].x += xShift
		boxes[i].y += yShift
	}
	if boxPaints(rootStyle) {
		boxes = append(boxes, blockRect{
			style:  rootStyle,
			x:      rootStyle.MarginLeft,
			y:      rootStyle.MarginTop,
			width:  availW - rootStyle.MarginLeft - rootStyle.MarginRight,
			height: bt + height + bb,
		})
	}
	sortBlockRects(boxes)

	place := bc.placementsFor(ch, ci, items, paras)
	lc := &laidChapter{
		items:      items,
		paras:      paras,
		boxes:      boxes,
		height:     height + yShift + rootStyle.MarginBottom,
		placements: place.placements,
		nodePlace:  place.nodePlace,
		nodeFirst:  place.nodeFirst,
		nodeAnchor: place.nodeAnchor,
		anchors:    place.anchors,
	}
	lc.pages = paginate(ctx, items, contentH, typo)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for pi := range lc.pages {
		lc.pages[pi].chapter = ci
	}
	return lc, nil
}

type layoutTask struct {
	chapter int
	// node >= 0 means a block subtree; node < 0 means an inline token run.
	node      NodeID
	tokens    []inlineToken
	style     Style
	rootStyle Style
	width     float32
}

type layoutPlan struct {
	chapter   int
	rootStyle Style
	units     []int
}

func buildTasks(doc *Document, opts LayoutOptions, typo Typography, defaultLang language.Language, need []bool) ([]layoutPlan, []layoutTask, error) {
	var (
		tasks []layoutTask
		plans []layoutPlan
	)
	for ci := range doc.chapters {
		if need != nil && !need[ci] {
			continue
		}
		ch := &doc.chapters[ci]
		nodes := ch.Nodes
		root := &nodes[ch.Root]
		rootStyle := resolveStyle(doc.style(root.Style), typo)
		_, rootRight, _, rootLeft := borderWidths(rootStyle)
		width := contentWidth(opts) - rootStyle.MarginLeft - rootStyle.MarginRight - rootLeft - rootRight
		if width <= 0 {
			return nil, nil, fmt.Errorf("%w: chapter %q has non-positive content width", ErrInvalidOptions, ch.ID)
		}
		plan := layoutPlan{chapter: ci, rootStyle: rootStyle}
		lc := &layoutContext{
			doc: doc, opts: opts, typo: typo,
			chapter: *ch, chapterIdx: ci, defaultLang: defaultLang,
		}
		if root.Kind == NodeInline {
			var toks []inlineToken
			if err := lc.flattenInline(nodes, []NodeID{ch.Root}, rootStyle, root.Lang, &toks); err != nil {
				return nil, nil, err
			}
			if len(toks) != 0 {
				tasks = append(tasks, layoutTask{chapter: ci, node: -1, tokens: toks, style: rootStyle, rootStyle: rootStyle, width: width})
				plan.units = append(plan.units, len(tasks)-1)
			}
		} else {
			var pending []inlineToken
			flush := func() {
				if len(pending) == 0 {
					return
				}
				tasks = append(tasks, layoutTask{chapter: ci, node: -1, tokens: pending, style: rootStyle, rootStyle: rootStyle, width: width})
				plan.units = append(plan.units, len(tasks)-1)
				pending = nil
			}
			for _, child := range root.Children {
				cn := &nodes[child]
				if cn.Kind == NodeBlock {
					flush()
					tasks = append(tasks, layoutTask{chapter: ci, node: child, rootStyle: rootStyle, width: width})
					plan.units = append(plan.units, len(tasks)-1)
					continue
				}
				if err := lc.flattenInline(nodes, []NodeID{child}, rootStyle, root.Lang, &pending); err != nil {
					return nil, nil, err
				}
			}
			flush()
		}
		plans = append(plans, plan)
	}
	return plans, tasks, nil
}

// breakKit distinguishes the payload of a breakUnit.
type breakKit uint8

const (
	breakLines breakKit = iota
	breakImage
	breakBlockUnit
)

// lineBreak is one line box without vertical placement. Height and baseline
// are derived when the line is stacked, so changing LineHeight never requires
// re-breaking or re-shaping.
type lineBreak struct {
	start, end int32
	x          float32
	width      float32
	avail      float32
	last       bool
	align      TextAlign

	ascent  float32
	descent float32
	// implicitSize is the largest font size inheriting Typography.LineHeight;
	// explicitHeight is the largest size*lineHeight from overriding styles.
	implicitSize   float32
	explicitHeight float32
}

// breakUnit is one vertical slot in a block. It is the cacheable counterpart
// of flow.go's unit: the same structure, but line heights are not baked in.
type breakUnit struct {
	kind  breakKit
	para  *paragraph
	lines []lineBreak
	image flowItem
	box   *breakBox

	xOffset      float32
	marginTop    float32
	marginBottom float32
	// gapBottom means marginBottom is the default paragraph gap, which must be
	// re-read from the current Typography when the unit is stacked.
	gapBottom       bool
	breakBefore     bool
	breakAfter      bool
	keepTogether    bool
	widows, orphans int
}

// breakBox mirrors flow.go's blockBox without stacking its children.
type breakBox struct {
	units        []breakUnit
	style        Style
	width        float32
	xOffset      float32
	marginTop    float32
	marginBottom float32
	breakBefore  bool
	breakAfter   bool
	keepTogether bool
}

func unitFromBox(box *breakBox) breakUnit {
	return breakUnit{
		kind:         breakBlockUnit,
		box:          box,
		xOffset:      box.xOffset,
		marginTop:    box.marginTop,
		marginBottom: box.marginBottom,
		breakBefore:  box.breakBefore,
		breakAfter:   box.breakAfter,
		keepTogether: box.keepTogether,
	}
}

// lineMetrics applies the current LineHeight to a cached line.
func lineMetrics(ln *lineBreak, typo Typography) (height, baseline float32) {
	lhMax := ln.explicitHeight
	if ln.implicitSize > 0 {
		if h := ln.implicitSize * typo.LineHeight; h > lhMax {
			lhMax = h
		}
	}
	height = lhMax
	if minH := ln.ascent + ln.descent; height < minH {
		height = minH
	}
	leading := height - ln.ascent - ln.descent
	if leading < 0 {
		leading = 0
	}
	baseline = ln.ascent + leading/2
	return height, baseline
}

// countBreakUnits sizes the flow item and paragraph slices up front. Without
// it, every nested block would append into its own growing slice and the
// parent would copy the result again: on a 5M-rune book that costs hundreds of
// megabytes per relayout.
func countBreakUnits(units []breakUnit) (items, paras int) {
	for i := range units {
		u := &units[i]
		switch u.kind {
		case breakLines:
			items += len(u.lines)
			if u.para != nil {
				paras++
			}
		case breakImage:
			items++
		case breakBlockUnit:
			bi, bp := countBreakUnits(u.box.units)
			items += bi
			paras += bp
		}
	}
	return items, paras
}

// stackBreakUnits is the cache-friendly equivalent of flow.go's stackUnits.
// The arithmetic matches stackUnits exactly so that a cached relayout and a
// full layout produce identical line boxes and page boundaries.
func stackBreakUnits(units []breakUnit, defaultGap float32, typo Typography) (items []flowItem, paras []*paragraph, height float32, boxes []blockRect) {
	nItems, nParas := countBreakUnits(units)
	items = make([]flowItem, 0, nItems)
	paras = make([]*paragraph, 0, nParas)
	height = appendBreakUnits(units, defaultGap, typo, &items, &paras, &boxes)
	return items, paras, height, boxes
}

func appendBreakUnits(units []breakUnit, defaultGap float32, typo Typography, items *[]flowItem, paras *[]*paragraph, boxes *[]blockRect) float32 {
	cursor := float32(0)
	prevBottom := float32(0)
	start := len(*items)
	for i := range units {
		u := &units[i]
		bottom := u.marginBottom
		if u.gapBottom {
			bottom = defaultGap
		}
		gap := u.marginTop
		if i > 0 {
			gap = maxF32(prevBottom, u.marginTop)
			if gap == 0 {
				gap = defaultGap
			}
		}
		cursor += gap
		base := len(*items)
		// keepGroup is local to this unit list, matching stackUnits' use of
		// the index inside the nested slice.
		group := int32(base - start)
		switch u.kind {
		case breakLines:
			y := float32(0)
			for li := range u.lines {
				ln := &u.lines[li]
				h, baseline := lineMetrics(ln, typo)
				*items = append(*items, flowItem{
					kind:      itemLine,
					para:      u.para,
					start:     ln.start,
					end:       ln.end,
					x:         ln.x + u.xOffset,
					y:         cursor + y,
					width:     ln.width,
					avail:     ln.avail,
					height:    h,
					baseline:  baseline,
					last:      ln.last,
					align:     ln.align,
					paraLine:  int32(li),
					paraLines: int32(len(u.lines)),
					widows:    u.widows,
					orphans:   u.orphans,
				})
				y += h
			}
			cursor += y
			if u.para != nil {
				*paras = append(*paras, u.para)
			}
		case breakImage:
			it := u.image
			it.x += u.xOffset
			it.y += cursor
			*items = append(*items, it)
			cursor += it.height
		case breakBlockUnit:
			mark := len(*items)
			markBoxes := len(*boxes)
			bTop, bRight, bBottom, bLeft := borderWidths(u.box.style)
			bh := appendBreakUnits(u.box.units, defaultGap, typo, items, paras, boxes)
			for j := mark; j < len(*items); j++ {
				(*items)[j].x += u.xOffset
				(*items)[j].y += cursor + bTop
			}
			for j := markBoxes; j < len(*boxes); j++ {
				(*boxes)[j].x += u.xOffset
				(*boxes)[j].y += cursor + bTop
			}
			if boxPaints(u.box.style) {
				*boxes = append(*boxes, blockRect{
					style:  u.box.style,
					x:      u.xOffset - bLeft,
					y:      cursor,
					width:  u.box.width + bLeft + bRight,
					height: bTop + bh + bBottom,
				})
			}
			cursor += bTop + bh + bBottom
		}
		if len(*items) > base {
			if u.breakBefore {
				(*items)[base].breakBefore = true
			}
			if u.breakAfter {
				(*items)[len(*items)-1].breakAfter = true
			}
			if u.keepTogether {
				for k := base; k < len(*items); k++ {
					(*items)[k].keepTogether = true
					(*items)[k].keepGroup = group
				}
			}
		}
		prevBottom = bottom
	}
	return cursor
}

// breakContext is the per-task state for shaping and line breaking. It only
// produces break units; vertical placement happens later in layChapter.
type breakContext struct {
	ctx         context.Context
	doc         *Document
	typo        Typography
	chapter     Chapter
	defaultLang language.Language
	limits      Limits
	worker      *textWorker
	inline      *layoutContext
	explicitLH  map[NodeID]float32
}

func (bc *breakContext) block(nodes []Node, id NodeID, parentWidth float32, parentStyle Style) (*breakBox, error) {
	if id < 0 || int(id) >= len(nodes) {
		return nil, fmt.Errorf("%w: block node %d out of range", ErrInvalidDocument, id)
	}
	n := &nodes[id]
	st := inheritStyle(parentStyle, bc.doc.style(n.Style), bc.typo)
	_, br, _, bl := borderWidths(st)
	contentW := parentWidth - st.MarginLeft - st.MarginRight - bl - br
	if contentW <= 0 {
		return nil, fmt.Errorf("%w: block %d has non-positive content width", ErrInvalidDocument, id)
	}
	box := &breakBox{
		style:        st,
		width:        contentW,
		xOffset:      st.MarginLeft + bl,
		marginTop:    st.MarginTop,
		marginBottom: st.MarginBottom,
		breakBefore:  st.BreakBefore,
		breakAfter:   st.BreakAfter,
		keepTogether: st.KeepTogether,
	}

	var (
		units   []breakUnit
		pending []inlineToken
	)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		us, err := bc.tokens(pending, contentW, st)
		if err != nil {
			return err
		}
		units = append(units, us...)
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
			cb, err := bc.block(nodes, child, contentW, st)
			if err != nil {
				return nil, err
			}
			units = append(units, unitFromBox(cb))
			continue
		}
		if err := bc.inline.flattenInline(nodes, []NodeID{child}, st, n.Lang, &pending); err != nil {
			return nil, err
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	box.units = units
	return box, nil
}

func (bc *breakContext) inlineRun(tokens []inlineToken, parent Style, width float32) (*breakBox, error) {
	units, err := bc.tokens(tokens, width, parent)
	if err != nil {
		return nil, err
	}
	return &breakBox{units: units, style: parent, width: width}, nil
}

func (bc *breakContext) tokens(tokens []inlineToken, width float32, blockStyle Style) ([]breakUnit, error) {
	var units []breakUnit
	for i := 0; i < len(tokens); {
		tok := &tokens[i]
		switch tok.kind {
		case tokenBreak:
			i++
		case tokenImage:
			u, ok, err := bc.imageUnit(tok, width, blockStyle)
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
				st := t.style
				// Vertical styling is re-applied at stack time. Store only the
				// document's explicit multiplier (zero means "inherit typo").
				st.LineHeight = bc.explicitLH[t.node]
				p.segs = append(p.segs, paraSeg{
					node: t.node, style: st,
					start: start, end: int32(len(p.text)),
					lang: t.lang, link: t.link,
				})
			}
			lines, err := bc.worker.wrapParagraph(bc.ctx, p, width, bc.typo, blockStyle, bc.defaultLang)
			if err != nil {
				return nil, err
			}
			if len(lines) > 0 {
				u := breakUnit{
					kind:    breakLines,
					para:    p,
					widows:  blockStyle.Widows,
					orphans: blockStyle.Orphans,
				}
				for li := range lines {
					ln := &lines[li]
					u.lines = append(u.lines, lineBreak{
						start:          ln.start,
						end:            ln.end,
						x:              ln.x,
						width:          ln.width,
						avail:          ln.avail,
						last:           ln.last,
						align:          ln.align,
						ascent:         ln.ascent,
						descent:        ln.descent,
						implicitSize:   ln.implicitSize,
						explicitHeight: ln.explicitHeight,
					})
				}
				if j < len(tokens) && tokens[j].kind == tokenBreak {
					u.marginBottom = 0
				} else {
					u.gapBottom = true
					u.marginBottom = bc.typo.ParagraphGap
				}
				units = append(units, u)
			}
			i = j
		}
	}
	return units, nil
}

func (bc *breakContext) imageUnit(tok *inlineToken, width float32, blockStyle Style) (breakUnit, bool, error) {
	if tok.w <= 0 && tok.h <= 0 {
		return breakUnit{}, false, nil
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
	if bc.limits.MaxImagePixels > 0 && pixels > bc.limits.MaxImagePixels {
		return breakUnit{}, false, fmt.Errorf("%w: image %d is %d pixels", ErrLimitExceeded, tok.node, pixels)
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
	return breakUnit{kind: breakImage, image: it}, true, nil
}

// placementsFor computes the node/anchor maps once per BreakKey. Item order is
// identical for every PageKey derived from the same line boxes, so a
// LineHeight or paragraph-gap change does not repeat this O(lines) work.
func (bc *breakChapter) placementsFor(ch *Chapter, ci int, items []flowItem, paras []*paragraph) *chapterPlacements {
	bc.placeOnce.Do(func() {
		cf := chapterFlow{
			chapter:    *ch,
			items:      items,
			paras:      paras,
			nodePlace:  make(map[NodeID]int32, len(ch.Nodes)),
			nodeFirst:  make(map[NodeID]int32, len(ch.Nodes)),
			nodeAnchor: make(map[NodeID]AnchorID, len(ch.Anchors)),
			anchors:    make(map[AnchorID]anchorPos, len(ch.Anchors)),
		}
		buildPlacements(&cf)
		for id, node := range ch.Anchors {
			pos := anchorPos{chapter: ci, node: node, offset: 0}
			cf.anchors[id] = pos
			if cur, exists := cf.nodeAnchor[node]; !exists || id < cur {
				cf.nodeAnchor[node] = id
			}
		}
		bc.place = &chapterPlacements{
			placements: cf.placements,
			nodePlace:  cf.nodePlace,
			nodeFirst:  cf.nodeFirst,
			nodeAnchor: cf.nodeAnchor,
			anchors:    cf.anchors,
		}
	})
	return bc.place
}

func buildPlacements(cf *chapterFlow) {
	parents := make(map[NodeID]NodeID, len(cf.chapter.Nodes))
	for i := range cf.chapter.Nodes {
		for _, c := range cf.chapter.Nodes[i].Children {
			parents[c] = NodeID(i)
		}
	}
	byPara := make(map[*paragraph]int, len(cf.paras))
	cf.placements = make([]paraPlacement, 0, len(cf.paras))
	for idx, it := range cf.items {
		switch it.kind {
		case itemLine:
			pi, ok := byPara[it.para]
			if !ok {
				pi = len(cf.placements)
				byPara[it.para] = pi
				cf.placements = append(cf.placements, paraPlacement{para: it.para, firstItem: idx})
			}
			cf.placements[pi].lineCount++
			for si := range it.para.segs {
				seg := &it.para.segs[si]
				if int(seg.end) <= int(it.start) || int(seg.start) >= int(it.end) {
					continue
				}
				cf.nodePlace[seg.node] = int32(pi)
				markNodeFirst(cf, parents, seg.node, idx)
			}
		case itemImage:
			markNodeFirst(cf, parents, it.node, idx)
		}
	}
}

func markNodeFirst(cf *chapterFlow, parents map[NodeID]NodeID, node NodeID, idx int) {
	for node != InvalidNode {
		if _, ok := cf.nodeFirst[node]; ok {
			// Ancestors closer to the root were already visited at an earlier
			// or equal item index.
			if cf.nodeFirst[node] <= int32(idx) {
				return
			}
		}
		cf.nodeFirst[node] = int32(idx)
		p, ok := parents[node]
		if !ok {
			return
		}
		node = p
	}
}

// paginate slices a flowed chapter into pages. Page top values are absolute
// chapter content coordinates.
func paginate(ctx context.Context, items []flowItem, pageHeight float32, typo Typography) []pageInfo {
	if len(items) == 0 {
		return []pageInfo{{chapter: 0, first: 0, last: 0, top: 0}}
	}
	const eps = 0.01
	pages := make([]pageInfo, 0, len(items)/20+1)
	first := 0
	i := 0
	pageTop := float32(0)
	for i < len(items) {
		if i%2048 == 0 && ctx.Err() != nil {
			return pages
		}
		if i > first && items[i].breakBefore {
			pages = append(pages, pageInfo{first: first, last: i, top: pageTop})
			first = i
			pageTop = items[i].y
			continue
		}
		if i > first && items[i-1].breakAfter {
			pages = append(pages, pageInfo{first: first, last: i, top: pageTop})
			first = i
			pageTop = items[i].y
			continue
		}
		it := &items[i]
		limit := pageTop + pageHeight
		if i == first || it.y+it.height <= limit+eps {
			i++
			continue
		}

		breakAt := i
		// Keep the entire block together when possible.
		if it.keepTogether && it.keepGroup != 0 && i > first {
			start := i
			for start > first && items[start-1].keepTogether && items[start-1].keepGroup == it.keepGroup {
				start--
			}
			if start > first {
				breakAt = start
			}
		}
		// Widows and orphans only apply when the break is inside a paragraph.
		if breakAt == i && it.para != nil && it.paraLine > 0 {
			orphans := it.orphans
			if orphans <= 0 {
				orphans = typo.Orphans
			}
			widows := it.widows
			if widows <= 0 {
				widows = typo.Widows
			}
			pl := int(it.paraLine)
			total := int(it.paraLines)
			if pl < orphans {
				start := i - pl
				if start > first {
					breakAt = start
				}
			} else if remain := total - pl; remain < widows {
				cand := i + (widows - remain)
				if cand > i && cand <= len(items) &&
					items[cand-1].y+items[cand-1].height <= limit+eps {
					breakAt = cand
				}
			}
		}
		if breakAt <= first {
			breakAt = first + 1
		}
		pages = append(pages, pageInfo{first: first, last: breakAt, top: pageTop})
		first = breakAt
		pageTop = items[first].y
		i = breakAt
	}
	pages = append(pages, pageInfo{first: first, last: len(items), top: pageTop})
	return pages
}

func (e *Engine) checkLimits(ctx context.Context, doc *Document) error {
	lim := e.limits
	nodes := 0
	runes := 0
	var pixels int64
	for ci := range doc.chapters {
		if err := ctx.Err(); err != nil {
			return err
		}
		ch := &doc.chapters[ci]
		nodes += len(ch.Nodes)
		if nodes > lim.MaxNodes {
			return fmt.Errorf("%w: nodes exceed %d", ErrLimitExceeded, lim.MaxNodes)
		}
		if err := checkDepth(ch, lim.MaxDepth); err != nil {
			return err
		}
		for ni := range ch.Nodes {
			n := &ch.Nodes[ni]
			runes += utf8.RuneCountInString(n.Text)
			if runes > lim.MaxTextRunes {
				return fmt.Errorf("%w: text exceeds %d runes", ErrLimitExceeded, lim.MaxTextRunes)
			}
			if n.Kind == NodeImage && n.Width > 0 && n.Height > 0 {
				pixels += int64(n.Width * n.Height)
				if pixels > lim.MaxImagePixels {
					return fmt.Errorf("%w: image pixels exceed %d", ErrLimitExceeded, lim.MaxImagePixels)
				}
			}
		}
	}
	return nil
}

func checkDepth(ch *Chapter, maxDepth int) error {
	type frame struct {
		node  NodeID
		depth int
	}
	stack := []frame{{node: ch.Root, depth: 1}}
	seen := make([]bool, len(ch.Nodes))
	for len(stack) != 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node < 0 || int(f.node) >= len(ch.Nodes) {
			return fmt.Errorf("%w: chapter %q node %d out of range", ErrInvalidDocument, ch.ID, f.node)
		}
		if f.depth > maxDepth {
			return fmt.Errorf("%w: chapter %q exceeds max depth %d", ErrLimitExceeded, ch.ID, maxDepth)
		}
		if seen[f.node] {
			continue
		}
		seen[f.node] = true
		for _, c := range ch.Nodes[f.node].Children {
			stack = append(stack, frame{node: c, depth: f.depth + 1})
		}
	}
	return nil
}

// PageCount returns the total number of pages. Scroll-mode layouts have no
// pages and return 0; use TileCount instead.
func (l *Layout) PageCount() int {
	if l.opts.Mode == ModeScroll {
		return 0
	}
	return len(l.pages)
}

// Size returns the layout viewport.
func (l *Layout) Size() paint.Size {
	return paint.Size{Width: l.opts.Viewport.Width, Height: l.opts.Viewport.Height}
}

// Typography returns the resolved typography of this layout.
func (l *Layout) Typography() Typography { return l.opts.Typography }

// Viewport returns the viewport of this layout.
func (l *Layout) Viewport() Viewport { return l.opts.Viewport }

// Locate resolves a host anchor to a location.
func (l *Layout) Locate(anchor AnchorID) (Location, bool) {
	pos, ok := l.anchors[anchor]
	if !ok {
		return Location{}, false
	}
	return Location{Chapter: pos.chapter, Node: pos.node, Anchor: anchor, Offset: pos.offset}, true
}

// PageOf returns the page containing loc. It is paged-mode only: scroll-mode
// layouts return false because pages are not part of their coordinate space.
func (l *Layout) PageOf(loc Location) (int, bool) {
	if l.opts.Mode == ModeScroll {
		return 0, false
	}
	if loc.Chapter < 0 || loc.Chapter >= len(l.chapters) {
		return 0, false
	}
	loc, ok := l.resolveLocation(loc)
	if !ok {
		return 0, false
	}
	cf := &l.chapters[loc.Chapter]
	item, ok := cf.itemOf(loc)
	if !ok {
		return 0, false
	}
	k := sort.Search(len(cf.pages), func(i int) bool { return cf.pages[i].last > item })
	if k >= len(cf.pages) {
		k = len(cf.pages) - 1
	}
	if k < 0 {
		return 0, false
	}
	return cf.firstPage + k, true
}

// resolveLocation fills Node from Anchor when the caller only has an anchor,
// and rejects locations that do not belong to a laid-out node.
func (l *Layout) resolveLocation(loc Location) (Location, bool) {
	if loc.Chapter >= 0 && loc.Chapter < len(l.chapters) {
		if _, known := l.chapters[loc.Chapter].nodeFirst[loc.Node]; known && loc.Node != InvalidNode {
			return loc, true
		}
	}
	if loc.Anchor == NoAnchor {
		return Location{}, false
	}
	pos, ok := l.anchors[loc.Anchor]
	if !ok {
		return Location{}, false
	}
	loc.Chapter = pos.chapter
	loc.Node = pos.node
	loc.Offset = pos.offset
	return loc, true
}

func (cf *chapterFlow) itemOf(loc Location) (int, bool) {
	if pi, ok := cf.nodePlace[loc.Node]; ok && int(pi) < len(cf.placements) {
		pl := &cf.placements[pi]
		target := 0
		for si := range pl.para.segs {
			seg := &pl.para.segs[si]
			if seg.node == loc.Node {
				target = int(seg.start) + clampRuneOffset(seg, loc.Offset)
				break
			}
		}
		if target < 0 {
			target = 0
		}
		n := pl.lineCount
		if n <= 0 {
			return pl.firstItem, true
		}
		k := sort.Search(n, func(i int) bool {
			return int(cf.items[pl.firstItem+i].end) > target
		})
		if k >= n {
			k = n - 1
		}
		return pl.firstItem + k, true
	}
	if idx, ok := cf.nodeFirst[loc.Node]; ok {
		return int(idx), true
	}
	return 0, false
}

// Relayout computes a new layout with different options. The receiver is not
// modified. Line boxes and page maps are reused when their keys are unchanged.
func (l *Layout) Relayout(ctx context.Context, opts LayoutOptions) (*Layout, error) {
	return l.engine.layoutDoc(ctx, l.doc, opts, l.variants)
}

// Selection returns the page rectangles covering the rune range from..to.
// Cross-chapter selections are supported; locations are ordered by chapter.
func (l *Layout) Selection(from, to Location) []SelectionQuad {
	var ok bool
	if from, ok = l.resolveLocation(from); !ok {
		return nil
	}
	if to, ok = l.resolveLocation(to); !ok {
		return nil
	}
	if from.Chapter > to.Chapter {
		from, to = to, from
	}
	a, ok1 := l.chapters[from.Chapter].itemOf(from)
	b, ok2 := l.chapters[to.Chapter].itemOf(to)
	if !ok1 || !ok2 {
		return nil
	}
	if from.Chapter == to.Chapter && a > b {
		from, to = to, from
		a, b = b, a
	}
	w := l.pool.Get().(*textWorker)
	defer l.pool.Put(w)

	var out []SelectionQuad
	for ci := from.Chapter; ci <= to.Chapter; ci++ {
		cf := &l.chapters[ci]
		if len(cf.items) == 0 {
			continue
		}
		first, last := 0, len(cf.items)-1
		if ci == from.Chapter {
			first = a
		}
		if ci == to.Chapter {
			last = b
		}
		first = clampInt(first, 0, len(cf.items)-1)
		last = clampInt(last, first, len(cf.items)-1)
		for i := first; i <= last; i++ {
			it := &cf.items[i]
			page := cf.pageOfItem(i)
			if page < 0 {
				continue
			}
			pi := &cf.pages[page]
			dx := l.opts.Margins.Left
			dy := l.opts.Margins.Top + it.y - pi.top
			switch it.kind {
			case itemImage:
				out = append(out, SelectionQuad{
					Page: cf.firstPage + page,
					Rect: paint.Rect{X: dx + it.x, Y: dy, Width: it.width, Height: it.height},
				})
			case itemLine:
				lo, hi := int(it.start), int(it.end)
				if ci == from.Chapter && i == first {
					lo = maxInt(lo, runeForLocation(cf, i, from))
				}
				if ci == to.Chapter && i == last {
					hi = minInt(hi, runeForLocation(cf, i, to))
				}
				if hi <= lo {
					continue
				}
				x0, x1 := l.lineRuneX(w, it, ci, lo, hi)
				out = append(out, SelectionQuad{
					Page: cf.firstPage + page,
					Rect: paint.Rect{X: dx + it.x + x0, Y: dy, Width: x1 - x0, Height: it.height},
				})
			}
		}
	}
	return out
}

// runeForLocation maps loc to an absolute paragraph rune index for item i.
func runeForLocation(cf *chapterFlow, item int, loc Location) int {
	it := &cf.items[item]
	if it.para == nil {
		return int(it.start)
	}
	for si := range it.para.segs {
		seg := &it.para.segs[si]
		if seg.node == loc.Node {
			return int(seg.start) + clampRuneOffset(seg, loc.Offset)
		}
	}
	return int(it.start)
}

func clampRuneOffset(seg *paraSeg, off int) int {
	n := int(seg.end) - int(seg.start)
	if n <= 0 {
		return 0
	}
	if off < 0 {
		return 0
	}
	if off >= n {
		return n - 1
	}
	return off
}

func (cf *chapterFlow) pageOfItem(item int) int {
	k := sort.Search(len(cf.pages), func(i int) bool { return cf.pages[i].last > item })
	if k >= len(cf.pages) {
		return len(cf.pages) - 1
	}
	if k < 0 {
		return -1
	}
	return k
}
