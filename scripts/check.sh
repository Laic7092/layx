#!/usr/bin/env bash
# check.sh — everything a contributor should run before sending a change.
#
# Usage:
#   scripts/check.sh            # gofmt, go vet, tests, cross-builds
#   scripts/check.sh --fuzz     # ... plus short fuzz smoke tests
#
# Environment:
#   FUZZTIME   how long each fuzz target runs (default 15s)
#
# The repository is a Go workspace (root module + epub/), so all commands run
# in workspace mode; -mod=readonly is accepted there and in plain module mode.
set -euo pipefail
export GOFLAGS=-mod=readonly

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fuzz=0
for arg in "$@"; do
	case "$arg" in
	--fuzz) fuzz=1 ;;
	-h | --help)
		sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown argument: $arg (try --help)" >&2
		exit 2
		;;
	esac
done

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

section() {
	printf '\n==> %s\n' "$*"
}

section "gofmt"
unformatted=$(git ls-files --cached --others --exclude-standard -z -- '*.go' | xargs -0 gofmt -l)
if [[ -n $unformatted ]]; then
	printf '%s\n' "$unformatted"
	fail "gofmt needs to be run on the files above"
fi
echo "ok"

section "go vet (root module)"
go vet ./...
echo "ok"

section "go test (root module)"
go test ./... -count=1

section "go vet (epub module)"
(cd epub && go vet ./...)
echo "ok"

section "go test (epub module)"
(cd epub && go test ./... -count=1)

section "cross-compile (root module)"
targets=(
	"linux/386"
	"linux/arm64"
	"windows/amd64"
	"darwin/arm64"
	"js/wasm"
	"freebsd/amd64"
)
for target in "${targets[@]}"; do
	goos=${target%/*}
	goarch=${target#*/}
	printf '  build %s/%s\n' "$goos" "$goarch"
	GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build ./...
done

if ((fuzz)); then
	section "fuzz smoke (${FUZZTIME:-15s} each)"
	run_fuzz() { # dir, package, target
		local dir=$1 pkg=$2 target=$3 list
		list=$( (cd "$dir" && go test "$pkg" -list "^${target}$") || true)
		if ! grep -qxF "$target" <<<"$list"; then
			printf '  skip  %s (%s not present)\n' "$target" "$pkg"
			return 0
		fi
		printf '  fuzz  %s (%s)\n' "$target" "$pkg"
		(cd "$dir" && go test "$pkg" -run '^$' -fuzz "^${target}$" -fuzztime "${FUZZTIME:-15s}")
	}
	run_fuzz . ./render FuzzLayoutText
	run_fuzz epub ./ FuzzConvertChapter
	run_fuzz epub ./ FuzzParseCSS
	run_fuzz epub ./ FuzzParseNCX
fi

section "done"
echo "all checks passed"
