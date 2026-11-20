# Security policy

layx is a layout library, not a service: it has no network access, starts no
background goroutines of its own, and writes no files. Its security surface is
therefore small but not empty — it parses and lays out **bytes the host does
not trust**, inside the host's own process.

The threat model below is the one the project designs and tests against. If
you find a gap between it and the implementation, that is a security bug, not
just a bug.

## Supported versions

layx is pre-1.0. Only the latest released minor version of each module is
supported; older minor versions do not receive security fixes.

| Module | Supported version |
| --- | --- |
| `github.com/laic7092/layx/render` | latest `v0.x` release |
| `github.com/laic7092/layx/epub` | latest `epub/v0.x` release (tagged separately) |

The two modules are released together and must be upgraded together when a
security fix spans both.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Use GitHub's private vulnerability reporting for this repository:

1. Open <https://github.com/Laic7092/layx/security/advisories/new>.
2. Describe the impact, the input that triggers it, and the version you tested.
3. If possible, include a minimal reproducer (a short Go program, or a
   deliberately malformed EPUB/XHTML/CSS file). Keep it small and do not attach
   the contents of a book you do not own.

If private reporting is unavailable, contact the maintainer listed in the
repository's commit history and ask for a private channel. Do not include
exploit details in a public issue, pull request, or discussion.

We aim to:

- acknowledge a report within 7 days;
- confirm or reject the report and agree on a disclosure date within 30 days;
- credit reporters in the advisory unless they prefer to stay anonymous.

## Threat model

### What is untrusted

The primary untrusted input is **bytes from a file or user**: an EPUB
container (zip), the XHTML, CSS, OPF and nav documents inside it, and any other
markup or style text a host feeds the EPUB converter. A host may also hand the
layout engine a `render.Document` built from untrusted text (pasted chapters,
downloaded HTML) and untrusted numeric options (fractions computed from
sliders, `float32` overflow).

The library must stay safe for all of it:

- **No panic may escape the library.** Layout work runs on helper goroutines
  that a host cannot recover; a panic there would end the process. Every
  failure is returned as an `error`, and a panic recovered at a worker boundary
  is returned as an error wrapping `render.ErrInternal`. `ErrInternal` means
  the library itself has a bug: treat the output as invalid, don't assume the
  input is bad, and report it.
- **Malformed input is an error, not a crash and not unbounded work.** Beyond
  the structural validation (`ErrInvalidDocument`, `ErrInvalidOptions`),
  `Limits` caps nodes, depth, text runes, image pixels, pages, tiles and line
  boxes; `MinTileHeight` stops a tile-height setting from multiplying the tile
  count. Exceeding a limit returns `ErrLimitExceeded` naming the measured
  value and the cap. A document that is valid but enormous is still a denial of
  service if the host leaves limits unbounded — see "Host responsibilities".
- **NaN and ±Inf are rejected.** Floating-point options and styles are
  validated with `ErrInvalidOptions` (and `DocumentError` names the field), so
  a bad slider value cannot flow into geometry, display lists, or page counts.
- **Content that is dropped is reported, not hidden.** Missing fonts, missing
  glyphs (tofu) and content the IR or the EPUB converter cannot express are
  reported through `Options.OnIssue` / `Layout.Issues` and
  `epub.Options.OnIssue` / `Book.Issues`. Diagnostics are data, not errors:
  the layout still succeeds unless the input is invalid.

### What is trusted

- **Font files** passed to `render.NewFontSet`. They are parsed by the
  `go-text/typesetting` dependency; a malicious font is an upstream parser
  issue first. Report it upstream and to us.
- **The host's `paint.Painter`.** It runs host code. `Page.Draw` and the other
  draw methods deliberately do not recover panics from the painter; a painter
  must handle its own failures and keep its state consistent.
- **The host's filesystem, environment and configuration.** layx performs no
  I/O on its own. `epub.Open` reads the file the host names; the host is
  responsible for path control and for any zip extraction it does itself.

### Out of scope

- Vulnerabilities in dependencies (`go-text/typesetting`,
  `golang.org/x/net`, `golang.org/x/image`). Report them upstream; we will
  update the dependency and ship a release.
- Panics or memory corruption inside a host-provided `paint.Painter`, image
  decoder, or font loader that runs after the library's API boundary.
- Denial of service from a host that deliberately disables limits (e.g.
  setting `Limits` fields to very large values) or trusts remote input without
  a size cap on the file itself.
- Rendering fidelity (tofu, wrong page breaks, incorrect hyphenation). Those
  are ordinary bugs; please report them with a normal issue.
- Secrets in snapshots, sample books or benchmark corpora. Those files are not
  committed; see `CONTRIBUTING.md`.

## Host responsibilities

The library keeps its side of the contract; a host still needs to:

1. Cap the size of a file before opening it, and sandbox any extraction.
2. Keep the default `Limits` unless it has a measured reason not to, and
   surface `ErrLimitExceeded` to the user as "this book is too large" rather
   than retrying with higher limits.
3. Treat `ErrInternal` as a library bug: fail the current layout, keep the host
   process alive, and report the input if it can be shared safely.
4. Handle panics from its own `Painter`, image decoder and UI code.

## Disclosure

We prefer coordinated disclosure. Once a fix is merged we publish a GitHub
Security Advisory, credit the reporter, and describe the affected versions and
the upgrade path. Only supported versions receive backports.
