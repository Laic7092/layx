package render

import (
	"container/list"
	"hash/maphash"
	"sync"
)

// breakChapter is the reusable result of shaping and line breaking a chapter.
// It contains no vertical positions: those depend on PageKey and are applied
// by stackBreakUnits.
type breakChapter struct {
	key   keyHash
	units []breakUnit

	// Placements (node -> paragraph -> item) depend only on the break result,
	// not on vertical typography, so they are computed once per BreakKey.
	placeOnce sync.Once
	place     *chapterPlacements
}

// chapterPlacements is the BreakKey-scoped half of a chapterFlow.
type chapterPlacements struct {
	placements []paraPlacement
	nodePlace  map[NodeID]int32
	nodeFirst  map[NodeID]int32
	nodeAnchor map[NodeID]AnchorID
	anchors    map[AnchorID]anchorPos
}

// laidChapter is a chapter with vertical positions and a page map. It is
// produced from a breakChapter and reused while PageKey is unchanged.
type laidChapter struct {
	items  []flowItem
	paras  []*paragraph
	boxes  []blockRect
	height float32

	placements []paraPlacement
	nodePlace  map[NodeID]int32
	nodeFirst  map[NodeID]int32
	nodeAnchor map[NodeID]AnchorID
	anchors    map[AnchorID]anchorPos
	pages      []pageInfo
}

// cacheKey identifies one chapter variant. The cache itself is scoped to one
// document (it is created by Layout and carried through Relayout), so the
// document pointer is not part of the key. Fonts are, because Relayout may be
// called with a different FontSet for the same document.
type cacheKey struct {
	chapter int
	fonts   *FontSet
	key     keyHash
}

// lineKey identifies one shaped line. The cache is scoped to a single Layout,
// so a paragraph pointer is a stable identity for the lifetime of every entry.
// The chapter and rune range are included so relayout variants never mix lines
// whose shaping inputs could differ.
type lineKey struct {
	chapter int
	para    *paragraph
	start   int32
	end     int32
}

type lineEntry struct {
	key  lineKey
	runs []shapedRun
}

// lineCache is a bounded LRU of shaped, not-yet-justified lines. Justification
// depends on the line's available width and is applied by buildLine on every
// use, so a shape result can be shared by every consumer of the same Layout.
//
// The value stores shaped runs, not flow items and not positioned glyph runs:
// callers must treat the returned slice as read-only.
type lineCache struct {
	mu      sync.Mutex
	limit   int
	order   *list.List
	entries map[lineKey]*list.Element
}

func newLineCache(limit int) *lineCache {
	return &lineCache{
		limit:   limit,
		order:   list.New(),
		entries: make(map[lineKey]*list.Element, limit),
	}
}

func (c *lineCache) get(key lineKey) ([]shapedRun, bool) {
	if c == nil || c.limit <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*lineEntry).runs, true
}

func (c *lineCache) put(key lineKey, runs []shapedRun) {
	if c == nil || c.limit <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		e.Value.(*lineEntry).runs = runs
		c.order.MoveToFront(e)
		return
	}
	for c.order.Len() >= c.limit {
		back := c.order.Back()
		if back == nil {
			break
		}
		delete(c.entries, back.Value.(*lineEntry).key)
		c.order.Remove(back)
	}
	e := c.order.PushFront(&lineEntry{key: key, runs: runs})
	c.entries[key] = e
}

// variantCache keeps the most recent whole-document variants. Capacity is
// derived from the chapter count so that one full layout always fits in two
// variants; the contract is "the last 2-4 variants", not an unbounded cache.
type variantCache struct {
	mu sync.Mutex

	// Seed the digest per document chain so BreakKey/PageKey values stay
	// comparable across Relayout calls without any global state.
	seedA maphash.Seed
	seedB maphash.Seed

	breakLimit int
	breakMap   map[cacheKey]*breakChapter
	breakOrder []cacheKey

	pageLimit int
	pageMap   map[cacheKey]*laidChapter
	pageOrder []cacheKey

	// A Document is immutable and an Engine's Limits are fixed, so the
	// O(runes) limit scan only has to run once per document chain.
	limitsDone bool
}

// variantsPerChapter is the number of recent variants retained per chapter.
// Two is enough for the common A/B toggle (e.g. line height 1.5 <-> 1.8)
// without letting a large book's retained line boxes grow without bound.
const variantsPerChapter = 2

func newVariantCache(chapters int) *variantCache {
	limit := chapters * variantsPerChapter
	if limit < variantsPerChapter {
		limit = variantsPerChapter
	}
	return &variantCache{
		seedA:      maphash.MakeSeed(),
		seedB:      maphash.MakeSeed(),
		breakLimit: limit,
		breakMap:   make(map[cacheKey]*breakChapter, limit),
		pageLimit:  limit,
		pageMap:    make(map[cacheKey]*laidChapter, limit),
	}
}

func (c *variantCache) limitsVerified() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitsDone
}

func (c *variantCache) markLimitsVerified() {
	c.mu.Lock()
	c.limitsDone = true
	c.mu.Unlock()
}

func (c *variantCache) newKeyer() *keyer {
	return &keyer{seedA: c.seedA, seedB: c.seedB}
}

func (c *variantCache) getBreak(key cacheKey) *breakChapter {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.breakMap[key]
	if !ok {
		return nil
	}
	c.breakOrder = touchKey(c.breakOrder, key)
	return v
}

func (c *variantCache) putBreak(key cacheKey, v *breakChapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.breakMap[key]; exists {
		c.breakMap[key] = v
		return
	}
	for len(c.breakMap) >= c.breakLimit && len(c.breakOrder) > 0 {
		old := c.breakOrder[0]
		c.breakOrder = c.breakOrder[1:]
		delete(c.breakMap, old)
	}
	c.breakMap[key] = v
	c.breakOrder = append(c.breakOrder, key)
}

func (c *variantCache) getPage(key cacheKey) *laidChapter {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.pageMap[key]
	if !ok {
		return nil
	}
	c.pageOrder = touchKey(c.pageOrder, key)
	return v
}

func (c *variantCache) putPage(key cacheKey, v *laidChapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pageMap[key]; exists {
		c.pageMap[key] = v
		return
	}
	for len(c.pageMap) >= c.pageLimit && len(c.pageOrder) > 0 {
		old := c.pageOrder[0]
		c.pageOrder = c.pageOrder[1:]
		delete(c.pageMap, old)
	}
	c.pageMap[key] = v
	c.pageOrder = append(c.pageOrder, key)
}

// touchKey moves key to the most-recent end of a small LRU order slice.
func touchKey(order []cacheKey, key cacheKey) []cacheKey {
	for i := range order {
		if order[i] == key {
			copy(order[i:], order[i+1:])
			order[len(order)-1] = key
			return order
		}
	}
	return append(order, key)
}
