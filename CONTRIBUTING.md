# Contributing to layx

Thanks for taking the time to contribute. layx is a layout engine that other
people embed in readers, note apps and print pipelines, so changes here are
changes to someone else's product. This document describes how to build, test
and propose changes so review stays cheap.

By contributing you agree that your contribution is licensed under the
repository's [MIT license](LICENSE) (inbound = outbound).

## What belongs here

- `render/` — the layout core: document IR, fonts, shaping, line breaking,
  pagination, scrolling, hit testing, selection.
- `render/paint/` — the drawing contract. No rasterizer lives in this repository.
- `render/hyphen/en/` — the optional English hyphenation patterns. The core does
  not import it.
- `epub/` — the EPUB/XHTML/CSS → `render.Document` converter. A **separate Go
  module**, so it can carry its own dependencies and version.
- `examples/`, `tools/`, `scripts/` — demos and developer tooling; not part of
  the library API.
- `docs/` — design notes (`design.md`), typography policy (`text-rules.md`) and
  the API reference (`api.md`). `README.md` is the Chinese entry point,
  `README.en.md` the English one.

Open an issue before large changes, new public API, new dependencies, or a new
module. Small fixes (typos, tests, docs, bug fixes with a reproducer) can go
straight to a pull request.

## Development setup

Requirements:

- **Go 1.21 or newer** for the root module (`render`). That is the minimum the
  code actually needs, not the version on the maintainer's machine.
- **Go 1.26 or newer** for the `epub` nested module, because
  `golang.org/x/net` requires it.

A single Go 1.26+ toolchain satisfies both. The repository commits a
`go.work` that lists both modules, so from a fresh clone:

```sh
git clone https://github.com/Laic7092/layx.git
cd layx
go work sync          # only after changing a go.mod
go test ./...         # root module, workspace mode
(cd epub && go test ./...)
```

`go.work` is a development convenience. The two modules are published
independently: `epub/go.mod` must keep naming a real `github.com/laic7092/layx`
version and must not carry a local `replace`, and the root module must keep
building with Go 1.21 in plain module mode (`GOWORK=off`), which CI checks in
its `go-min` job.

If `go` fails with `-mod may only be set to readonly or vendor when in
workspace mode`, a global `go env -w GOFLAGS=-mod=mod` is at fault (workspace
mode rejects it): run `go env -u GOFLAGS`, or use `scripts/check.sh`, which
exports the `-mod=readonly` value both module and workspace mode accept.

Do not edit `go.mod`, `go.work` or `epub/go.mod` to point at a local checkout —
that breaks distribution and CI.

## The checks

Run the same gate locally that CI runs:

```sh
./scripts/check.sh          # gofmt, go vet, tests, cross-builds
./scripts/check.sh --fuzz   # ... plus a short fuzz smoke run (FUZZTIME=15s)
```

`check.sh` runs:

- `gofmt -l` over the tracked Go files (must be empty);
- `go vet ./...` for both modules;
- `go test ./...` for both modules;
- cross-compilation of the platform matrix (`linux/386`, `linux/arm64`,
  `windows/amd64`, `darwin/arm64`, `js/wasm`, `freebsd/amd64`), which is cheap
  and catches Windows-only path and `unsafe` assumptions;
- with `--fuzz`: `FuzzLayoutText`, `FuzzConvertChapter` and `FuzzParseCSS` for
  `$FUZZTIME` each (default 15s).

CI additionally runs the race detector, the coverage gate and the benchmark
regression gate; run `go test ./render -race` locally for concurrency changes.

Individual commands, if you want a tighter loop:

```sh
gofmt -l . | grep -v '^$'          # nothing should print
go vet ./...
go test ./render -count=1
go test ./render -race -count=1
go test ./render -run '^$' -fuzz FuzzLayoutText -fuzztime 30s
(cd epub && go test ./... -count=1)
(cd epub && go test -run '^$' -fuzz FuzzConvertChapter -fuzztime 30s)
```

Benchmarks compare against a checked-in baseline:

```sh
./scripts/benchgate.sh            # compare
./scripts/benchgate.sh -update    # regenerate scripts/bench_baseline.txt
```

Benchmark numbers are machine-specific; compare ratios, not absolute
milliseconds, and use the same corpus and font bytes when comparing.

## Coding conventions

- **`gofmt` is mandatory.** `goimports` ordering is welcome but `gofmt` is the
  gate.
- **Code comments are in English**, including doc comments. The public
  documentation is Chinese (`README.md`) plus English (`README.en.md`,
  `docs/api.md`); the source is English so `pkg.go.dev` and non-Chinese
  contributors can read it.
- **Doc comments start with the name of the thing they document** and explain
  *why* the API has that shape, not only what it does. A public identifier that
  a host cannot use correctly from its doc comment is a bug in the comment.
- **No new direct dependencies** without discussion. `render` deliberately
  depends only on `go-text/typesetting` and `golang.org/x/image/math/fixed`;
  `render/deps_test.go` enforces the import gate. The `epub` module is where
  parsing dependencies belong.
- **No global state.** No `init()`, no registries, no logging. If a feature
  needs background work it must be explicit (a returned handle the host closes
  or waits on), never a package-level goroutine.
- **Determinism.** A layout must produce byte-identical output regardless of
  `Options.Workers`. Tests compare the serial and parallel paths; keep them
  doing that.
