package render

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-text/typesetting/language"
	"github.com/laix/layx/render/paint"
)

// Location identifies a position inside a laid-out document.
//
// Node and Offset are valid for the lifetime of a Layout and are what hit
// testing, selection and search use. Anchor is a host-owned node identifier
// (or NoAnchor); persist Anchor and Offset together, because an anchor names a
// node and Offset names a rune inside it.
//
// When Anchor is non-zero, PageOf, Selection, Progress and OffsetOf resolve the
// location by the anchor and ignore Node. That is what lets a stable anchor
// survive a rebuilt Document whose node ids moved. An unknown anchor makes
// resolution fail rather than falling back to a possibly stale Node.
//
// Locations returned by Locate and HitTest always have both Node and Anchor
// filled in. When constructing a location from a persisted anchor alone, set
// Node to InvalidNode for clarity; resolution does not depend on it.
type Location struct {
	Chapter int
	Node    NodeID
	Anchor  AnchorID
	// Offset is a rune offset within Node's text.
	Offset int
}

// SelectionQuad is one highlighted rectangle. Width and Height are the same
// in both coordinate systems; only the origins differ.
type SelectionQuad struct {
	// Page is the global page index of the quad in paged mode. It is -1 in
	// scroll mode, where PageCount is 0 and pages are not part of the output.
	Page int
	// Rect is in page coordinates: page margins included, relative to the
	// top-left corner of Page. It is what a host paints on the page it got
	// from Layout.Page, and it is only usable in paged mode.
	Rect paint.Rect
	// Content is the same rectangle in the book content coordinate system
	// used by Layout.LocationAt, OffsetOf, HitTest and Tile.Top: x is measured
	// from the left edge of the viewport (page Margins.Left included, matching
	// the tile display lists) and y from the top of the book's body, across
	// chapters. It is defined in both modes and is the only rectangle a
	// scroll-mode host can use: the tile-local rectangle is Content shifted by
	// -Tile.Top(). In paged mode Content.X equals Rect.X and Content.Y is the
	// page slot top plus the in-page offset, i.e. the OffsetOf of the same
	// location. Note that Layout.HitTest takes x without the left margin, so
	// feed it Content.X - Margins.Left.
	Content paint.Rect
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

// laidUnit is one placed leaf break unit: a paragraph's lines or an image.
// Units are kept in flow order and their items are contiguous, so an item
// index maps to a unit by binary search over first.
type laidUnit struct {
	unit  *breakUnit
	x     float32
	first int32
	group int32
	flags uint8
}

// Flags of a laidUnit. They mirror the former per-item flowItem booleans.
const (
	unitBreakBefore uint8 = 1 << iota
	unitBreakAfter
	unitKeepTogether
)

// chapterFlow is a chapter with vertical positions and a page map.
//
// It does not store a flowItem per line. A line's y is the only value that
// depends on the vertical typography, so the flow is ys (4 bytes per item)
// plus the placed units; start, alignment, available width, baseline and the
// per-page flags are derived from the cached break data by item. A 5M-rune book
// keeps ~180k lines, and a full flowItem per line cost 20 MB for data that was
// already in the line boxes.
type chapterFlow struct {
	chapter Chapter
	units   []laidUnit
	ys      []float32
	// unitOf is the index in units of each item. It costs 4 bytes per item and
	// turns item lookup into one array read: locating the unit by binary search
	// on every item made page building, hit testing and pagination measurably
	// slower than the former materialized flow.
	unitOf []int32
	// lineHeight is Typography.LineHeight for this flow; line metrics are
	// re-derived from it when an item is materialized.
	lineHeight float32
	// boxes are block border/background rectangles in chapter content
	// coordinates. Unstyled documents leave this empty.
	boxes []blockRect

	placements []paraPlacement
	// nodePlace, nodeFirst and nodeAnchor are indexed by NodeID, or nil when
	// the chapter has no nodes of that kind. A negative entry means "absent".
	nodePlace  []int32
	nodeFirst  []int32
	nodeAnchor []AnchorID
	anchors    map[AnchorID]anchorPos
	height     float32
	pages      []pageInfo
	firstPage  int
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

	// pool is this Layout's worker pool: the Engine's cache entry for its font
	// set, shared with every other layout of the same fonts. Workers are leased
	// per page, tile or paragraph, never held for the layout's lifetime, so a
	// Layout needs no shutdown.
	pool *sync.Pool
	// lang is the document's default language, resolved once per layout.
	lang language.Language

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
	if e.measure {
		e.stats.layouts.Add(1)
		defer e.stop(time.Now(), &e.stats.total)
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
	// shaping, so it is cheap even for a 5M-rune book. Chapters are independent,
	// so the pass is parallel: each task builds a keyer from the cache's seeds,
	// and the digests are identical to a sequential pass.
	keyStart := e.mark()
	contentH := contentHeight(opts)
	breakKeys := make([]keyHash, nch)
	pageKeys := make([]keyHash, nch)
	seedA, seedB := variants.seeds()
	e.parallelFor(ctx, nch, func(ci int) {
		k := &keyer{seedA: seedA, seedB: seedB}
		ch := &doc.chapters[ci]
		breakKeys[ci] = chapterBreakKey(k, doc, ch, opts, typo)
		pageKeys[ci] = pageKey(k, breakKeys[ci], typo, contentH)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	currentPaintKey := paintKey(variants.newKeyer(), pageKeys, typo)
	e.stop(keyStart, &e.stats.keys)

	// Pass 2: find cached line boxes. Only chapters whose BreakKey is absent
	// need shaping.
	breakStart := e.mark()
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

	pool := e.workerPool(opts.Fonts)

	if anyBreak {
		// Phase A: plan each chapter. Planning resolves inherited styles and
		// tokenizes inline runs but shapes nothing, so it is cheap and its result
		// is a flat list of paragraphs to break.
		plans := make([]*chapterPlan, nch)
		planErrs := make([]error, nch)
		e.parallelFor(ctx, nch, func(ci int) {
			if !needBreak[ci] {
				return
			}
			explicit := explicitLineHeights(doc, &doc.chapters[ci])
			p, err := planChapter(doc, opts, typo, defaultLang, e.limits, ci, explicit)
			if err != nil {
				planErrs[ci] = err
				return
			}
			plans[ci] = p
		})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, err := range planErrs {
			if err != nil {
				return nil, err
			}
		}

		// Phase B: break every paragraph of every chapter from one flat task
		// list, so the load is balanced by text rather than by document shape.
		// A book whose whole body is a single block still fills every worker.
		var tasks []*paraTask
		for _, p := range plans {
			if p != nil {
				tasks = append(tasks, p.tasks...)
			}
		}
		if err := e.breakParagraphs(ctx, pool, tasks, opts, typo, defaultLang); err != nil {
			return nil, err
		}

		// Phase C: put the lines back into the plan's unit tree, in document
		// order, and publish the chapter.
		for ci, plan := range plans {
			if plan == nil {
				continue
			}
			units := make([]breakUnit, 0, len(plan.boxes))
			next := 0
			for bi := range plan.boxes {
				pb := &plan.boxes[bi]
				next = attachLines(pb.box.units, plan.tasks, next)
				u := unitFromBox(pb.box)
				if pb.inline {
					// A top-level inline task is not a block of its own: its box
					// is the chapter root. layChapter emits that root's border and
					// background once around the whole flow, so the wrapper must
					// not paint or re-apply root border offsets.
					cp := *pb.box
					cp.style = Style{}
					u.box = &cp
				}
				units = append(units, u)
			}
			if next != len(plan.tasks) {
				// The attach pass is the one place where a plan and its unit tree
				// must agree; a mismatch would silently drop lines, so report it.
				return nil, fmt.Errorf("%w: chapter %q attached %d of %d paragraphs",
					ErrInvalidDocument, doc.chapters[ci].ID, next, len(plan.tasks))
			}
			units = pruneUnits(units)
			bc := &breakChapter{key: breakKeys[ci], units: units}
			breaks[ci] = bc
			variants.putBreak(cacheKey{chapter: ci, fonts: opts.Fonts, key: breakKeys[ci]}, bc)
		}
	}
	e.stop(breakStart, &e.stats.brk)

	// Pass 3: reuse complete page maps when PageKey is unchanged, otherwise
	// re-stack the cached line boxes and re-paginate. Chapters are independent
	// (each paginates from its own top), so this is parallel too; the page
	// numbering is stitched together in order below.
	laid := make([]*chapterFlow, nch)
	needPage := make([]bool, nch)
	for ci := 0; ci < nch; ci++ {
		key := cacheKey{chapter: ci, fonts: opts.Fonts, key: pageKeys[ci]}
		if lc := variants.getPage(key); lc != nil {
			laid[ci] = lc
		} else {
			needPage[ci] = true
		}
	}
	pageErrs := make([]error, nch)
	e.parallelFor(ctx, nch, func(ci int) {
		if !needPage[ci] {
			return
		}
		ch := &doc.chapters[ci]
		root := &ch.Nodes[ch.Root]
		rootStyle := resolveStyle(doc.style(root.Style), typo)
		lc, err := e.layChapter(ctx, ch, ci, breaks[ci], rootStyle, typo, contentH, contentWidth(opts))
		if err != nil {
			pageErrs[ci] = err
			return
		}
		laid[ci] = lc
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, err := range pageErrs {
		if err != nil {
			return nil, err
		}
	}
	for ci := 0; ci < nch; ci++ {
		if needPage[ci] {
			variants.putPage(cacheKey{chapter: ci, fonts: opts.Fonts, key: pageKeys[ci]}, laid[ci])
		}
	}

	asmStart := e.mark()
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
		lang:      defaultLang,
	}
	l.chapters = make([]chapterFlow, nch)
	for ci := 0; ci < nch; ci++ {
		ch := &doc.chapters[ci]
		lc := laid[ci]
		cf := chapterFlow{
			chapter:    *ch,
			units:      lc.units,
			ys:         lc.ys,
			unitOf:     lc.unitOf,
			lineHeight: lc.lineHeight,
			boxes:      lc.boxes,
			placements: lc.placements,
			nodePlace:  lc.nodePlace,
			nodeFirst:  lc.nodeFirst,
			nodeAnchor: lc.nodeAnchor,
			anchors:    lc.anchors,
			height:     lc.height,
			pages:      lc.pages,
			firstPage:  len(l.pages),
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
	e.stop(asmStart, &e.stats.assemble)
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
) (*chapterFlow, error) {
	if bc == nil {
		return nil, fmt.Errorf("%w: chapter %q has no break result", ErrInvalidDocument, ch.ID)
	}
	stackStart := e.mark()
	bt, _, bb, bl := borderWidths(rootStyle)
	xShift := rootStyle.MarginLeft + bl
	yShift := rootStyle.MarginTop + bt
	lc := stackBreakUnits(bc.units, typo.ParagraphGap, typo.LineHeight, xShift, yShift)
	if boxPaints(rootStyle) {
		lc.boxes = append(lc.boxes, blockRect{
			style:  rootStyle,
			x:      rootStyle.MarginLeft,
			y:      rootStyle.MarginTop,
			width:  availW - rootStyle.MarginLeft - rootStyle.MarginRight,
			height: bt + lc.height + bb,
		})
	}
	sortBlockRects(lc.boxes)

	place := bc.placementsFor(ch, ci, lc)
	lc.placements = place.placements
	lc.nodePlace = place.nodePlace
	lc.nodeFirst = place.nodeFirst
	lc.nodeAnchor = place.nodeAnchor
	lc.anchors = place.anchors
	lc.height += yShift + rootStyle.MarginBottom
	e.stop(stackStart, &e.stats.stack)
	pageStart := e.mark()
	lc.pages = paginate(ctx, lc, contentH, typo)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for pi := range lc.pages {
		lc.pages[pi].chapter = ci
	}
	e.stop(pageStart, &e.stats.paginate)
	return lc, nil
}

// paraTask is one paragraph waiting for shaping. The plan phase resolves its
// style and width; the break phase fills lines; nothing else is written. A
// paragraph is independent of every other paragraph by construction, which is
// what makes it the right unit of parallel work.
type paraTask struct {
	para  *paragraph
	width float32
	style Style
	// lines is the break result, in the compact form the flow stores.
	lines []lineBreak
}

// setLines stores a paragraph's line boxes in the compact form the flow uses.
func (t *paraTask) setLines(lines []lineBox) {
	if len(lines) == 0 {
		return
	}
	t.lines = make([]lineBreak, len(lines))
	for li := range lines {
		ln := &lines[li]
		t.lines[li] = lineBreak{
			end:            ln.end,
			width:          ln.width,
			ascent:         clampF16(ln.ascent),
			descent:        clampF16(ln.descent),
			implicitSize:   ln.implicitSize,
			explicitHeight: ln.explicitHeight,
		}
	}
}

// planBox is one top-level box of a chapter. inline marks a run the chapter
// root wrapped directly, which must not paint the root's own box.
type planBox struct {
	box    *breakBox
	inline bool
}

// chapterPlan is what one chapter needs shaped. It is a pure function of the
// document and the layout options: it carries no vertical placement and no page
// geometry, only the unit tree with empty paragraph units and the flat task
// list that fills them.
type chapterPlan struct {
	boxes []planBox
	tasks []*paraTask
}

// planChapter resolves styles and tokenizes one chapter without shaping
// anything. It is the first of the three break phases (plan, break, attach) and
// its cost is O(nodes), not O(lines), so it never needs to be parallel itself.
func planChapter(doc *Document, opts LayoutOptions, typo Typography, lang language.Language, limits Limits, ci int, explicit map[NodeID]float32) (*chapterPlan, error) {
	ch := &doc.chapters[ci]
	nodes := ch.Nodes
	root := &nodes[ch.Root]
	rootStyle := resolveStyle(doc.style(root.Style), typo)
	_, rootRight, _, rootLeft := borderWidths(rootStyle)
	width := contentWidth(opts) - rootStyle.MarginLeft - rootStyle.MarginRight - rootLeft - rootRight
	if width <= 0 {
		return nil, fmt.Errorf("%w: chapter %q has non-positive content width", ErrInvalidOptions, ch.ID)
	}
	plan := &chapterPlan{}
	lc := &layoutContext{doc: doc, typo: typo}
	bc := &breakContext{
		doc:        doc,
		typo:       typo,
		limits:     limits,
		inline:     lc,
		explicitLH: explicit,
		plan:       plan,
	}
	flushInline := func(toks []inlineToken) error {
		if len(toks) == 0 {
			return nil
		}
		box, err := bc.inlineRun(toks, rootStyle, width)
		if err != nil {
			return err
		}
		plan.boxes = append(plan.boxes, planBox{box: box, inline: true})
		return nil
	}
	if root.Kind == NodeInline {
		var toks []inlineToken
		if err := lc.flattenInline(nodes, []NodeID{ch.Root}, rootStyle, root.Lang, &toks); err != nil {
			return nil, err
		}
		if err := flushInline(toks); err != nil {
			return nil, err
		}
		return plan, nil
	}
	var pending []inlineToken
	for _, child := range root.Children {
		if child < 0 || int(child) >= len(nodes) {
			return nil, fmt.Errorf("%w: child %d out of range", ErrInvalidDocument, child)
		}
		cn := &nodes[child]
		if cn.Kind == NodeBlock {
			if err := flushInline(pending); err != nil {
				return nil, err
			}
			pending = pending[:0]
			box, err := bc.block(nodes, child, width, rootStyle)
			if err != nil {
				return nil, err
			}
			plan.boxes = append(plan.boxes, planBox{box: box})
			continue
		}
		if err := lc.flattenInline(nodes, []NodeID{child}, rootStyle, root.Lang, &pending); err != nil {
			return nil, err
		}
	}
	if err := flushInline(pending); err != nil {
		return nil, err
	}
	return plan, nil
}

// breakParagraphs shapes and line-breaks a flat list of paragraphs on the
// engine's budget. Every task writes only its own slot, so the result does not
// depend on how many workers ran or in which order they finished.
func (e *Engine) breakParagraphs(ctx context.Context, pool *sync.Pool, tasks []*paraTask, opts LayoutOptions, typo Typography, lang language.Language) error {
	if len(tasks) == 0 {
		return nil
	}
	errs := make([]error, len(tasks))
	e.parallelFor(ctx, len(tasks), func(i int) {
		t := tasks[i]
		w := leaseWorker(pool, typo, opts.Hyphenator, lang)
		lines, err := w.wrapParagraph(ctx, t.para, t.width, typo, t.style, lang)
		releaseWorker(pool, w)
		if err != nil {
			errs[i] = err
			return
		}
		t.setLines(lines)
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("paragraph %d: %w", i, err)
		}
	}
	return nil
}

// attachLines moves paragraph line boxes onto the units that own them. Units
// are visited in the same document order the plan produced tasks in, so the
// match is a counter rather than a lookup.
func attachLines(units []breakUnit, tasks []*paraTask, next int) int {
	for i := range units {
		u := &units[i]
		switch u.kind {
		case breakLines:
			if next < len(tasks) && tasks[next].para == u.para {
				u.lines = tasks[next].lines
				next++
			}
		case breakBlockUnit:
			next = attachLines(u.box.units, tasks, next)
		}
	}
	return next
}

// pruneUnits drops paragraph units that produced no line box. The sequential
// path never appended them in the first place; deferring shaping moves that
// decision after the break phase.
func pruneUnits(units []breakUnit) []breakUnit {
	out := units[:0]
	for i := range units {
		u := units[i]
		if u.kind == breakLines && len(u.lines) == 0 {
			continue
		}
		if u.kind == breakBlockUnit {
			u.box.units = pruneUnits(u.box.units)
		}
		out = append(out, u)
	}
	return out
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
//
// It stores only what shaping and wrapping produced. The line's start is the
// previous line's end (wrapping never leaves a gap), its x and available width
// follow from the block style and whether it is the first line, and last/align
// follow from the line index and the paragraph style. Storing them cost 20
// bytes per line of a book with ~180k lines, for values that are rebuilt in a
// few instructions.
type lineBreak struct {
	end   int32
	width float32
	// ascent and descent are the shaper's line bounds in 1/64 px, which is
	// exactly what the fixed-point extents carry: no precision is lost.
	ascent  int16
	descent int16
	// implicitSize is the largest font size inheriting Typography.LineHeight;
	// explicitHeight is the largest size*lineHeight from overriding styles.
	implicitSize   float32
	explicitHeight float32
}

// imagePlacement is the cacheable form of an inline image unit. It is a small
// struct of its own: embedding a full flowItem here kept a 112-byte item alive
// per image, most of it line-only fields.
type imagePlacement struct {
	node             NodeID
	image            ImageID
	x, width, height float32
	link             string
}

// breakUnit is one vertical slot in a block: the cacheable result of shaping
// and line breaking, with vertical metrics still unapplied.
type breakUnit struct {
	kind  breakKit
	para  *paragraph
	lines []lineBreak
	image imagePlacement
	box   *breakBox

	xOffset      float32
	marginTop    float32
	marginBottom float32
	// gapBottom means marginBottom is the default paragraph gap, which must be
	// re-read from the current Typography when the unit is stacked.
	gapBottom    bool
	breakBefore  bool
	breakAfter   bool
	keepTogether bool
	widows       int16
	orphans      int16
}

// breakBox is a block's break units before they are stacked.
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
func lineMetrics(ln *lineBreak, lineHeight float32) (height, baseline float32) {
	ascent := float32(ln.ascent) / 64
	descent := float32(ln.descent) / 64
	lhMax := ln.explicitHeight
	if ln.implicitSize > 0 {
		if h := ln.implicitSize * lineHeight; h > lhMax {
			lhMax = h
		}
	}
	height = lhMax
	if minH := ascent + descent; height < minH {
		height = minH
	}
	leading := height - ascent - descent
	if leading < 0 {
		leading = 0
	}
	baseline = ascent + leading/2
	return height, baseline
}

// countBreakUnits sizes the flow item and unit slices up front. Without it,
// every nested block would append into its own growing slice and the parent
// would copy the result again: on a 5M-rune book that costs hundreds of
// megabytes per relayout.
func countBreakUnits(units []breakUnit) (items, leaves int) {
	for i := range units {
		u := &units[i]
		switch u.kind {
		case breakLines:
			items += len(u.lines)
			leaves++
		case breakImage:
			items++
			leaves++
		case breakBlockUnit:
			bi, bl := countBreakUnits(u.box.units)
			items += bi
			leaves += bl
		}
	}
	return items, leaves
}

// stackBreakUnits turns break units into the flow items a page or tile slices.
// Line heights are read from the current Typography here, which is why a
// line-height change reuses cached line boxes instead of re-breaking them.
func stackBreakUnits(units []breakUnit, defaultGap, lineHeight, x, y float32) *chapterFlow {
	nItems, nUnits := countBreakUnits(units)
	cf := &chapterFlow{
		lineHeight: lineHeight,
		ys:         make([]float32, 0, nItems),
		unitOf:     make([]int32, 0, nItems),
		units:      make([]laidUnit, 0, nUnits),
	}
	cf.height = appendBreakUnits(units, defaultGap, lineHeight, x, y, cf)
	return cf
}

// appendBreakUnits walks one level of the unit tree, placing every leaf unit at
// an absolute x/y. It appends to the flow's unit list, item tops and block
// boxes and returns the height of this level.
//
// Only the item's top is stored: line boxes, alignment, available width,
// baselines and the break/keep flags are all derivable from the cached break
// data, which is what lets a chapter flow cost 4 bytes per line instead of a
// full flowItem.
func appendBreakUnits(units []breakUnit, defaultGap, lineHeight, x, y float32, cf *chapterFlow) float32 {
	cursor := float32(0)
	prevBottom := float32(0)
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
		mark := len(cf.units)
		// The group only has to distinguish adjacent keep-together units.
		group := int32(mark)
		switch u.kind {
		case breakLines:
			ui := int32(len(cf.units))
			first := len(cf.ys)
			top := y + cursor
			for li := range u.lines {
				cf.ys = append(cf.ys, top)
				cf.unitOf = append(cf.unitOf, ui)
				h, _ := lineMetrics(&u.lines[li], lineHeight)
				top += h
			}
			cf.units = append(cf.units, laidUnit{
				unit: u, x: x, first: int32(first), group: group, flags: unitFlags(u),
			})
			cursor = top - y
		case breakImage:
			cf.ys = append(cf.ys, y+cursor)
			cf.unitOf = append(cf.unitOf, int32(len(cf.units)))
			cf.units = append(cf.units, laidUnit{
				unit: u, x: x, first: int32(len(cf.ys) - 1), group: group, flags: unitFlags(u),
			})
			cursor += u.image.height
		case breakBlockUnit:
			bTop, bRight, bBottom, bLeft := borderWidths(u.box.style)
			inner := appendBreakUnits(u.box.units, defaultGap, lineHeight, x+u.xOffset, y+cursor+bTop, cf)
			if boxPaints(u.box.style) {
				cf.boxes = append(cf.boxes, blockRect{
					style:  u.box.style,
					x:      x + u.xOffset - bLeft,
					y:      y + cursor,
					width:  u.box.width + bLeft + bRight,
					height: bTop + inner + bBottom,
				})
			}
			cursor += bTop + inner + bBottom
		}
		if len(cf.units) > mark {
			if u.breakBefore {
				cf.units[mark].flags |= unitBreakBefore
			}
			if u.breakAfter {
				cf.units[len(cf.units)-1].flags |= unitBreakAfter
			}
			if u.keepTogether {
				for k := mark; k < len(cf.units); k++ {
					cf.units[k].flags |= unitKeepTogether
					cf.units[k].group = group
				}
			}
		}
		prevBottom = bottom
	}
	return cursor
}

// unitFlags converts a unit's own break flags into laidUnit flags.
func unitFlags(u *breakUnit) uint8 {
	var f uint8
	if u.breakBefore {
		f |= unitBreakBefore
	}
	if u.breakAfter {
		f |= unitBreakAfter
	}
	if u.keepTogether {
		f |= unitKeepTogether
	}
	return f
}

// count returns the number of flow items in the chapter.
func (cf *chapterFlow) count() int { return len(cf.ys) }

// locate maps an item index to its unit and to the line index inside it.
func (cf *chapterFlow) locate(i int) (unit, line int) {
	u := cf.unitOf[i]
	return int(u), i - int(cf.units[u].first)
}

// item materializes one flow item from the placed units and the line boxes.
//
// The returned value is exactly what the stacking pass used to store, so every
// consumer (drawing, hit testing, selection, pagination) keeps its semantics.
func (cf *chapterFlow) item(i int) flowItem {
	if i < 0 || i >= len(cf.ys) {
		return flowItem{}
	}
	u, li := cf.locate(i)
	lu := &cf.units[u]
	var it flowItem
	if lu.unit.kind == breakImage {
		img := &lu.unit.image
		it = flowItem{
			kind: itemImage, node: img.node, image: img.image,
			x: lu.x + img.x, width: img.width, height: img.height, link: img.link,
		}
	} else {
		p := lu.unit.para
		lines := lu.unit.lines
		ln := &lines[li]
		start := int32(0)
		if li > 0 {
			start = lines[li-1].end
		}
		indent := float32(0)
		if li == 0 {
			if indent = p.style.FirstLineIndent; indent < 0 {
				indent = 0
			}
		}
		avail := p.width - indent
		height, baseline := lineMetrics(ln, cf.lineHeight)
		it = flowItem{
			kind: itemLine, para: p, start: start, end: ln.end,
			x:     lu.x + lineX(p.style.TextAlign, avail, ln.width) + indent,
			width: ln.width, avail: avail, height: height, baseline: baseline,
			last:     li == len(lines)-1,
			align:    p.style.TextAlign,
			paraLine: int32(li), paraLines: int32(len(lines)),
			widows: int(lu.unit.widows), orphans: int(lu.unit.orphans),
		}
		if li == 0 {
			it.breakBefore = lu.flags&unitBreakBefore != 0
		}
		if li == len(lines)-1 {
			it.breakAfter = lu.flags&unitBreakAfter != 0
		}
	}
	it.y = cf.ys[i]
	it.keepTogether = lu.flags&unitKeepTogether != 0
	it.keepGroup = lu.group
	return it
}

// itemExtent is the geometry hit testing needs, without materializing a whole
// flowItem: a page's hit test scans every item on it, and the full item is only
// built for the winner.
type itemExtent struct {
	kind                       itemKind
	x, y, width, avail, height float32
}

// itemExtent returns the extent of item i inside a placed unit. li is the
// line index inside that unit, which callers walking a unit already know.
func (cf *chapterFlow) unitExtent(u, li, i int) itemExtent {
	lu := &cf.units[u]
	if lu.unit.kind == breakImage {
		return itemExtent{kind: itemImage, x: lu.x + lu.unit.image.x, y: cf.ys[i],
			width: lu.unit.image.width, height: lu.unit.image.height}
	}
	p := lu.unit.para
	ln := &lu.unit.lines[li]
	indent := float32(0)
	if li == 0 {
		if indent = p.style.FirstLineIndent; indent < 0 {
			indent = 0
		}
	}
	avail := p.width - indent
	h, _ := lineMetrics(ln, cf.lineHeight)
	return itemExtent{
		kind: itemLine, x: lu.x + lineX(p.style.TextAlign, avail, ln.width) + indent,
		y: cf.ys[i], width: ln.width, avail: avail, height: h,
	}
}

// itemCount returns the number of items a placed unit contributes.
func (lu *laidUnit) itemCount() int {
	if lu.unit.kind == breakImage {
		return 1
	}
	return len(lu.unit.lines)
}

// itemBottom returns the bottom edge of an item without materializing it.
func (cf *chapterFlow) itemBottom(i int) float32 {
	u, li := cf.locate(i)
	if cf.units[u].unit.kind == breakImage {
		return cf.ys[i] + cf.units[u].unit.image.height
	}
	h, _ := lineMetrics(&cf.units[u].unit.lines[li], cf.lineHeight)
	return cf.ys[i] + h
}

// lineX returns a line's offset inside its available width for an alignment.
func lineX(align TextAlign, avail, width float32) float32 {
	switch align {
	case AlignRight:
		return avail - width
	case AlignCenter:
		return (avail - width) / 2
	}
	return 0
}

// breakContext is the per-task state for shaping and line breaking. It only
// produces break units; vertical placement happens later in layChapter.
type breakContext struct {
	doc        *Document
	typo       Typography
	limits     Limits
	inline     *layoutContext
	explicitLH map[NodeID]float32
	// plan collects the paragraphs this walk produces, in document order. The
	// units it returns carry the paragraph but no lines: shaping happens later,
	// in parallel, one task per entry of plan.tasks.
	plan *chapterPlan
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
				start := p.runeLen
				// The segment keeps the source string; the runes are
				// materialized on demand instead of being copied per paragraph.
				p.runeLen += int32(utf8.RuneCountInString(t.text))
				st := t.style
				// Vertical styling is re-applied at stack time. Store only the
				// document's explicit multiplier (zero means "inherit typo").
				st.LineHeight = bc.explicitLH[t.node]
				p.segs = append(p.segs, paraSeg{
					node: t.node, style: st, text: t.text,
					start: start, end: p.runeLen,
					lang: t.lang, link: t.link,
				})
			}
			u := breakUnit{
				kind:    breakLines,
				para:    p,
				widows:  clampI16(blockStyle.Widows),
				orphans: clampI16(blockStyle.Orphans),
			}
			if j < len(tokens) && tokens[j].kind == tokenBreak {
				u.marginBottom = 0
			} else {
				u.gapBottom = true
				u.marginBottom = bc.typo.ParagraphGap
			}
			units = append(units, u)
			bc.plan.tasks = append(bc.plan.tasks, &paraTask{para: p, width: width, style: blockStyle})
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
	it := imagePlacement{
		node: tok.node, image: tok.image,
		x: x, width: w, height: h, link: tok.link,
	}
	return breakUnit{kind: breakImage, image: it}, true, nil
}

// placementsFor computes the node/anchor maps once per BreakKey. Item order is
// identical for every PageKey derived from the same line boxes, so a
// LineHeight or paragraph-gap change does not repeat this O(lines) work.
func (bc *breakChapter) placementsFor(ch *Chapter, ci int, flow *chapterFlow) *chapterPlacements {
	bc.placeOnce.Do(func() {
		cf := chapterFlow{
			chapter:    *ch,
			units:      flow.units,
			ys:         flow.ys,
			unitOf:     flow.unitOf,
			nodePlace:  absentInt32(len(ch.Nodes)),
			nodeFirst:  absentInt32(len(ch.Nodes)),
			nodeAnchor: make([]AnchorID, len(ch.Nodes)),
			anchors:    make(map[AnchorID]anchorPos, len(ch.Anchors)),
		}
		buildPlacements(&cf)
		for id, node := range ch.Anchors {
			pos := anchorPos{chapter: ci, node: node, offset: 0}
			cf.anchors[id] = pos
			if cur := cf.nodeAnchor[node]; cur == NoAnchor || id < cur {
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

// absentInt32 returns a node-indexed table whose entries all mean "absent".
func absentInt32(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = -1
	}
	return out
}

func buildPlacements(cf *chapterFlow) {
	parents := make(map[NodeID]NodeID, len(cf.chapter.Nodes))
	for i := range cf.chapter.Nodes {
		for _, c := range cf.chapter.Nodes[i].Children {
			parents[c] = NodeID(i)
		}
	}
	byPara := make(map[*paragraph]int, len(cf.chapter.Nodes))
	cf.placements = make([]paraPlacement, 0, len(cf.chapter.Nodes))
	base := 0
	for ui := range cf.units {
		lu := &cf.units[ui]
		u := lu.unit
		if u.kind != breakLines {
			markNodeFirst(cf, parents, u.image.node, base)
			base++
			continue
		}
		pi, ok := byPara[u.para]
		if !ok {
			pi = len(cf.placements)
			byPara[u.para] = pi
			cf.placements = append(cf.placements, paraPlacement{para: u.para, firstItem: int(lu.first)})
		}
		cf.placements[pi].lineCount += len(u.lines)
		start := int32(0)
		for li := range u.lines {
			end := u.lines[li].end
			idx := base + li
			for si := range u.para.segs {
				seg := &u.para.segs[si]
				if int(seg.end) <= int(start) || int(seg.start) >= int(end) {
					continue
				}
				cf.nodePlace[seg.node] = int32(pi)
				markNodeFirst(cf, parents, seg.node, idx)
			}
			start = end
		}
		base += len(u.lines)
	}
}

func markNodeFirst(cf *chapterFlow, parents map[NodeID]NodeID, node NodeID, idx int) {
	for node != InvalidNode {
		if cur := cf.nodeFirst[node]; cur >= 0 {
			// Ancestors closer to the root were already visited at an earlier
			// or equal item index.
			if cur <= int32(idx) {
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

// pageItem is the part of a flow item pagination needs. Materializing a full
// flowItem would be wasteful in this loop: it runs once per line of the book.
type pageItem struct {
	y, height           float32
	para                *paragraph
	paraLine, paraLines int32
	breakBefore         bool
	breakAfter          bool
	keepTogether        bool
	keepGroup           int32
	widows, orphans     int
}

// pageView is the sequential view of a chapter flow used by paginate.
func (cf *chapterFlow) pageItem(i int) pageItem {
	u, li := cf.locate(i)
	lu := &cf.units[u]
	pi := pageItem{
		y:            cf.ys[i],
		keepTogether: lu.flags&unitKeepTogether != 0,
		keepGroup:    lu.group,
	}
	if lu.unit.kind == breakImage {
		pi.height = lu.unit.image.height
		return pi
	}
	lines := lu.unit.lines
	h, _ := lineMetrics(&lines[li], cf.lineHeight)
	pi.height = h
	pi.para = lu.unit.para
	pi.paraLine = int32(li)
	pi.paraLines = int32(len(lines))
	pi.widows = int(lu.unit.widows)
	pi.orphans = int(lu.unit.orphans)
	if li == 0 {
		pi.breakBefore = lu.flags&unitBreakBefore != 0
	}
	if li == len(lines)-1 {
		pi.breakAfter = lu.flags&unitBreakAfter != 0
	}
	return pi
}

// paginate slices a flowed chapter into pages. Page top values are absolute
// chapter content coordinates.
func paginate(ctx context.Context, cf *chapterFlow, pageHeight float32, typo Typography) []pageInfo {
	return paginateN(ctx, cf.count(), cf.pageItem, pageHeight, typo)
}

// paginateN is paginate over an item accessor, so the pagination rules can be
// tested without building a whole chapter flow.
func paginateN(ctx context.Context, n int, at func(int) pageItem, pageHeight float32, typo Typography) []pageInfo {
	typo.Rules = typo.Rules.withDefaults()
	if n == 0 {
		return []pageInfo{{chapter: 0, first: 0, last: 0, top: 0}}
	}
	const eps = 0.01
	pages := make([]pageInfo, 0, n/20+1)
	first := 0
	i := 0
	pageTop := float32(0)
	it := at(0)
	for i < n {
		if i%2048 == 0 && ctx.Err() != nil {
			return pages
		}
		if i > first && it.breakBefore {
			pages = append(pages, pageInfo{first: first, last: i, top: pageTop})
			first = i
			pageTop = it.y
			continue
		}
		if i > first && at(i-1).breakAfter {
			pages = append(pages, pageInfo{first: first, last: i, top: pageTop})
			first = i
			pageTop = it.y
			continue
		}
		limit := pageTop + pageHeight
		if i == first || it.y+it.height <= limit+eps {
			i++
			if i < n {
				it = at(i)
			}
			continue
		}

		breakAt := i
		// Keep the entire block together when possible.
		if it.keepTogether && it.keepGroup != 0 && i > first {
			start := i
			for start > first && at(start-1).keepTogether && at(start-1).keepGroup == it.keepGroup {
				start--
			}
			if start > first {
				breakAt = start
			}
		}
		// Widows and orphans only apply when the break is inside a paragraph.
		// Both counts are at least 1 here: paginate padded unset fields to the
		// CSS initial value, and a block-level zero fell back to those above.
		if breakAt == i && it.para != nil && it.paraLine > 0 {
			orphans := it.orphans
			if orphans <= 0 {
				orphans = typo.Rules.Orphans
			}
			widows := it.widows
			if widows <= 0 {
				widows = typo.Rules.Widows
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
				if cand > i && cand <= n {
					prev := at(cand - 1)
					if prev.y+prev.height <= limit+eps {
						breakAt = cand
					}
				}
			}
		}
		if breakAt <= first {
			breakAt = first + 1
		}
		pages = append(pages, pageInfo{first: first, last: breakAt, top: pageTop})
		first = breakAt
		pageTop = at(first).y
		i = breakAt
		if i < n {
			it = at(i)
		}
	}
	pages = append(pages, pageInfo{first: first, last: n, top: pageTop})
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

// Document returns the document this layout was built from. Relayout variants
// share the same pointer, so a host can use pointer identity to reject a stale
// search index or clear an image cache.
func (l *Layout) Document() *Document { return l.doc }

// Margins returns the page margins used by this layout.
func (l *Layout) Margins() paint.Insets { return l.opts.Margins }

// PageContentHeight returns the page viewport height after the top and bottom
// margins, i.e. Viewport.Height - Margins.Top - Margins.Bottom. It is the
// height of one page slot in paged mode and the inset height in scroll mode.
func (l *Layout) PageContentHeight() float32 { return contentHeight(l.opts) }

// Locate resolves a host anchor to the node it names. Offset is 0 because an
// anchor is node-level; set Location.Offset from a persisted position to
// address a rune inside the node. An anchor of NoAnchor is never found.
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
	loc, ok := l.resolveLocation(loc)
	if !ok {
		return 0, false
	}
	if loc.Chapter < 0 || loc.Chapter >= len(l.chapters) {
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

// LocationOfPage returns the location of the first flow item on page. It is
// the inverse of PageOf: PageOf(LocationOfPage(p)) == p for every non-blank
// page. Scroll layouts and blank pages report false.
func (l *Layout) LocationOfPage(page int) (Location, bool) {
	if l.opts.Mode == ModeScroll || page < 0 || page >= len(l.pages) {
		return Location{}, false
	}
	info := &l.pages[page]
	cf := &l.chapters[info.chapter]
	if info.first < 0 || info.first >= info.last || info.first >= cf.count() {
		return Location{}, false
	}
	return l.locationAtItem(cf, info.chapter, info.first)
}

// PageRange returns the global page range [first, first+count) that chapter
// occupies, and whether that range is meaningful.
//
// Pages are numbered across the whole book (see Layout.Page), so first is the
// page a host jumps to for the chapter and first+count-1 is its last page.
// Every laid-out chapter covers at least one page: a chapter without flow items
// still gets a blank page. Chapter c+1 starts at first+count, so the ranges
// tile [0, PageCount) without gaps or overlaps.
//
// PageRange is paged-mode only. Scroll layouts have no pages (PageCount returns
// 0), so they report ok=false; use ChapterContentTop for the chapter's start in
// scroll content coordinates.
func (l *Layout) PageRange(chapter int) (first, count int, ok bool) {
	if l.opts.Mode == ModeScroll || chapter < 0 || chapter >= len(l.chapters) {
		return 0, 0, false
	}
	cf := &l.chapters[chapter]
	return cf.firstPage, len(cf.pages), true
}

// ChapterPages returns the first global page of every chapter in document
// order, or nil in scroll mode, where pages are not part of the output.
//
// ChapterPages()[i] equals the first value of PageRange(i), so a host can build
// a table of contents or a progress bar without walking pages itself.
func (l *Layout) ChapterPages() []int {
	if l.opts.Mode == ModeScroll {
		return nil
	}
	out := make([]int, len(l.chapters))
	for i := range l.chapters {
		out[i] = l.chapters[i].firstPage
	}
	return out
}

// ChapterContentTop returns the content y where chapter starts, in the
// coordinate system of LocationAt, OffsetOf and Progress.
//
// The value is the top of the chapter's own flow box: the chapter's root block
// margin belongs to the chapter, so the first line box of a chapter with a root
// MarginTop sits below this y. Chapter 0 starts at y=0 in both modes.
//
//   - Scroll mode: the chapter's position in the continuous stream.
//   - Paged mode: the top of the chapter's first page slot, i.e.
//     PageRange(chapter).first * contentHeight, where contentHeight is the
//     viewport height minus the top and bottom margins. Paged content
//     coordinates are page slots, so the value snaps to a page boundary.
//
// The method exists for both modes so a host can build a chapter list once and
// resolve it to pages (paged) or to a scroll position (scroll). Consecutive
// values, plus ContentHeight for the last chapter, give each chapter's extent
// without a second accessor.
func (l *Layout) ChapterContentTop(chapter int) (float32, bool) {
	if chapter < 0 || chapter >= len(l.chapters) {
		return 0, false
	}
	if l.opts.Mode == ModeScroll {
		return l.chapterTop[chapter], true
	}
	return float32(l.chapters[chapter].firstPage) * contentHeight(l.opts), true
}

// resolveLocation resolves a location to a laid-out node. A non-zero anchor is
// authoritative: it is the only identifier that stays meaningful after the
// document is rebuilt. Node is a fast path for locations without anchors.
func (l *Layout) resolveLocation(loc Location) (Location, bool) {
	if loc.Anchor != NoAnchor {
		pos, ok := l.anchors[loc.Anchor]
		if !ok {
			return Location{}, false
		}
		loc.Chapter = pos.chapter
		loc.Node = pos.node
		// Keep loc.Offset: an anchor names the node, the caller may add a rune
		// position inside it.
		return loc, true
	}
	if loc.Chapter >= 0 && loc.Chapter < len(l.chapters) && loc.Node != InvalidNode {
		if nf := l.chapters[loc.Chapter].nodeFirst; int(loc.Node) < len(nf) && nf[loc.Node] >= 0 {
			return loc, true
		}
	}
	return Location{}, false
}

func (cf *chapterFlow) itemOf(loc Location) (int, bool) {
	if pi, ok := cf.nodePlaceAt(loc.Node); ok && int(pi) < len(cf.placements) {
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
			return int(cf.itemEnd(pl.firstItem+i)) > target
		})
		if k >= n {
			k = n - 1
		}
		return pl.firstItem + k, true
	}
	if idx, ok := cf.nodeFirstAt(loc.Node); ok {
		return int(idx), true
	}
	return 0, false
}

// nodePlaceAt returns the paragraph placement index of a node, if it has one.
func (cf *chapterFlow) nodePlaceAt(node NodeID) (int32, bool) {
	if node < 0 || int(node) >= len(cf.nodePlace) {
		return 0, false
	}
	pi := cf.nodePlace[node]
	return pi, pi >= 0
}

// nodeFirstAt returns the first item index of a node, if it has one.
func (cf *chapterFlow) nodeFirstAt(node NodeID) (int32, bool) {
	if node < 0 || int(node) >= len(cf.nodeFirst) {
		return 0, false
	}
	idx := cf.nodeFirst[node]
	return idx, idx >= 0
}

// nodeAnchorOf returns the anchor assigned to a node, or NoAnchor.
func (cf *chapterFlow) nodeAnchorOf(node NodeID) AnchorID {
	if node < 0 || int(node) >= len(cf.nodeAnchor) {
		return NoAnchor
	}
	return cf.nodeAnchor[node]
}

// itemEnd returns the paragraph rune offset where an item ends. Images have no
// runes; they report 0.
func (cf *chapterFlow) itemEnd(i int) int32 {
	u, li := cf.locate(i)
	if cf.units[u].unit.kind != breakLines {
		return 0
	}
	return cf.units[u].unit.lines[li].end
}

// Relayout computes a new layout with different options. The receiver is not
// modified. Line boxes and page maps are reused when their keys are unchanged.
func (l *Layout) Relayout(ctx context.Context, opts LayoutOptions) (*Layout, error) {
	return l.engine.layoutDoc(ctx, l.doc, opts, l.variants)
}

// WithColors returns the same layout printed in another paper: ink on
// background, with link text in link. The three are paint-only — they never
// reach BreakKey or PageKey — so no line is shaped and no page moves; the pages
// and tiles already drawn are forgotten and the next Draw paints them again.
//
// It is the theme switch: it costs a copy of the layout, not a pass over the
// book. The result shares the receiver's flow, pages, anchors and variant cache,
// so a later Relayout of the recoloured layout reuses them too.
func (l *Layout) WithColors(ink, background, link paint.Color) *Layout {
	opts := l.opts
	opts.Typography.Color = ink
	opts.Typography.Background = background
	opts.Typography.LinkColor = link
	opts.Typography = opts.Typography.withDefaults()

	n := &Layout{
		engine:    l.engine,
		doc:       l.doc,
		opts:      opts,
		chapters:  l.chapters,
		pages:     l.pages,
		anchors:   l.anchors,
		variants:  l.variants,
		breakKeys: l.breakKeys,
		pageKeys:  l.pageKeys,
		paintKey:  paintKey(l.variants.newKeyer(), l.pageKeys, opts.Typography),
		pool:      l.engine.workerPool(opts.Fonts),
		lineCache: newLineCache(l.engine.lineCache),
		cache:     make(map[int]*Page),
		lang:      l.lang,
	}
	n.chapterTop, n.contentHeight = l.chapterTop, l.contentHeight
	n.tileHeight, n.tileCount = l.tileHeight, l.tileCount
	if opts.Mode == ModeScroll {
		n.tileCache = make(map[int]*Tile)
	}
	return n
}

// leaseWorker borrows a shaping worker configured for this layout variant.
// Every page, tile, hit test and selection takes one for the duration of the
// call, so workers are never held across an API boundary and a Layout has no
// shutdown to perform.
func (l *Layout) leaseWorker() *textWorker {
	return leaseWorker(l.pool, l.opts.Typography, l.opts.Hyphenator, l.lang)
}

func (l *Layout) releaseWorker(w *textWorker) {
	releaseWorker(l.pool, w)
}

// Selection returns the rectangles covering the rune range from..to.
// Cross-chapter selections are supported; locations are ordered by chapter.
//
// It is available in both modes. Paged hosts paint each quad on its Page using
// Rect; scroll hosts use Content and subtract the tile top. In paged mode the
// result is unchanged by the mode dimension: Page and Rect describe the same
// pages as before, and Content is added alongside them.
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
	w := l.leaseWorker()
	defer l.releaseWorker(w)

	var out []SelectionQuad
	for ci := from.Chapter; ci <= to.Chapter; ci++ {
		cf := &l.chapters[ci]
		n := cf.count()
		if n == 0 {
			continue
		}
		first, last := 0, n-1
		if ci == from.Chapter {
			first = a
		}
		if ci == to.Chapter {
			last = b
		}
		first = clampInt(first, 0, n-1)
		last = clampInt(last, first, n-1)
		for i := first; i <= last; i++ {
			itv := cf.item(i)
			it := &itv
			page := cf.pageOfItem(i)
			if page < 0 || page >= len(cf.pages) {
				continue
			}
			pi := &cf.pages[page]
			pageNo := cf.firstPage + page
			if l.opts.Mode == ModeScroll {
				// Pages are not exposed in scroll mode; a page number would
				// only be a stale implementation detail.
				pageNo = -1
			}
			dx := l.opts.Margins.Left
			dy := l.opts.Margins.Top + it.y - pi.top
			cy := l.contentYOfItem(cf, ci, page, it.y)
			emit := func(x, width, height float32) {
				x += dx
				out = append(out, SelectionQuad{
					Page:    pageNo,
					Rect:    paint.Rect{X: x, Y: dy, Width: width, Height: height},
					Content: paint.Rect{X: x, Y: cy, Width: width, Height: height},
				})
			}
			switch it.kind {
			case itemImage:
				emit(it.x, it.width, it.height)
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
				emit(it.x+x0, x1-x0, it.height)
			}
		}
	}
	return out
}

// runeForLocation maps loc to an absolute paragraph rune index for item i.
func runeForLocation(cf *chapterFlow, item int, loc Location) int {
	itv := cf.item(item)
	it := &itv
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
