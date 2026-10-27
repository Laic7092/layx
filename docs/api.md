# layx API reference

Quick reference for the public surface whose semantics are easiest to get wrong.
It covers `render`, `render/paint` and the nested `github.com/laix/layx/epub`
module. The Chinese entry point is [`../README.md`](../README.md); the English one
is [`../README.en.md`](../README.en.md). Full signatures and doc comments are on
[pkg.go.dev](https://pkg.go.dev/github.com/laix/layx/render).

Layout code is the source of truth. This page documents the contract the code is
expected to keep; a mismatch is a bug.

## Modules and versions

| Module | Import path | Tag | Minimum Go |
|---|---|---|---|
| layout core | `github.com/laix/layx/render` | `v0.x.y` | 1.21 |
| drawing contract | `github.com/laix/layx/render/paint` | (in the root module) | 1.21 |
| optional hyphenation | `github.com/laix/layx/render/hyphen/en` | (in the root module) | 1.21 |
| EPUB loader | `github.com/laix/layx/epub` | `epub/v0.x.y` | 1.26 |

The two modules are released together and should be upgraded together. `epub`
requires a published `render` version; there is no local `replace` in the
published module.

```sh
go get github.com/laix/layx/render@v0.1.0
go get github.com/laix/layx/epub@v0.1.0
```

## Request flow

```text
DocumentBuilder ──Build()──▶ *Document ──Engine.Layout(ctx, doc, opts)──▶ *Layout
                                                                    │
                         ┌──────────────────────────────────────────┤
                         ▼                      ▼                   ▼
                    Page / Tile            Location/Selection   Issues()/Stats()
                         │
                         └──Draw(painter)──▶ paint.Painter (host)
```

- `Document`, `FontSet` and `Layout` are immutable after construction (internal
  caches mutate) and safe for concurrent readers.
- `Page`, `Tile`, `Prefetch` and `Engine` are safe for concurrent use; a host
  `Painter` is not (one painter per goroutine).
- The library does no I/O, starts no hidden goroutines and has no global state.

## Out-of-range and empty results

Rule: a lookup that cannot resolve returns `(T, false)`, `nil`, or an empty slice
— it never panics and never returns a partial object. Values returned with
`false` are the zero value of `T`.

### Pointers (check for `nil`)

| Call | Returns `nil` when |
|---|---|
| `(*Layout).Page(n int) *Page` | `n` is out of range; scroll-mode layouts have no pages (`PageCount() == 0`) |
| `(*Layout).Tile(i int) *Tile` | `i` is out of range; paged-mode layouts have no tiles (`TileCount() == 0`) |

### `(T, bool)` lookups

| Call | Returns `false` when |
|---|---|
| `(*Document).Chapter(i int) (Chapter, bool)` | `i` is out of range |
| `(*Document).Text(chapter int) (string, []TextRun, bool)` | `chapter` is out of range |
| `(*Layout).Locate(anchor AnchorID) (Location, bool)` | the anchor is unknown (`NoAnchor` is not locatable) |
| `(*Layout).PageOf(loc Location) (int, bool)` | scroll mode, or the location cannot be resolved in this layout |
| `(*Layout).LocationOfPage(page int) (Location, bool)` | `page` is out of range, or the layout has no pages |
| `(*Layout).LocationAt(y float32) (Location, bool)` | there is no content at `y` (scroll mode: `y` is above or below the book body) |
| `(*Layout).OffsetOf(loc Location) (float32, bool)` | the location cannot be resolved |
| `(*Layout).Progress(loc Location) (float32, bool)` | the location cannot be resolved |
| `(*Layout).PageRange(chapter int) (first, count int, ok bool)` | scroll mode, or `chapter` is outside this layout |
| `(*Layout).ChapterContentTop(chapter int) (float32, bool)` | `chapter` is outside this layout |
| `(*Layout).HitTest(pt paint.Point) (Location, bool)` | there is nothing to hit (empty layout, or an empty chapter at that position) |
| `(*Page).HitTest(pt paint.Point) (Location, bool)` | this page has no hittable content (empty page) |
| `(*FontSet).Resolve(style Style, r rune) (FaceID, bool)` | no face covers `r` |
| `(*FontSet).Face(id FaceID) (paint.Font, bool)` | `id` is not a face of this set |
| `(*epub.Book).ResolveLink(url string) (LinkTarget, bool)` | `url` is external (http, mailto, ...) or a broken in-book link |

`HitTest` returns the nearest item rather than rejecting points at the page edge;
`false` means "nothing to hit" (empty layout/chapter/page), not "coordinate
outside the viewport".

### Slices

| Call | Empty result |
|---|---|
| `(*Layout).Issues() []Issue` | empty slice (`len == 0`) when no diagnostic was raised; the slice is a copy and safe to keep |
| `(*Layout).Selection(from, to Location) []SelectionQuad` | `nil` when either end does not resolve, or when the resolved range covers no rune (an empty selection) |
| `(*Layout).Tiles(lo, hi int) []*Tile` | `nil` when the range is empty after clamping to `[0, TileCount)` |
| `(*Layout).ChapterPages() []int` | one entry per **document** chapter: the chapter's first global page, or `-1` for chapters not part of this layout (range layout) — `nil` in scroll mode |
| `(*epub.Book).Issues() []render.Issue` | `nil` when the most recent Open/Build raised no diagnostic |

### Plain values

| Call | Contract |
|---|---|
| `(*Layout).Document() *Document` | always non-`nil`; `Relayout` variants share the pointer |
| `(*Layout).PageCount() int` | non-negative; `0` in scroll mode |
| `(*Layout).TileCount() int` | non-negative; `0` in paged mode |
| `(*Layout).ContentHeight() float32` | non-negative; `0` for an empty layout |
| `(*Layout).PageContentHeight() float32` | page height minus top/bottom margins |
| `(*Document).ChapterCount() int` | number of chapters |
| `(*Document).StyleCount() int` | number of style entries |

## Diagnostics

Diagnostics report what the library **decides to drop or substitute**. They never
change layout results, and they are not errors: layout succeeds.

```go
eng, _ := render.New(render.Options{
    OnIssue: func(is render.Issue) { log.Print(is) },
})
layout, err := eng.Layout(ctx, doc, opts)
// same data, after the call:
issues := layout.Issues()
```

Delivery semantics:

- `Options.OnIssue` fires once per issue **before** `Layout` / `Relayout` returns,
  on the goroutine that called it, after all workers have joined. It never runs
  concurrently with itself and may call back into the library.
- `(*Layout).Issues()` returns the same list as a copy, retained for the lifetime
  of the layout; it is an empty slice when nothing was reported. It is the right
  interface when only statistics are needed.
- The list is deduplicated and capped at 1024 entries. When capped, the last entry
  is `IssueSuppressed` whose `Detail` holds how many issues were not retained.
- With `Engine.LayoutRange`, only issues from the laid-out range are reported.

`Issue`:

| Field | Meaning |
|---|---|
| `Code IssueCode` | classification, see below |
| `Chapter string` | chapter ID, or `""` when not tied to a chapter |
| `Node NodeID` | node ID, or `InvalidNode` when not tied to a node |
| `Detail string` | human-readable specifics (code points, family name, ...) |

| `IssueCode` | `String()` | Reported when |
|---|---|---|
| `IssueMissingFont` | `"missing-font"` | a non-empty `Style.FontFamily` has no face in the `FontSet`; a fallback face was used, one issue per (chapter, node, family) |
| `IssueMissingGlyphs` | `"missing-glyphs"` | no configured face has a glyph for some runes of a text segment; they were shaped as `.notdef`; one issue per segment node, `Detail` lists up to 8 code points plus a count. Control/format/default-ignorable characters and spaces are not reported |
| `IssueDroppedContent` | `"dropped-content"` | content the IR cannot express or for which data is missing (e.g. an image without bytes); the `epub` layer also reports tables it linearized, dropped images/SVG, ignored ruby, skipped CSS at-rules and unrepresentable attributes |
| `IssueSuppressed` | `"suppressed"` | the 1025th and later issues of one call; `Detail` is the number not retained |

`epub` reuses these codes: `epub.Options.OnIssue func(render.Issue)` and
`(*epub.Book).Issues() []render.Issue` follow the same semantics (one callback per
issue, before `Build`/`BuildWith` returns, on the caller goroutine).
`Book.Issues()` starts with the issues found by `Open`, is replaced by every
`Build`/`BuildWith`, and is `nil` for a clean book.

## Font coverage

```go
func (fs *FontSet) FontCount() int
func (fs *FontSet) HasFamily(family string) bool
func (fs *FontSet) Coverage(r rune) bool
func (fs *FontSet) Resolve(style Style, r rune) (FaceID, bool)
func (fs *FontSet) Face(id FaceID) (paint.Font, bool)

type FaceID int
const InvalidFace FaceID = -1
```

| Call | Semantics |
|---|---|
| `FontCount()` | number of faces in the set |
| `HasFamily(name)` | there is a face whose `Family` matches `name` (case-insensitive, trimmed). Empty name → `false` |
| `Coverage(r)` | any face in the set has a nominal glyph for `r` |
| `Resolve(style, r)` | the face index the layout would actually choose for `r` under `style.FontFamily`/`FontWeight`/`FontSlant`, using the same fallback ordering as layout; `false` if no face covers `r` |
| `Face(id)` | the immutable `paint.Font` of a face, for drawing or font matching by the host |

All of these are host queries, safe for concurrent use, and cheap enough to call in
a loop. They answer "will this text render, and with which face", not "is the
result pretty".

## Limits

`Options.Limits` caps one layout call. Every field is a hard cap; exceeding one
returns an error wrapping `ErrLimitExceeded` with the measured value and the cap.
A zero field selects its default (`DefaultLimits()`).

| Field | Default | Caps |
|---|---|---|
| `Limits.MaxNodes` | `1 << 20` (1,048,576) | nodes visited by one layout |
| `Limits.MaxDepth` | `256` | chapter tree depth |
| `Limits.MaxTextRunes` | `20_000_000` | text runes |
| `Limits.MaxImagePixels` | `64 << 20` (67,108,864) | total image pixels |
| `Limits.MaxPages` | `100_000` | pages produced in paged mode |
| `Limits.MaxTiles` | `100_000` | tiles produced in scroll mode (content height / `TileHeight`) |
| `Limits.MaxLines` | `2_000_000` | line boxes built by the break phase |

Enforcement points: `Engine.Layout`, `Layout.Relayout` and `Engine.LayoutRange`,
including cache hits (a cached chapter cannot bypass `MaxLines`). `InitScroll`
checks `MaxTiles` before allocating the tile index.

`const MinTileHeight = 16` is the smallest accepted non-zero
`LayoutOptions.TileHeight` in CSS pixels; below it the layout fails with
`ErrInvalidOptions` because every extra content pixel would add a tile.

## Errors

Every returned error wraps exactly one sentinel, so `errors.Is` works across the
API. Use `errors.As` for structured context; never parse `Error()` text.

| Sentinel | Meaning |
|---|---|
| `ErrInvalidDocument` | the `Document` is malformed: node/style/chapter IDs out of range, cycles, duplicate chapter IDs, bad anchors, non-finite style values |
| `ErrInvalidOptions` | `Engine`/`Layout` options are invalid: non-finite or non-positive geometry, `TileHeight` below `MinTileHeight`, invalid font faces, `LayoutRange` bounds out of range, bad mode |
| `ErrNoFonts` | `NewFontSet()` called without faces, or a layout with no usable font |
| `ErrLimitExceeded` | a `Limits` cap was hit (measured value and cap are in `Detail`) |
| `ErrClosed` | reserved for a closed resource; `Engine.Close()` currently only releases reusable workers and the Engine stays usable, so hosts should not depend on seeing this |
| `ErrInternal` | a library bug: a panic recovered at a worker boundary. The library contract is "any input ends as an error, never as a crash"; seeing this means report it |

```go
type DocumentError struct {
    Kind    error   // one of the sentinels above
    Chapter string  // "" when not applicable
    Node    NodeID  // InvalidNode when not applicable
    Style   StyleID // InvalidStyle when not applicable
    Detail  string  // description without the context prefix
}

func (e *DocumentError) Error() string
func (e *DocumentError) Unwrap() error // returns Kind

var (
    ErrInvalidDocument error
    ErrInvalidOptions  error
    ErrNoFonts         error
    ErrLimitExceeded   error
    ErrClosed          error
    ErrInternal        error
)
```

```go
var de *render.DocumentError
if errors.As(err, &de) {
    if de.Chapter != "" {
        host.JumpToChapter(de.Chapter)
    }
    log.Printf("kind=%v node=%d style=%d detail=%s", de.Kind, de.Node, de.Style, de.Detail)
}
```

Sentinels are stable identities: a new error site must keep its sentinel, and
`errors.Is` must keep working for it.

### Panic contract

The layout engine converts panics on both the calling and helper goroutines into
an error wrapping `ErrInternal`; the first panic wins and no further work is
handed out. Consequently:

- **No input can crash the host process.** Malformed documents, hostile text,
  absurd options and enormous inputs end as errors (`ErrInvalidDocument`,
  `ErrInvalidOptions`, `ErrLimitExceeded`, ...) or as a successful layout.
- `ErrInternal` is a bug report: treat the layout as failed, but do not assume the
  document is invalid.
- The library does **not** recover panics from a host-provided `paint.Painter`
  inside `Page.Draw` / `Tile.Draw`. Painters must handle their own panics and keep
  their own state consistent.
- Library state stays consistent after a recovered panic: no budget token is
  leaked, lazy caches do not retain partial results, and the `Engine` remains
  usable.

## Range layout

```go
type ChapterRange struct {
    First, Last int // half-open [First, Last); First < Last required
}

// LayoutOptions.Range *ChapterRange — nil means the whole document.
func (e *Engine) LayoutRange(ctx context.Context, doc *Document, opts LayoutOptions, first, last int) (*Layout, error)
```

`LayoutRange(ctx, doc, opts, first, last)` is equivalent to
`opts.Range = &ChapterRange{First: first, Last: last}` followed by
`Engine.Layout(ctx, doc, opts)`.

Semantics:

| Aspect | Behaviour |
|---|---|
| Chapters | only `[First, Last)` are laid out; other chapters produce no lines or pages |
| Page numbers | start at 0 for the first page of the range |
| `Location.Chapter` | the document's chapter index, not an offset inside the range |
| Resolution | `Locate`, `PageOf`, `LocationOfPage`, `LocationAt`, `OffsetOf`, `Progress`, `PageRange`, `ChapterContentTop` never resolve outside the range and return `false`; `ChapterPages()` puts `-1` in the entries of chapters outside the range (and is `nil` in scroll mode) |
| Scroll content | `y = 0` is the top of the first included chapter |
| `Relayout` | keeps the range (`opts.Range` from the original layout is reused unless overridden) |
| `Document()` | still returns the full document |
| Bounds | `First < 0`, `Last > ChapterCount` or `First >= Last` (empty or backwards) → `ErrInvalidOptions` |

`LayoutOptions.Range == nil` means the whole document; a `ChapterRange` must name
a non-empty range inside the document. The zero `ChapterRange` is an empty range,
not "the whole document", and is rejected.

The intended use is serialized/lazy hosting: lay out chapter-by-chapter, keep the
full `Document`, and translate `Location.Chapter` to a global chapter index for the
UI.

## Per-call statistics

```go
type LayoutStats struct {
    Total, Keys, Break, Stack, Paginate, Assemble time.Duration
    Shapes, Tasks                                  int64
    Pages, Tiles                                   int
}

func (l *Layout) Stats() LayoutStats
```

- `Stats()` attributes phases to **one** `Layout`/`Relayout`/`LayoutRange` call;
  the engine-wide `Engine.Stats()` remains cumulative and is not reset by it.
- Durations are recorded only when `render.Options{Measure: true}` is set; with
  `Measure` off they are zero. `Shapes`/`Tasks`/`Pages`/`Tiles` are always counted.
- `Stack` and `Paginate` are sums over worker segments; they can exceed wall time
  when phases run in parallel. Use `Total` for phase attribution.
- `Relayout` variants report their own call; the cache-hit share is visible by
  `Keys`/`Break`/`Paginate` staying near zero.

## Coordinate systems

| Coordinate | Used by | Origin |
|---|---|---|
| page-local | `Page.HitTest`, `SelectionQuad.Rect` | top-left of the page, margins included |
| content | `Layout.HitTest`, `LocationAt`, `OffsetOf`, `SelectionQuad.Content`, `Tile.Top` | x from the left edge of the viewport; y from the top of the book body, across chapters |
| tile-local | `Tile.Draw`/`Tile.Links` ops | top-left of the tile = content minus `Tile.Top()` |

`Layout.HitTest` takes content coordinates but **x without `Margins.Left`** — feed
it `Content.X - Margins.Left`. `Page.HitTest` takes page-local coordinates as-is.

## EPUB extras

| Call | Semantics |
|---|---|
| `epub.Open(path) (*Book, error)` | opens the container/OPF; no spine item is parsed |
| `epub.ReadMetadata(path) (Metadata, error)` | shelf metadata and cover bytes only; a non-zip file wraps `ErrNotArchive`; no cover is not an error |
| `(*Book).Build(base float32)` / `BuildWith(base, Options)` | converts the spine to `*render.Document` plus image bytes; image IDs are stable for the life of the `Book` |
| `(*Book).ResolveLink(url) (LinkTarget, bool)` | in-book link → chapter + `render.AnchorID`; external URL → `false` |
| `(*Book).Issues() []render.Issue` | diagnostics of the most recent build |
| `epub.Options.OnIssue` | same delivery semantics as `render.Options.OnIssue` |
