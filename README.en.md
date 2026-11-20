# layx

[![CI](https://github.com/Laic7092/layx/actions/workflows/ci.yml/badge.svg)](https://github.com/Laic7092/layx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/laic7092/layx/render.svg)](https://pkg.go.dev/github.com/laic7092/layx/render)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**English** · [中文](README.md)

layx is a pure-Go reflowable layout kernel that produces display lists. Its input
is a `render.Document` that the host has already parsed and cascaded; its output
is pagination, display lists, and hit-test/selection/anchor mappings. **It does
not parse EPUB/XHTML/CSS and ships no rasterizer.**

```text
host / compiler                         this library
EPUB / OPF / spine / CSS        →       render.Document
XHTML / selectors / cascade             ↓
                                        shaping / line breaking / block layout / pagination
                                        ↓
                                        Page + DisplayList + HitTest/Selection
                                        ↓
host painter (GPU / software / e-ink)
```

Implemented: paged and scrolling output paths, three-layer layout caches, shaped
line cache, tile cache, hit testing / cross-page selection / anchor mapping, CJK
line breaking and punctuation policy, pluggable hyphenation, borders/backgrounds/
text decoration. Layout keeps about **4 B per rune** for the whole book (document
and fonts excluded).

Design decisions are in [`docs/design.md`](docs/design.md); punctuation/line-breaking
policy, default-value sources and the recommended switch set for Chinese body text
are in [`docs/text-rules.md`](docs/text-rules.md) (Chinese). API reference:
[`docs/api.md`](docs/api.md).

## Install

Two independently released modules; fetch only what you need:

```sh
go get github.com/laic7092/layx/render@v0.1.0    # layout core (required)
go get github.com/laic7092/layx/epub@v0.1.0      # EPUB/XHTML/CSS loader (optional)
```

`epub` is a nested module, tagged as `epub/v0.x.y`; it requires a published
version of the root module and has no `replace`, so `go get` works for external
consumers. Both modules are released together; upgrade them together.

Minimum Go versions: **Go 1.21** for the root module (the language/API level the
`render` code actually needs) and **Go 1.26** for the `epub` module (required by
`golang.org/x/net`). The repository commits a `go.work` listing both modules, so
one Go 1.26+ toolchain is enough for development:

```sh
go test ./...              # root module (workspace mode)
(cd epub && go test ./...) # epub module
```

External consumers never see `go.work`; they resolve the released module versions.

## Packages

```text
github.com/laic7092/layx/render             core: IR, fonts, layout, pagination, hit test, selection
github.com/laic7092/layx/render/paint       drawing contract and basic types (no rasterizer)
github.com/laic7092/layx/render/hyphen/en   optional TeX English hyphenation; the core does not import it
github.com/laic7092/layx/epub               EPUB/XHTML/CSS → render.Document (nested module)
examples/layout                         minimal example: lay out and print page count
examples/wasm                           wasm + Canvas 2D prototype (benchmark path included)
tools/{bench_native,memreport,snapshotimg,genepub}   developer tools, not part of the library
```

`epub/` has its own `go.mod` and [`epub/README.md`](epub/README.md); it is not
part of the root `go test ./...`. Direct dependencies are only
`go-text/typesetting` (shaping / UAX #14 line breaking) and
`golang.org/x/image/math/fixed`; the `image/*` and `encoding/xml` packages in
`go list -deps ./render` come in transitively through
`go-text/typesetting/font`. `render/deps_test.go` gates direct imports.

## Quick start

```go
eng, _ := render.New(render.Options{Workers: 0}) // 0 = GOMAXPROCS; a whole-Engine concurrency budget

fonts, _ := render.NewFontSet(render.FontFace{
    Family: "serif",
    Weight: 400,
    Data:   serifTTF,
})

layout, err := eng.Layout(ctx, doc, render.LayoutOptions{
    Viewport: render.Viewport{Width: 600, Height: 900},
    Typography: render.Typography{
        FontFamily: "serif",
        FontSize:   18,
        LineHeight: 1.6,
        Color:      paint.Color{A: 0xff},
        Rules: render.TextRules{
            PunctuationCompression: true,
            Widows:                 2,
            Orphans:                2,
        },
    },
    Fonts:   fonts,
    Margins: render.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
})
page := layout.Page(0) // built on first access, then LRU-cached per Options.PageCache
page.Draw(painter)     // replay only: no re-layout, no whole-book shaping
```

`Document` is built with `DocumentBuilder`:

```go
b := render.NewDocumentBuilder().
    SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}}).
    SetMetadata(render.Metadata{Language: "en", Reflowable: true})

err := b.AddChapter(render.Chapter{
    ID:   "ch1",
    Root: 0,
    Nodes: []render.Node{
        {Kind: render.NodeBlock, Children: []render.NodeID{1}},
        {Kind: render.NodeText, Style: 1, Text: "Body text..."},
    },
    Anchors: map[render.AnchorID]render.NodeID{100: 1},
})
doc, err := b.Build()
```

The engine makes no aesthetic decisions for you: punctuation compression,
hanging punctuation, widow/orphan protection and CJK/Latin spacing are **policies**
whose zero value is the spec default, so punctuation policies are off by default
while widows/orphans default to the CSS initial value 2/2. See
[`docs/text-rules.md`](docs/text-rules.md#中文正文的推荐开关组) for the recommended
Chinese-body switch set.

## Two output modes

`LayoutOptions.Mode` picks the output; line breaking and block layout are shared,
only the window extraction differs.

Paged (`ModePaged`, the default):

```go
layout.PageCount()
page := layout.Page(i) // nil when out of range; built on first access, then LRU
page.Draw(painter)
page.HitTest(pt)
page.Links()
```

Scrolling (`ModeScroll`):

```go
render.LayoutOptions{Mode: render.ModeScroll, TileHeight: 1024} // 0 = Viewport.Height
layout.ContentHeight()
layout.TileCount()
tile := layout.Tile(i) // nil when out of range; built on first access, then LRU
layout.Tiles(lo, hi)   // build a tile range in parallel (tiles are independent)
layout.Prefetch(ctx, lo, hi)                     // paged = pages, scroll = tiles; parallel
pf := layout.StartPrefetch(ctx, lo, hi)          // same, on an explicit handle's goroutine
err := pf.Wait()
```

**A tile is a display list, not a bitmap:** `render` has no rasterizer, so
whether to cache tiles as pixels is the host's decision.

Content coordinates: in scroll mode `y = 0` is the top of the book body, chapters
stack continuously, and `Margins.Top/Bottom` are viewport insets only; in paged
mode pages are laid end to end into one virtual stream. Each line box belongs to
exactly one tile (assigned by its top edge); images may span tiles and the painter
clips them.

A host painter may implement the optional `paint.Clipped` interface
(`Visible() paint.Rect`): `Page.Draw` / `Tile.Draw` then skip ops that do not
intersect the visible region, and every glyph op carries its line-box rectangle.
Painters that do not implement it still receive every op; replay semantics are
unchanged.

## Concurrency and budget

`Options.Workers` (default `GOMAXPROCS`) is a **concurrency budget for the whole
Engine**, not a per-call cap:

- Concurrent `Layout` / `Relayout` / `Prefetch` calls share it, split by the
  number of active phases. A long layout that starts first cannot starve the
  machine: surplus helpers hand their budget back after the current task (tens of
  microseconds) and the later call picks it up immediately. Measured: a foreground
  small layout while a background whole-book relayout runs went from 4.2 ms to
  1.6–2.1 ms (1.27 ms alone).
- The caller of a phase always participates; helpers never block waiting for
  budget, so **deadlock is impossible**, and nested phases (a single page inside a
  prefetch) degrade to running in the caller when no budget is available.
- Apart from the goroutine explicitly returned by `StartPrefetch`, the library
  starts no background goroutines: every helper is joined before the phase
  returns. `Engine.Close()` merely drops reusable workers (face caches, shaper,
  scratch); calling it is optional, and the Engine stays usable afterwards.

**The unit of line-breaking work is the paragraph, not the block.** `Layout`
breaks text in three stages: plan chapters in parallel (style inheritance, inline
tokenizing, block tree; no shaping), flatten all book paragraphs into one task list
that workers pull from, then attach line boxes back to the block tree sequentially.
A book that wraps a whole chapter in one `<div>` therefore still saturates every
core instead of pinning one worker.

With `Options{Measure: true}`, `Stats` also reports `Tasks` (work items handed to
parallel phases), `Busy` (busy time per worker slot) and `Imbalance`
((busiest−idlest)/busiest). Imbalance is the load-balance check: with
paragraph-granular tasks it is close to 0.

## Positions and coordinates

- `Location{Chapter, Node, Anchor, Offset}`: `Node`/`Offset` are for hit testing,
  selection and search inside the current layout; `Anchor` is a host-persistable
  node-level identifier (`0` = none). **Persist `Anchor` + `Offset`:** a non-zero
  `Anchor` takes precedence over `Node`, so rebuilding a `Document` does not let a
  stale node number shadow the stable anchor. Anchors are laid on blocks,
  paragraphs and headings; cost is O(#anchors).
- `Locate(anchor)` → `Location` (node only, `Offset` 0), `PageOf(loc)` ↔
  `LocationOfPage(page)`; `PageRange(chapter)` → `[first, first+count)`, and
  `ChapterPages()` gives each chapter's first page. Scroll mode has no pages; use
  `ChapterContentTop(chapter)` instead.
- Hit testing and selection: `Page.HitTest(pt)` takes **page-local** coordinates
  (margins included), while `Layout.HitTest(pt)` takes **content coordinates** in
  both modes (x without `Margins.Left`, y measured across the book body); both
  return the same `Location` type. `Layout.Selection(from, to)` returns rectangles.
  A hit test with nothing to hit (empty chapter/page or empty layout) returns
  `(Location{}, false)` — no panic. `SelectionQuad` carries two coordinate
  systems: `Rect` is page-local (paged mode only) and `Content` is book content
  coordinates (both modes; subtract `Tile.Top()` for tile-local coordinates in
  scroll mode).
- `LocationAt(y)` ↔ `OffsetOf(loc)` share one content coordinate system in both
  modes and are inverses of each other. `Progress(loc)` ∈ [0,1]: paged uses
  "page + in-page offset", scroll uses `y / ContentHeight`; the denominators
  differ, so the same position has slightly different percentages — **do not
  persist percentages**.
- `Document.Text(chapter)` concatenates the chapter's text in reading order and
  returns each node's rune range (non-text nodes have zero width), so search and
  copy do not have to walk `NodeText` nodes themselves. It inserts no paragraph
  separators.
- Pointer identity of `Layout.Document()` tells a host whether its index/image
  caches are stale; `Margins()` / `PageContentHeight()` save the host from
  re-deriving page margins and content height.

## Out-of-range and empty-result semantics

One rule: **when a lookup cannot resolve, return `(T, false)` or `nil` — never
panic, never a half-built object.** Calls returning `bool` return the zero value
on failure; calls returning a pointer return `nil`; calls returning a slice return
`nil` or an empty slice (always safe to `range`). The full table is in
[`docs/api.md`](docs/api.md).

| Call | Semantics |
|---|---|
| `Document.Chapter(i)` | `(Chapter, bool)`; out of range → `false` |
| `Document.Text(chapter)` | `(string, []TextRun, bool)`; out of range → `false` |
| `Layout.Page(n)` | out of range / not available in this mode → `nil` (always `nil` in scroll mode) |
| `Layout.Tile(i)` | out of range / paged mode → `nil` |
| `Layout.Tiles(lo, hi)` | clamped to `[0, TileCount)`; empty range → `nil` |
| `Layout.Locate(anchor)` | unknown anchor → `(Location{}, false)` |
| `Layout.PageOf(loc)` / `LocationOfPage(page)` | unresolvable → `(0, false)` / `(Location{}, false)` |
| `Layout.LocationAt(y)` / `OffsetOf(loc)` / `Progress(loc)` | unresolvable → `(0, false)` |
| `Layout.PageRange(chapter)` / `ChapterContentTop(chapter)` | out of range → `(0, 0, false)` / `(0, false)` |
| `Layout.ChapterPages()` | one entry per **document** chapter: its first global page, or `-1` for chapters outside this layout (range layout); `nil` in scroll mode |
| `Layout.HitTest(pt)` / `Page.HitTest(pt)` | nothing to hit (empty chapter / empty layout) → `(Location{}, false)` |
| `Layout.Selection(from, to)` | either end unresolvable → `nil` (empty selection) |
| `Layout.Issues()` | empty slice (`len == 0`) when there are no diagnostics; safe to `range` |
| `Layout.Document()` | always non-`nil`; `PageCount` / `TileCount` / `ContentHeight` always return a value (0 in the other mode) |
| `Book.ResolveLink(url)` | external URL / broken link → `(LinkTarget{}, false)` |

`Page(n)` / `Tile(i)` returning `nil` is the only "check before dereferencing"
convention; everything else reports through `bool`.

## Diagnostics and errors

The library no longer degrades silently: **what it decides to drop or substitute
is reported**, and diagnostics never change layout results.

```go
eng, _ := render.New(render.Options{
    // Called once per issue before Layout returns, on the caller's goroutine.
    OnIssue: func(is render.Issue) { log.Printf("layout: %s", is) },
})
layout, err := eng.Layout(ctx, doc, opts)
if err != nil {
    return err
}
for _, is := range layout.Issues() { // the complete list of that call (a copy)
    log.Printf("%s chapter=%q node=%d %s", is.Code, is.Chapter, is.Node, is.Detail)
}
```

| `IssueCode` | Meaning |
|---|---|
| `IssueMissingFont` | the requested `FontFamily` has no face in the `FontSet`; the text was laid out with a fallback face |
| `IssueMissingGlyphs` | no configured face can draw these runes (tofu); `Detail` lists up to 8 code points plus a count |
| `IssueDroppedContent` | content the IR cannot express or lacks data for, so it was dropped (an image without bytes); the `epub` layer uses it for linearized tables, dropped images, ruby, etc. |
| `IssueSuppressed` | summary entry when the list is capped (1024 entries per call); `Detail` holds the number not retained |

`epub.Options.OnIssue func(render.Issue)` and `Book.Issues()` reuse the same
`IssueCode` values and delivery semantics, reporting what the EPUB → IR conversion
linearized, dropped or ignored (tables, images, ruby, `@font-face`, ...). Hosts can
also query font coverage before layout, keeping tofu out of pagination:

```go
fonts.HasFamily("serif")                          // is the family present?
fonts.Coverage('好')                               // does any face have a glyph for it?
face, ok := fonts.Resolve(render.Style{FontFamily: "serif"}, '好') // the face layout would pick
```

**Errors.** Every error wraps one of six sentinels — `ErrInvalidDocument`,
`ErrInvalidOptions`, `ErrNoFonts`, `ErrLimitExceeded`, `ErrClosed`,
`ErrInternal` — so `errors.Is` works; for location use `errors.As` to get a
`*render.DocumentError`:

```go
var de *render.DocumentError
if errors.As(err, &de) {
    log.Printf("kind=%v chapter=%q node=%d style=%d detail=%s",
        de.Kind, de.Chapter, de.Node, de.Style, de.Detail)
}
```

- **Panic contract:** every input ends as an `error`; the host process is never
  crashed. A panic inside a worker is recovered into an error wrapping
  `ErrInternal`, which means **a library bug**, not an invalid document. Panics
  from the host's `Painter` inside `Page.Draw` / `Tile.Draw` are *not* recovered:
  a painter must handle its own failures at its own boundary.
- **NaN/±Inf are rejected:** `LayoutOptions` viewport, margins, `TileHeight` and
  `Typography` numbers, plus every numeric field of every `Style` passed to
  `DocumentBuilder`, are checked for finiteness. A non-finite value returns
  `ErrInvalidOptions` (styles fail at `Build` with `ErrInvalidDocument` and style
  context) instead of flowing into geometry or page counts.

### Resource limits

`Limits` are **hard caps for a single layout**: exceeding one returns
`ErrLimitExceeded` with the measured value and the cap instead of allocating until
OOM. A zero field uses its default. They apply to `Layout`, `Relayout` and
range layouts, including cache hits.

| Field | Default | What it caps |
|---|---|---|
| `MaxNodes` | 1,048,576 | nodes visited by one layout |
| `MaxDepth` | 256 | chapter tree depth |
| `MaxTextRunes` | 20,000,000 | text runes |
| `MaxImagePixels` | 67,108,864 | total image pixels |
| `MaxPages` | 100,000 | pages in paged mode |
| `MaxTiles` | 100,000 | tiles in scroll mode (content height / `TileHeight`) |
| `MaxLines` | 2,000,000 | line boxes |

`MinTileHeight = 16`: a non-zero `TileHeight` below it returns
`ErrInvalidOptions` (otherwise one extra pixel of content adds another tile). A
host with genuinely larger documents can raise the caps explicitly and own the
memory cost; very large reference works should use range layout first.

### Range layout

```go
layout, err := eng.LayoutRange(ctx, doc, opts, 10, 20) // half-open [10, 20)
// equivalent to opts.Range = &render.ChapterRange{First: 10, Last: 20} before Layout
```

- Only chapters in the range are laid out; nothing outside it produces pages or
  lines. `Locate` / `PageOf` / `LocationOfPage` / `OffsetOf` / `LocationAt` never
  resolve outside the range (`false`); `ChapterPages()` puts `-1` in the entries
  of chapters outside the range.
- Page numbers start at 0 within the range; `Location.Chapter` keeps the
  document's chapter index, not an offset inside the range.
- Scroll-mode content coordinates start at 0 for the first included chapter;
  `Relayout` keeps the range; `Document()` still returns the full document.
- Out-of-range bounds or `First >= Last` (empty or backwards) return
  `ErrInvalidOptions`; `Range == nil` means the whole document.

### Per-call statistics

With `render.Options{Measure: true}`, `(*Layout).Stats()` reports **one call's**
phase attribution: `Total`, `Keys`, `Break`, `Stack`, `Paginate`, `Assemble`,
`Shapes`, `Tasks`, `Pages`, `Tiles`. With `Measure` off the durations are zero
while the `Shapes`/`Tasks`/`Pages`/`Tiles` counters stay accurate (no clock
reads). `Stack`/`Paginate` are sums over worker segments and can exceed wall time
when parallel; use `Total` for phase attribution. `Engine.Stats()` remains the
Engine-wide cumulative snapshot; the two do not pollute each other.

## Relayout and caches

```text
BreakKey = hash(text, fonts, fontSize, letterSpacing, width, lang, align, indent, rules)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins)
PaintKey = hash(PageKey, theme)
```

`Layout.Relayout(ctx, opts)` reuses phase products through these three keys: a
`BreakKey` hit skips shaping and line breaking; a `PageKey` hit reuses line boxes
and the page map and only recomputes y positions and page boundaries; options that
only affect painting (colors, decoration, backgrounds) enter only `PaintKey`. Each
chapter keeps its two most recent variants, and `breakUnit` stores rune ranges and
line metrics, not shaped glyphs. `Options.LineCache` (default 256) caches
non-justified shaped lines; a hit does not increase `Engine.shapes`.

`Typography.Rules` is hashed as a whole, so a newly added policy field
automatically participates. To change paper or theme, call `Layout.Relayout`; do
not rebuild the `Document` (that discards all three cache layers).

### Phase timing

`render.Options{Measure: true}` enables timing. `Engine.Stats()` reports cumulative
wall time: `Total`, `Keys`, `Break`, `Stack`, `Paginate`, `Assemble`, plus a
separately accumulated `Display`. With `Measure` off the layout path reads no
clock. `Tasks`, `Shapes`, `Pages` and `Tiles` are always counted; `Busy` /
`Imbalance` are measured only with `Measure` on. `Engine.Stats()` is cumulative
for the Engine's lifetime (good for a debug panel); to attribute one slow layout
to a specific call, use `Layout.Stats()` above.

## Performance

Same-corpus A/B (200k Chinese characters, 640×900, 18px/1.65, Droid Sans Fallback,
`scripts/ab.sh`):

- **Single-core is on par with Blink:** 59.1 vs 58.5 ms; 8/16 workers are 4.2×
  faster (14.1 / 13.7 ms).
- **Line-height relayout is 22× faster:** 1.1 vs 24.3 ms. `LineHeight` is not part
  of `BreakKey`, so no shaping runs at all.
- **Segmentation does no bidi for simple scripts:** pure LTR text is split by
  script+face in `render/segment.go`; complex scripts and bidi controls fall back
  to go-text's `Segmenter`. On Chinese paragraphs this dropped allocations from
  39.7 B/rune to 0: about 120 MB less allocated per layout of 10 MB of text. The
  fallback now splits at bidi paragraph separators (LF/CR/U+2029, ...) and runs
  `Segmenter.Split` per fragment, with a coverage invariant over the shaped runs,
  so **a text node that mixes a line break with Arabic/Hebrew/Devanagari/Thai
  text lays out normally** (it used to panic).
- **Layout memory is 3.9 B/rune** (5M characters = 18 MB; before: 13.2 B/rune /
  64 MB). Paragraphs keep no `[]rune` copies, line boxes are 20 B (`start`/`x`/
  `avail`/`last`/`align` derived from style), flow stores only 4 B of y and 4 B of
  unit index per item, and `flowItem` is materialized on demand.
- **wasm single-thread is 4.6× slower than native** (269.6 ms): a runtime
  difference, not an algorithm one, so in a browser layx is currently slower than
  the browser's own layout engine; it matches Blink when embedded natively in a
  reader.

### Concurrency and load (`go test ./render -bench`, 8 cores / 16 threads)

Cold-layout parallelism (200k Chinese characters, `BenchmarkLayoutScale`; identical
point-by-point to before, ±2% noise):

| workers | 1 | 2 | 4 | 8 | 16 |
|---|---|---|---|---|---|
| wall clock | 44.9 ms | 23.7 ms | 13.3 ms | 8.9 ms | 7.8 ms |

95% of it is shaping and UAX #14; on 8 physical cores 8 workers already give 5.1×
and 16 workers 5.8× — SMT is the ceiling: **the parallel limit of one cold layout
is the hardware, not task granularity**. 5M characters / 8 workers is on par with
before (±2%) with 4% fewer allocations.

What really got faster were three structural serialization/contention points (same
machine, `-benchtime 20x`, several runs; before → after):

| Scenario | Before | After |
|---|---|---|
| Relayout (100 chapters / 4000 paragraphs, paragraph gap only) | 1.16 ms | 0.31–0.38 ms |
| 16 display lists (cold, one page at a time) | 2.60 ms | 2.39 ms |
| Same, via `Prefetch` | — | 0.84 ms |
| Foreground small layout + background whole-book layout | 4.2 ms | 1.6–2.1 ms |

Numbers are measured by the scripts on the spot; do not compare across groups:
`scripts/ab.sh` (browser side), `tools/bench_native` (native side, same font
bytes), `go test ./render -run '^$' -bench 'BenchmarkLayout5M|BenchmarkRelayout'`
(5M characters).

Memory is measured with `tools/memreport`, which builds a document the way a host
does, lays it out, draws a few pages and changes the font size a few times,
printing the live heap, heap requested from the OS, allocation churn and peak RSS
(the number the OS task manager shows):

```sh
go run ./tools/memreport -chars 3300000 -turns 5     # 10 MB plain text, 5 font-size changes
go run ./tools/memreport -chars 3300000 -profile /tmp/alloc.pprof   # allocation attribution
go run ./tools/memreport -chars 3300000 -live-profile /tmp/live.pprof   # live-heap attribution
```

A/B for 10 MB of Chinese plain text (3.3M characters / 20k paragraphs / 100 chapters):

| | Before | After |
|---|---|---|
| Layout live heap | 80 MB | 52 MB |
| Allocation churn per Layout | 577 MB | 404 MB |
| Peak RSS (one layout) | 145 MB | 109 MB |
| Peak RSS (after 5 font-size changes) | 375 MB | 232 MB |

## Verification

CI (`.github/workflows/ci.yml`) runs `gofmt`, `go vet`, unit tests and `-race` on
Linux, macOS and Windows, a cross-build matrix (`linux/386`, `linux/arm64`,
`windows/amd64`, `darwin/arm64`, `js/wasm`, `freebsd/amd64`), a coverage gate,
short fuzz runs (`FuzzLayoutText`, `FuzzConvertChapter`, `FuzzParseCSS`), a
benchmark regression gate, and a `go-min` job (Go 1.21 with `GOWORK=off`) that
checks the root module's declared minimum. Run the same checks locally:

```sh
./scripts/check.sh          # gofmt / vet / tests / cross-builds
./scripts/check.sh --fuzz   # ... plus a short fuzz smoke run (FUZZTIME=15s)
```

Individually:

```sh
go test ./... -count=1
go test ./render -race -count=1
go vet ./...
(cd epub && go test ./... -count=1)
go test ./render -run '^$' -fuzz FuzzLayoutText -fuzztime 30s
(cd epub && go test -run '^$' -fuzz FuzzConvertChapter -fuzztime 30s)
./scripts/benchgate.sh    # compare with scripts/bench_baseline.txt; -update re-records
```

`render/mem_test.go` is a memory gate: it asserts that laying out a 1M-character
document stays under 6 B/rune (currently about 3.9) and logs the measured value;
it skips when no CJK font is available.

The comparison scripts need `curl` and Chrome/Chromium (or `CHROME=/path/to/chrome`):

- `./scripts/linecmp.sh {cjk,latin,mixed-ja,punct}`: feeds the same font bytes to
  the engine and the browser and compares **line by line** (the native side dumps
  line boxes; the browser side uses `Range.getClientRects()`). 54/54 lines match
  for Chinese, 42/42 for English, 63/63 for Japanese/English mixed text, 70/70 for
  dialogue-dense text with compression.
- `./scripts/ab.sh book --chars 200000 --turns 100 --repeat 3`: layx-wasm vs
  DOM + CSS multi-column in the same Chromium.
- `./scripts/snapshot.sh base` / `./scripts/snapshot.sh change --pages 1,2`:
  headless screenshots and per-stage pixel diffs; output in `snapshots/`
  (gitignored).

## Known limitations

- Not implemented: tables, ruby, vertical writing, footnotes, flex/grid, RTL
  visual order, float wrapping, `keep-with-next`. Tables and footnotes need only
  new styles and pagination rules; flex/grid, vertical writing and ruby need a new
  layout model.
- Unsupported features are no longer silent: the `epub` layer reports linearized
  tables, dropped images/SVG, ignored ruby and CSS at-rules as
  `IssueDroppedContent`; the `render` layer reports what it drops itself (e.g. an
  image without bytes). IR-level capabilities (tables, flex/grid, vertical
  writing, ruby) still have no layout model, so the host degrades after reading
  the diagnostics; fixed-layout/scripted content is likewise the host's call.
- Diagnostics report **what was dropped**, not a capability: `IssueMissingGlyphs`
  tells you the configured fonts do not cover runes; choosing fallback faces and
  adding them to the `FontSet` is still the host's job.
- `Style`'s zero value means "inherit", so an explicit reset such as
  `FirstLineIndent: 0`, `LetterSpacing: 0` or `Hyphenate: false` cannot be
  expressed; build with already-cascaded styles.
- Anchors are node-level, not rune-level; stability across book versions is the
  host's `AnchorID` contract.
- Punctuation compression only trims paired quotation marks to half width, with
  rules fixed to the Chromium behaviour measured by linecmp; `widows`/`orphans`
  default to the CSS initial value 2/2 (write `1` to fill pages); hanging
  punctuation only takes effect with hyphenation off.
- Hyphenation is off by default; enable it with both `LayoutOptions.Hyphenator`
  (e.g. `hyphenen.New()`) and `Style.Hyphenate: true` on the block.
- Pagination breaks at line boundaries for now; complex `break-inside` and
  cross-page image policy need refinement.
- `Layout` keeps the whole book's line boxes and flow, about 4 B/rune: a 1M-rune
  book is about 4 MB. The bigger remainder is not in the layout but in the
  `Document` (source text) and the `FontSet` (font bytes plus go-text's parsed
  `glyf` tables — a CJK font is about 20 MB; **share one FontSet per Engine; do
  not rebuild it per book or per font size**).
- The `Limits` defaults target one trade eBook: books above them fail with
  `ErrLimitExceeded` instead of slowly eating memory. Very large reference works
  or serialized web fiction should use `LayoutRange`; raise the caps explicitly
  only if you own the memory cost.
- Peak RSS is dominated by per-layout allocation churn (Go's GC grows the heap to
  roughly 2× the live set); about half of the churn is the part of go-text's
  shaping and bidi that cannot be avoided. If a Windows host sees much larger
  numbers, first check that it is not rebuilding the `FontSet` per font size and
  that `TileHeight` is not set to the whole book height (one tile's display list
  is the whole book's glyphs), then measure with `tools/memreport`.
- The `examples/wasm` demo still does a cold layout when changing font size/line
  height; its benchmark path uses `Relayout`.

## Documentation

- [`docs/api.md`](docs/api.md) — API reference: out-of-range semantics, `IssueCode`,
  `Limits`, error taxonomy, font coverage, `LayoutRange`
- [`docs/design.md`](docs/design.md) — design decisions and invariants
- [`docs/text-rules.md`](docs/text-rules.md) — typography policy and default-value
  sources (Chinese)
- [`epub/README.md`](epub/README.md) — the EPUB loader (Chinese; English abstract at the top)
- [`README.md`](README.md) — the Chinese primary README
- [`CONTRIBUTING.md`](CONTRIBUTING.md) · [`CHANGELOG.md`](CHANGELOG.md) · [`SECURITY.md`](SECURITY.md)

## License

layx is released under the **MIT license**; see [`LICENSE`](LICENSE). The test
font `render/testdata/DejaVuSerif.ttf` uses the DejaVu (Bitstream Vera-derived)
license, documented separately in `render/testdata/DejaVu-LICENSE.txt`; keep that
file when redistributing the font bytes.
