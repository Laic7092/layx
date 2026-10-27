# Sample EPUB files

The `epub` tests read the `.epub` files in this directory. They are **not
committed**: `.gitignore` ignores `epub/testdata/samples/*.epub`, and every test
that needs a sample skips (instead of failing) when it is absent.

## Which tests use this directory

| Test or benchmark | Needs | What it checks |
|---|---|---|
| `TestSamplesOpen` | every `*.epub` here | Opens and converts each sample into a `render.Document`; catches a truncated or corrupt file before it confuses a manual test. Skips when the directory has no samples. |
| `TestTOCOnRealSample` | `aq-zhuan.epub` | A real EPUB 2 whose NCX has seven entries, six pointing at fragments of one XHTML file. Checks table-of-contents order, nesting and resolved anchors. |
| `BenchmarkOpenAQ`, `BenchmarkBuildAQ`, `BenchmarkLayoutAQ` | `aq-zhuan.epub` | Open, build and layout cost on a real book. |

The real-book cases need an actual EPUB 2 saved here as `aq-zhuan.epub` (for
example a public-domain book from Project Gutenberg); they skip when it is
missing. Do not commit real books.

## Generate the synthetic samples

From the repository root:

```sh
go run ./tools/genepub
```

From this module directory:

```sh
cd epub
go run ../tools/genepub -out testdata/samples
```

`-out DIR` selects another output directory; the default is
`epub/testdata/samples` relative to the repository root.

The command writes two small files:

- `layx-synthetic.epub` — three chapters plus a navigation document and an NCX,
  a stylesheet with `@font-face` and `@page`, a table with `colspan`/`rowspan`, an
  inline PNG, ruby annotations, a vector-only SVG and a MathML formula. Building
  it exercises the dropped-content diagnostics.
- `layx-clean.epub` — plain text and a list. Its diagnostic list is empty.

The output is deterministic: zip entry order, entry timestamps and bytes are
fixed, so a given Go release regenerates byte-identical files. The generator
source is `tools/genepub/main.go`; change that instead of editing the generated
`.epub` files by hand, then regenerate.

Verify the samples with:

```sh
cd epub
go test ./... -run TestSamplesOpen -count=1
```
