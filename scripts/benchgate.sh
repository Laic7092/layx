#!/usr/bin/env bash
# benchgate.sh — coarse benchmark regression gate for the render package.
#
# Usage:
#   scripts/benchgate.sh            # compare against scripts/bench_baseline.txt
#   scripts/benchgate.sh --update   # rewrite the baseline from this machine
#
# Environment:
#   BENCH_TOL     allowed slowdown factor (default 1.5)
#   BENCH_COUNT   repetitions per benchmark; the median is compared (default 3)
#   BENCH_TIME    -benchtime used per repetition (default 1s)
#   LAYX_CJK_FONT CJK font used by the layout benchmarks (auto-detected otherwise)
#
# NOTE: ns/op depends on the CPU and on the number of cores. The committed
# baseline was captured on one development machine (see the header of
# scripts/bench_baseline.txt), so this gate is deliberately coarse: it catches
# large regressions, not a few percent of jitter. Re-capture the reference on
# the machine that should define "normal" with --update.
set -euo pipefail
export GOFLAGS=-mod=readonly

cd "$(dirname "${BASH_SOURCE[0]}")/.."

baseline=scripts/bench_baseline.txt
count=${BENCH_COUNT:-3}
benchtime=${BENCH_TIME:-1s}
tol=${BENCH_TOL:-1.5}

benchmarks=(
	"BenchmarkLayoutScale/workers=1"
	"BenchmarkLayout200K"
	"BenchmarkPageDraw"
	"BenchmarkHitTest"
)

update=0
for arg in "$@"; do
	case "$arg" in
	-update | --update) update=1 ;;
	-h | --help)
		sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown argument: $arg (try --help)" >&2
		exit 2
		;;
	esac
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# -bench splits its regular expression on unbracketed slashes into one regexp
# per identifier level, so the nested BenchmarkLayoutScale/workers=1 gets its
# own invocation; the top-level benchmarks are matched in a second one.
layout_pattern='^BenchmarkLayoutScale/workers=1$'
plain_pattern='^(BenchmarkLayout200K|BenchmarkPageDraw|BenchmarkHitTest)$'

echo "==> benchgate: $(go version)"
echo "==> benchmarks: ${#benchmarks[@]}, count=$count, benchtime=$benchtime, tolerance=${tol}x"
if [[ -r /proc/loadavg ]]; then
	load=$(cut -d' ' -f1 /proc/loadavg)
	cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 1)
	awk -v sysload="$load" -v ncpu="$cpus" 'BEGIN {
		if (sysload + 0 > ncpu + 0) printf "==> warning: load average %s on %s CPUs; timings may be noisy\n", sysload, ncpu
	}'
fi
: >"$tmp/out"
go test ./render -run '^$' -bench "$layout_pattern" -benchtime="$benchtime" -count="$count" -timeout 20m | tee -a "$tmp/out"
go test ./render -run '^$' -bench "$plain_pattern" -benchtime="$benchtime" -count="$count" -timeout 20m | tee -a "$tmp/out"

# Keep only "name ns/op" pairs; strip the "-<GOMAXPROCS>" suffix.
awk '$4 == "ns/op" { name = $1; sub(/-[0-9]+$/, "", name); print name, $3 }' "$tmp/out" >"$tmp/values"

median() { # median of the numbers in $1 (one per line)
	sort -n "$1" | awk '{ a[NR] = $1 } END { if (NR > 0) print a[int((NR + 1) / 2)] }'
}

for name in "${benchmarks[@]}"; do
	safe=${name//[^A-Za-z0-9]/_}
	awk -v n="$name" '$1 == n { print $2 }' "$tmp/values" >"$tmp/$safe"
done

if ((update)); then
	cpu=$(sed -n 's/^cpu: //p' "$tmp/out" | head -n 1)
	{
		echo "# Benchmark baselines for scripts/benchgate.sh (median ns/op)."
		echo "# Captured on: ${cpu:-unknown CPU} / $(go version) / $(go env GOOS)/$(go env GOARCH)"
		echo "#"
		echo "# Coarse by design: absolute ns/op differs between machines and CI hardware,"
		echo "# and the gate only allows BENCH_TOL (default 1.5x) of slowdown."
		echo "# Refresh with: scripts/benchgate.sh --update"
		echo "# The layout benchmarks need a CJK font (LAYX_CJK_FONT or a system one)."
		for name in "${benchmarks[@]}"; do
			safe=${name//[^A-Za-z0-9]/_}
			value=$(median "$tmp/$safe")
			if [[ -n $value ]]; then
				printf '%s\t%s\n' "$name" "$value"
			else
				printf '# NOT MEASURED (skipped benchmark?) %s\n' "$name"
			fi
		done
	} >"$baseline"
	echo "==> baseline written to $baseline"
	exit 0
fi

[[ -f $baseline ]] || {
	echo "error: $baseline not found; run scripts/benchgate.sh --update" >&2
	exit 1
}

declare -a base_names=() base_values=()
while IFS=$'\t' read -r name value; do
	[[ -z $name || $name == \#* ]] && continue
	base_names+=("$name")
	base_values+=("$value")
done <"$baseline"

lookup_base() { # name -> baseline ns/op ("" when absent)
	local i
	for i in "${!base_names[@]}"; do
		if [[ ${base_names[$i]} == "$1" ]]; then
			echo "${base_values[$i]}"
			return 0
		fi
	done
	return 0
}

status=0
checked=0
for name in "${benchmarks[@]}"; do
	safe=${name//[^A-Za-z0-9]/_}
	current=$(median "$tmp/$safe")
	if [[ -z $current ]]; then
		printf 'SKIP  %s (not measured; install a CJK font for the layout benchmarks)\n' "$name"
		continue
	fi
	base=$(lookup_base "$name")
	if [[ -z $base ]]; then
		printf 'NEW   %s: %s ns/op (no baseline entry; run --update)\n' "$name" "$current"
		continue
	fi
	checked=$((checked + 1))
	if ! awk -v name="$name" -v base="$base" -v cur="$current" -v tol="$tol" 'BEGIN {
		ratio = cur / base
		if (ratio > tol) {
			printf "FAIL  %s: %.0f ns/op vs baseline %.0f ns/op (%.2fx slower; tolerance %.2fx)\n", name, cur, base, ratio, tol
			exit 1
		}
		if (ratio < 1 / tol) {
			printf "FAST  %s: %.0f ns/op vs baseline %.0f ns/op (%.2fx; consider --update)\n", name, cur, base, ratio
			exit 0
		}
		printf "OK    %s: %.0f ns/op vs baseline %.0f ns/op (%.2fx; tolerance %.2fx)\n", name, cur, base, ratio, tol
	}'; then
		status=1
	fi
done

if ((checked == 0)); then
	echo "error: no measured benchmark matched the baseline in $baseline" >&2
	exit 1
fi
if ((status != 0)); then
	echo "benchgate: FAILED" >&2
	exit 1
fi
echo "benchgate: OK ($checked benchmarks within ${tol}x of baseline)"
