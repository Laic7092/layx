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
type Layout struct {
	engine *Engine
	doc    *Document
	opts   LayoutOptions

	chapters []chapterFlow
	pages    []pageInfo
	anchors  map[AnchorID]anchorPos

	pool *sync.Pool

	cacheMu    sync.Mutex
	cache      map[int]*Page
	cacheOrder []int
}

// Layout computes a new layout variant for doc.
func (e *Engine) Layout(ctx context.Context, doc *Document, opts LayoutOptions) (*Layout, error) {
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
	if err := e.checkLimits(ctx, doc); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	typo := opts.Typography
	defaultLang := language.NewLanguage(doc.meta.Language)
	plans, tasks, err := buildTasks(doc, opts, typo, defaultLang)
	if err != nil {
		return nil, err
	}

	results := make([]*blockBox, len(tasks))
	errs := make([]error, len(tasks))
	pool := &sync.Pool{New: func() any {
		w := newTextWorker(opts.Fonts, typo)
		w.defaultLang = defaultLang
		return w
	}}

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
				lc := &layoutContext{
					ctx:         ctx,
					doc:         doc,
					opts:        opts,
					typo:        typo,
					limits:      e.limits,
					chapter:     *ch,
					chapterIdx:  t.chapter,
					rootStyle:   t.rootStyle,
					defaultLang: defaultLang,
					worker:      w,
				}
				var (
					box     *blockBox
					taskErr error
				)
				if t.node >= 0 {
					box, taskErr = lc.layoutBlock(ctx, ch.Nodes, t.node, t.width, lc.rootStyle)
				} else {
					box, taskErr = lc.layoutInlineRun(ctx, t.tokens, t.style, t.width)
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

	l := &Layout{
		engine:  e,
		doc:     doc,
		opts:    opts,
		pool:    pool,
		cache:   make(map[int]*Page),
		anchors: make(map[AnchorID]anchorPos),
	}
	if err := l.assemble(ctx, plans, results, typo, defaultLang); err != nil {
		return nil, err
	}
	return l, nil
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

func buildTasks(doc *Document, opts LayoutOptions, typo Typography, defaultLang language.Language) ([]layoutPlan, []layoutTask, error) {
	var (
		tasks []layoutTask
		plans []layoutPlan
	)
	for ci := range doc.chapters {
		ch := &doc.chapters[ci]
		nodes := ch.Nodes
		root := &nodes[ch.Root]
		rootStyle := resolveStyle(doc.style(root.Style), typo)
		width := contentWidth(opts) - rootStyle.MarginLeft - rootStyle.MarginRight
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

func (l *Layout) assemble(ctx context.Context, plans []layoutPlan, results []*blockBox, typo Typography, defaultLang language.Language) error {
	l.chapters = make([]chapterFlow, len(plans))
	for ci := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		plan := &plans[ci]
		ch := l.doc.chapters[ci]
		units := make([]unit, 0, len(plan.units))
		for _, ti := range plan.units {
			box := results[ti]
			if box == nil {
				return fmt.Errorf("%w: chapter %q produced no layout", ErrInvalidDocument, ch.ID)
			}
			units = append(units, unit{
				items:        box.items,
				paras:        box.paras,
				xOffset:      box.xOffset,
				height:       box.height,
				marginTop:    box.marginTop,
				marginBottom: box.marginBottom,
				breakBefore:  box.breakBefore,
				breakAfter:   box.breakAfter,
				keepTogether: box.keepTogether,
			})
		}
		items, paras, height := stackUnits(units, typo.ParagraphGap)
		xShift := plan.rootStyle.MarginLeft
		yShift := plan.rootStyle.MarginTop
		for i := range items {
			items[i].x += xShift
			items[i].y += yShift
		}
		cf := chapterFlow{
			chapter:    ch,
			items:      items,
			paras:      paras,
			height:     height + yShift + plan.rootStyle.MarginBottom,
			contentTop: yShift,
			nodePlace:  make(map[NodeID]int32),
			nodeFirst:  make(map[NodeID]int32),
			nodeAnchor: make(map[NodeID]AnchorID),
			anchors:    make(map[AnchorID]anchorPos),
		}
		buildPlacements(&cf)
		for id, node := range ch.Anchors {
			pos := anchorPos{chapter: ci, node: node, offset: 0}
			cf.anchors[id] = pos
			if cur, exists := cf.nodeAnchor[node]; !exists || id < cur {
				cf.nodeAnchor[node] = id
			}
			if _, exists := l.anchors[id]; !exists {
				l.anchors[id] = pos
			}
		}
		cf.pages = paginate(ctx, cf.items, contentHeight(l.opts), typo)
		if err := ctx.Err(); err != nil {
			return err
		}
		for pi := range cf.pages {
			cf.pages[pi].chapter = ci
		}
		cf.firstPage = len(l.pages)
		l.pages = append(l.pages, cf.pages...)
		l.chapters[ci] = cf
	}
	return nil
}

func buildPlacements(cf *chapterFlow) {
	parents := make(map[NodeID]NodeID, len(cf.chapter.Nodes))
	for i := range cf.chapter.Nodes {
		for _, c := range cf.chapter.Nodes[i].Children {
			parents[c] = NodeID(i)
		}
	}
	byPara := make(map[*paragraph]int)
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

// PageCount returns the total number of pages.
func (l *Layout) PageCount() int { return len(l.pages) }

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

// PageOf returns the page containing loc.
func (l *Layout) PageOf(loc Location) (int, bool) {
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
// modified.
func (l *Layout) Relayout(ctx context.Context, opts LayoutOptions) (*Layout, error) {
	return l.engine.Layout(ctx, l.doc, opts)
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
				x0, x1 := lineRuneX(w, it, lo, hi)
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