- **Cache keys are the contract.** If a new option changes line breaking,
  pagination or painting, it must enter `BreakKey`, `PageKey` or `PaintKey`
  respectively — policy fields belong in `TextRules`, whose whole struct is
  hashed. A field missing from its key silently returns a wrong layout after
  `Relayout`. See `docs/text-rules.md`.
- **Every error wraps a sentinel** (`ErrInvalidDocument`, `ErrInvalidOptions`,
  `ErrNoFonts`, `ErrLimitExceeded`, `ErrClosed`, `ErrInternal`) and, where
  context exists, is a `*DocumentError` naming chapter/node/style. Hosts branch
  on `errors.Is` / `errors.As`; never require parsing `Error()` text.
- **The library does not panic on input.** New code that can fail returns an
  error. `recover` is reserved for the worker boundary that converts library
  bugs into `ErrInternal`.

## Tests

- Table-driven tests where the variants are data; named tests for the
  behaviours a host depends on.
- No network access and no reliance on system fonts in tests. Fonts come from
  `render/testdata/`; EPUB samples are generated or skipped when absent.
- Layout results have golden tests. When you intentionally change layout, say
  so in the PR and update the golden data in the same commit; never regenerate
  goldens just to make a test pass without checking the diff.
- Race and compatibility: new public API needs a test; new concurrency needs a
  `-race`-clean path; new input parsing needs hostile-input cases (empty,
  malformed, deeply nested, huge) and ideally a fuzz seed.
- Memory is a feature: `render/mem_test.go` asserts the per-rune layout budget.
  If your change increases it, explain why in the PR.

## Adding test data, fonts and sample books

- **Fonts**: add the smallest file that exercises the case, together with its
  license text next to it (`render/testdata/DejaVu-LICENSE.txt` is the model).
  Do not add fonts whose license forbids redistribution or requires a
  different copyright notice than the one you include. Prefer a font that is
  already present if it covers the script.
- **EPUB samples**: real books are not committed (copyright). Generate the
  synthetic samples with the in-repo generator instead:

  ```sh
  go run ./tools/genepub -out epub/testdata/samples
  ```

  See `epub/testdata/samples/README.md`. Tests must skip cleanly when samples
  are absent.
- **Corpora and snapshots**: benchmark corpora live in `scripts/corpora/`;
  rendering snapshots are build products and are gitignored.

## Commit messages

Use the style already in history: `<area>: <imperative summary>` in the subject
(≤ 72 chars), a blank line, then a body that explains *why* the change is
needed. Reference an issue or an audit section when there is one.

```text
render: keep LocationAt exact at float32 rounding

LocationAt divided a float32 content offset by the page height and truncated,
so a y exactly on a page boundary could resolve to the previous page.
```

Areas: `render:`, `epub:`, `docs:`, `examples:`, `tools:`, `scripts:`, `ci:`.
Do not mix refactors with behaviour changes; separate commits per concern.
Never commit generated artifacts (`*.test`, snapshots, `__pycache__/`).

## Pull requests

A pull request is reviewed for:

1. **Correctness and contract.** Does it keep the invariants in
   `docs/design.md` and `docs/text-rules.md`? Does it update the cache keys,
   error taxonomy and docs when the public surface changes?
2. **Tests.** New behaviour has tests; bug fixes have a regression test that
   fails before the fix. Prefer a reproducer first, then the fix.
3. **Blast radius.** Small, focused diffs. Public API changes are called out in
   the description and in `CHANGELOG.md`.
4. **Distribution.** The root module still builds with Go 1.21 in plain module
   mode (`GOWORK=off`), the `epub` module has no local `replace`, and the
   workspace build used by CI stays green.
5. **Hygiene.** `gofmt`, `go vet`, `-race` and the CI fuzz smoke are green;
   comments are English; no dead code or unrelated formatting churn.

At least one maintainer approval is required to merge. CI must be green. The
maintainer may squash-merge; the resulting commit message follows the same
convention.

## What counts as a breaking change (pre-1.0)

The library is pre-1.0: **minor versions (`v0.x.0`) may break** and
patch versions (`v0.x.y`) do not introduce intentional breakage. The following
are breaking changes and need a minor bump, a CHANGELOG entry and a migration
note:

- Removing or renaming an exported identifier, or changing a function or method
  signature (including receiver type). Adding a method is not breaking.
- Changing the meaning of existing fields or functions, even if the signature
  is unchanged: defaults, units, coordinate systems, page/section numbering,
  `Location` resolution rules, or the guarantee that a call cannot panic.
- Changing layout output in a way that invalidates persisted host state:
  `Anchor`/`Offset` semantics, `Location.Chapter` indices for a given document,
  `Document.Text` rune offsets.
- Changing which error sentinel an input produces, or the set of inputs that
  are errors at all.
- Changing a cache key in a way that makes a `Relayout` from an older version
  return different results (hosts persist nothing, but benchmark baselines and
  golden tests do).
- Changing the minimum Go version or the module paths.

Not breaking: bug fixes that make previously wrong output correct, new options
whose zero value preserves existing behaviour, new diagnostics, new `Example`s
and docs. When in doubt, treat it as breaking and ask.

## Security

Do not report security issues in a public issue or PR. See
[`SECURITY.md`](SECURITY.md) for the private channel and the threat model.
