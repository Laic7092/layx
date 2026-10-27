# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/) with the usual pre-1.0 caveat: minor
releases may contain breaking changes (see `CONTRIBUTING.md`).

The `render` and `epub` modules are versioned together: `v0.1.0` tags the root
module and `epub/v0.1.0` tags the nested one. Upgrade them in lockstep.

## [0.1.0] — 2026-10-27

The first public release. It turns an internally healthy layout engine into a
library a stranger can adopt: a crash boundary, an input contract, diagnostics,
licensing, CI and an English entry point. Fixes are grouped by the audit that
drove them (`docs/audit.md`).

### P0 — the host process can no longer be crashed by input

- **`Layout` no longer panics on multi-paragraph complex-script text**
  (audit §2.1). `go-text`'s `Segmenter.Split` truncates its output at the first
  bidi paragraph separator, which left the shaped runs covering only the first
  paragraph while the line wrapper still mapped runes across the whole
  paragraph. The text worker now splits the input at every bidi paragraph
  separator (LF, CR, CRLF, U+001C–U+001E, U+0085, U+2029), shapes each
  fragment, copies the segmenter's rune slices before the next call, and
  enforces a coverage invariant: shaped runs must cover the whole paragraph,
  with a placeholder run filling any gap. Arabic, Hebrew, Devanagari, Thai and
  bidi-control text inside a `NodeText` with `\n` now lays out normally.
- **`Page.HitTest` and `Layout.HitTest` no longer panic on empty text nodes**
  (audit §2.2). A chapter whose only reachable content is an empty text node
  (or an empty paragraph, hidden block, `<p></p>` from a converter) returns
  `(Location{}, false)` instead of indexing an empty unit list. This is the
  reader's most frequent path — click-to-position, long-press selection,
  double-click word selection.
- **Panics in library worker goroutines are converted to errors**
  (audit §2.1/§4). `parallelFor`/`runTask` recover on both the calling and
  helper goroutines and return `fmt.Errorf("%w: %v", ErrInternal, r)`; the
  first panic wins and no further work is handed out. `layoutDoc` also recovers
  on the caller goroutine. The library now promises: **any input ends as an
  `error`, never as a crash**; an `ErrInternal` means a library bug, not a bad
  document. Partial lazy caches and budget tokens are left consistent.
- **Fuzz targets**: `FuzzLayoutText` (render) and `FuzzConvertChapter` /
  `FuzzParseCSS` (epub) are committed and smoke-run in CI (audit §2.3.4,
  §6.2). The audit's three crash inputs run as seeds.

### Input contract

- **NaN/±Inf are rejected** (audit §3.1). `LayoutOptions.validate` rejects
  non-finite `Viewport.Width/Height`, `Margins.*`, `TileHeight` and
  `Typography.FontSize/LineHeight/LetterSpacing/WordSpacing/ParagraphGap`;
  `DocumentBuilder.Build` rejects non-finite fields in every `Style`
  (`FontSize`, `LineHeight`, `LetterSpacing`, `WordSpacing`, `FirstLineIndent`,
  `Margin*`, `CornerRadius`, `Border*.Width`) with style context. Previously a
  NaN viewport produced NaN page geometry, and a NaN line height silently
  produced a one-page layout.
- **`Limits.MaxPages`, `Limits.MaxTiles` and `Limits.MaxLines` are enforced**
  (audit §3.2, contract): defaults are 100 000 pages, 100 000 tiles and
  2 000 000 line boxes, hard caps that return `ErrLimitExceeded` with the
  measured value and the cap. Enforced in `Layout`, `LayoutRange` and
  `Relayout`, including cached page maps and cached break chapters.
- **`TileHeight` has a lower bound**: `MinTileHeight = 16` CSS px. A non-zero
  tile height below it fails with `ErrInvalidOptions` instead of turning a
  27-px content height into 270 001 tiles.
- **Hostile-input test suite** (`TestHostileInputs`): NaN/Inf, zero/negative,
  tiny and huge values, empty text, control characters, RTL and invalid UTF-8,
  each asserted to return the right sentinel rather than panic or produce
  non-finite geometry.

### Diagnostics and errors

- **New diagnostics API** (audit §3.3): `Options.OnIssue func(Issue)` and
  `(*Layout).Issues() []Issue`. `Issue{Code, Chapter, Node, Detail}`; codes are
  `IssueMissingFont`, `IssueMissingGlyphs`, `IssueDroppedContent` and
  `IssueSuppressed`. Delivery is after `Layout`/`Relayout` returns, on the
  caller's goroutine; the list is deduplicated, capped at 1024 entries with an
  `IssueSuppressed` summary, and never changes layout results. Unknown font
  families, tofu glyphs and content the IR drops are now visible to the host.
- **New `FontSet` coverage API** (audit §3.5):
  `FontSet.HasFamily(name string) bool`, `Coverage(r rune) bool`,
  `Resolve(style Style, r rune) (FaceID, bool)` (plus `FaceID`,
  `InvalidFace` and `(*FontSet).Face`), safe for concurrent use. Hosts can
  detect tofu, pick a fallback, and explain to users why a page looks wrong.
- **Structured errors** (audit §3.4):
  `render.DocumentError{Kind, Chapter, Node, Style, Detail}` with
  `Error()`/`Unwrap()`. Every error still wraps one of
  `ErrInvalidDocument`, `ErrInvalidOptions`, `ErrNoFonts`, `ErrLimitExceeded`,
  `ErrClosed` or `ErrInternal`, so `errors.Is` keeps working, but hosts can now
  read the chapter/node/style identifiers instead of parsing error strings.
