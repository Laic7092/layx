# Pull request

## Summary

<!-- What problem does this PR solve, and how? One or two paragraphs. -->

## Related issue / audit

<!-- Fixes #..., or references docs/audit.md §... for the audit fixes. -->

## Type of change

- [ ] Bug fix (no public API change)
- [ ] New public API (additive)
- [ ] Behaviour or default change
- [ ] Breaking change (pre-1.0 minor bump; note the migration below)
- [ ] Docs / examples / tooling only
- [ ] Performance / memory change (numbers below)

## Checklist

### Required — CI runs the same gate, but catch it early

- [ ] `gofmt -l` on the changed Go files prints nothing
- [ ] `go vet ./...` (and `cd epub && go vet ./...`)
- [ ] `go test ./...` (and `cd epub && go test ./...`)
- [ ] `go test ./render -race -count=1` for concurrency/layout changes
- [ ] Fuzz smoke for parser/shaping changes:
      `go test ./render -run '^$' -fuzz FuzzLayoutText -fuzztime 30s`,
      `cd epub && go test -run '^$' -fuzz FuzzConvertChapter -fuzztime 30s`
- [ ] Root module still builds with Go 1.21 in plain module mode
      (`GOWORK=off go build ./...`); no local `replace` in `epub/go.mod`
- [ ] New behaviour has a test; a bug fix has a regression test that fails
      before the fix

### Public API / behaviour

- [ ] Doc comments explain the contract, not just the mechanics; comments are
      in English
- [ ] New options have a zero value that preserves existing behaviour
- [ ] New line-breaking/pagination/painting inputs are added to
      `BreakKey`/`PageKey`/`PaintKey` (policy fields go inside `TextRules`)
- [ ] Errors wrap a sentinel (`ErrInvalidDocument`, `ErrInvalidOptions`,
      `ErrNoFonts`, `ErrLimitExceeded`, `ErrClosed`, `ErrInternal`) and use
      `*DocumentError` where chapter/node/style context exists
- [ ] Untrusted input still cannot panic: hostile/empty/huge input covered

### Docs and distribution

- [ ] `CHANGELOG.md` updated for user-visible changes
- [ ] `README.md` / `README.en.md` / `docs/api.md` updated for changed public
      surface; examples still compile
- [ ] No new direct dependency, or the addition is justified in the description
- [ ] Test data/fonts carry their license text; sample EPUBs are generated, not
      committed
- [ ] No generated artifacts, golden-regeneration noise, or unrelated formatting

## Breaking-change notes

<!-- Delete if not applicable: what breaks, and how a host migrates. -->

## Measurements

<!-- For performance/memory changes: corpus, command, before/after. Ratios, not
     absolute milliseconds, across machines. -->

## Review notes

<!-- Anything the reviewer should look at first; known gaps. -->