- **EPUB diagnostics** (audit §3.3): `epub.Options.OnIssue func(render.Issue)`
  and `(*Book).Issues() []render.Issue`, reusing `render.IssueCode`.
  Linearized `<table>` rows, dropped `<img>`/`<image>`/`<svg>`, ignored
  `<ruby>`, skipped CSS at-rules, unrepresentable attributes such as
  `colspan`, and malformed markup are reported with the chapter path and a
  detail string, instead of disappearing silently.
- **Reproducible sample books**: `tools/genepub` generates deterministic
  synthetic EPUBs (container, OPF, nav, chapters, tables/images/CSS) so
  examples and tests do not depend on anyone's private book collection.

### Distribution

- **`LICENSE` (MIT)** added (audit §2.3.1, §6.1) — the single blocker with the
  highest adoption cost. Test fonts keep their own license next to the data
  (`render/testdata/DejaVu-LICENSE.txt`).
- **`epub` is installable**: `epub/go.mod` no longer contains a local
  `replace`, and requires a real `github.com/laix/layx` version (audit §2.3.2,
  §6.5). The module is published with tags (`v0.x.y`, `epub/v0.x.y`).
- **CI added** (`.github/workflows`, audit §2.3.3, §6.1): `gofmt`/`go vet`/unit
  tests, `-race` on Linux, tests on Windows/macOS, a cross-build matrix
  (`linux/386`, `linux/arm64`, `windows/amd64`, `darwin/arm64`, `js/wasm`,
  `freebsd/amd64`), EPUB module tests, coverage gate, short fuzz smoke runs and
  a benchmark regression gate. `scripts/check.sh` runs the same checks locally;
  `scripts/benchgate.sh` compares against `scripts/bench_baseline.txt`.
- **Minimum Go versions lowered** (audit §2.3.5, §6.5): root module `go 1.21`,
  `epub` `go 1.26` (driven by `golang.org/x/net`). A committed `go.work` makes
  workspace development work on one toolchain without local `replace`
  directives.
- **Repository hygiene** (audit §2.3.6): committed `__pycache__` artifacts
  removed and ignored; generated test binaries and snapshots stay untracked.

### Documentation and examples

- **English entry point**: new `README.en.md` covering overview, package table,
  quick start, outputs, concurrency/budget, positions/hit testing, relayout and
  caches, diagnostics, testing/benchmarks, known limitations and license,
  cross-linked from `README.md` (`English · 中文`). `README.md` stays the
  Chinese primary document and gains the license, CI/badge, minimum-version,
  diagnostics and out-of-range sections (audit §3.6).
- **`docs/api.md`**: concise API reference — out-of-range and empty-result
  semantics table (audit §4), `IssueCode` table, `Limits` defaults, error
  taxonomy, `FontSet` coverage API, `LayoutRange` semantics.
- **`CONTRIBUTING.md`**, **`CHANGELOG.md`**, **`SECURITY.md`**, issue templates
  (`bug_report.yml`, `feature_request.yml`) and a pull-request template added
  (audit §3.6).
- **Runnable `Example` functions** in `render/example_test.go`, shown on
  pkg.go.dev: engine setup and layout, building a `Document`, font coverage /
  hit testing. No network, no temporary files (audit §3.6).
- **`epub/README.md` is self-contained**: all links to a private companion
  repository and its local paths were removed; it now links only to files in
  this repository (audit §3.6).

### API evolution (P2)

- **`Layout.HitTest` is unified with `Page.HitTest`**: it now takes
  `paint.Point` (`HitTest(pt paint.Point) (Location, bool)`) in both paged and
  scroll modes (audit §4). This is a breaking signature change; the old
  `HitTest(x, y float32)` call sites must be rewritten (all in-repo callers
  were).
- **Per-call `LayoutStats`**: `(*Layout).Stats() LayoutStats` attributes a
  single call's `Total/Keys/Break/Stack/Paginate/Assemble/Shapes/Tasks/Pages/
  Tiles` (durations only with `Options.Measure`), so a slow layout can be
  explained without the engine-wide cumulative `Stats` and without one
  goroutine resetting another's counters (audit §4).
- **Chapter-range layout**: `ChapterRange{First, Last}`, `LayoutOptions.Range`
  and `Engine.LayoutRange(ctx, doc, opts, first, last)` lay out only a chapter
  range, for serialized books and lazy loading. Page numbers start at 0 for the
  range; `Location.Chapter` keeps the document's chapter index; positions
  outside the range never resolve; `Relayout` keeps the range; `Document()`
  returns the full document (audit §4).
- **Out-of-range and empty-result semantics are documented and uniform**
  (audit §4): `Page(n) *Page` returns `nil`; `Chapter(i) (Chapter, bool)`,
  `Locate`/`HitTest`/`PageOf`/`LocationOfPage`/`LocationAt`/`OffsetOf`/
  `Progress` return `(T, false)`; `Issues()` and `Tiles()` return empty
  slices. See `docs/api.md`.

### Changed

- Unknown fonts and missing glyphs no longer degrade silently: they are
  reported through `OnIssue`/`Issues` (same layout result, visible diagnostic).
- Invalid floating-point options and styles now fail fast with
  `ErrInvalidOptions` instead of producing NaN output.
- Dependency-free API additions reachable from `render` keep using the
  `paint` aliases; nothing new is re-exported without a reason.

### Fixed

- Rendering a `NodeText` that mixes a paragraph break with complex-script text
  could panic or drop text; it now lays out all paragraphs.
- Hit testing an empty chapter could panic; it now reports "no hit".
- `HitTest` on the wrong format could be evaluated to a fallback face; font
  resolution now reports the missing family and the coverage gap.
- Layout work exceeding resource budgets failed by exhausting memory; it now
  fails deterministically with `ErrLimitExceeded`.
