#!/usr/bin/env bash
# Capture per-stage rendering snapshots, and compare a stage with the one
# before it. Snapshots are build products: they live in snapshots/ and are
# gitignored, only this script is versioned.
#
#   scripts/snapshot.sh M0
#   scripts/snapshot.sh M1 --pages 1,2 --size 18 --lh 1.65 --dpr 2
#   scripts/snapshot.sh --compare M0 M1      # rebuild comparison + index only
#
# Options: --w 640 --h 900 --size 18 --lh 1.65 --theme 0 --justify 0|1
#          --dpr 2 --pages 1,2,3 --note "what changed" --no-build --port 8098
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

stage=""
compare_only=0
prev_stage=""
w=640 h=900 size=18 lh=1.65 theme=0 justify=1 dpr=2 pages="" build=1 port=${LAYX_SNAPSHOT_PORT:-8098}
note=""

while [ $# -gt 0 ]; do
  case "$1" in
    --compare)
      compare_only=1; shift
      prev_stage="${1:-}"; stage="${2:-}"; shift 2
      [ -n "$prev_stage" ] && [ -n "$stage" ] || { echo "--compare needs two stages" >&2; exit 2; } ;;
    --w) w=$2; shift 2 ;;
    --h) h=$2; shift 2 ;;
    --size) size=$2; shift 2 ;;
    --lh) lh=$2; shift 2 ;;
    --theme) theme=$2; shift 2 ;;
    --justify) justify=$2; shift 2 ;;
    --dpr) dpr=$2; shift 2 ;;
    --pages) pages=$2; shift 2 ;;
    --port) port=$2; shift 2 ;;
    --note) note=$2; shift 2 ;;
    --no-build) build=0; shift ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) stage=$1; shift ;;
  esac
done

[ -n "$stage" ] || { echo "missing stage name, e.g. M0" >&2; exit 2; }

snapdir="$root/snapshots"
out="$snapdir/$stage"

find_chrome() {
  local c
  for c in "${CHROME:-}" google-chrome google-chrome-stable chromium chromium-browser \
           "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe" \
           "/mnt/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
    [ -n "$c" ] && [ -x "$c" ] && { printf '%s\n' "$c"; return 0; }
  done
  return 1
}

to_native_path() {
  if command -v wslpath >/dev/null 2>&1 && [[ "$1" == /mnt/* || "$1" == /home/* || "$1" == /tmp/* ]]; then
    wslpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

chrome="$(find_chrome)" || { echo "no Chrome/Chromium found; set CHROME=..." >&2; exit 1; }
tmpbin="$(mktemp -d)"
trap 'rm -rf "$tmpbin"; [ -n "${server_pid:-}" ] && kill "$server_pid" 2>/dev/null || true' EXIT

go build -o "$tmpbin/snapshotimg" ./tools/snapshotimg

if [ "$compare_only" = 0 ]; then
  [ "$build" = 1 ] && ./examples/wasm/build.sh
  mkdir -p "$out"

  go build -o "$tmpbin/server" ./examples/wasm/server
  ("$tmpbin/server" -addr "127.0.0.1:$port" -dir examples/wasm/web >"$tmpbin/server.log" 2>&1) &
  server_pid=$!
  for _ in $(seq 1 100); do
    curl -fsS -o /dev/null "http://127.0.0.1:$port/shot.html" 2>/dev/null && break
    sleep 0.1
  done

  base="http://127.0.0.1:$port/shot.html?w=$w&h=$h&size=$size&lh=$lh&theme=$theme&justify=$justify&dpr=$dpr"

  dom="$("$chrome" --headless=new --disable-gpu --virtual-time-budget=15000 --dump-dom "$base&page=1" 2>/dev/null || true)"
  total="$(printf '%s' "$dom" | grep -o 'data-pages="[0-9]*"' | head -1 | tr -dc '0-9')"
  [ -n "$total" ] || { echo "snapshot page failed to render; server log:" >&2; cat "$tmpbin/server.log" >&2; exit 1; }
  echo "==> $stage: $total pages at ${w}x${h}, ${size}px, dpr $dpr"

  list="$pages"
  [ -n "$list" ] || list="$(seq 1 "$total" | paste -sd, -)"

  IFS=',' read -ra wanted <<<"$list"
  for p in "${wanted[@]}"; do
    p="$(echo "$p" | tr -d ' ')"
    [ "$p" -le "$total" ] || continue
    shot="$out/page-$p.png"
    "$chrome" --headless=new --disable-gpu --hide-scrollbars \
      --force-device-scale-factor="$dpr" --window-size="$w,$h" \
      --virtual-time-budget=15000 \
      --screenshot="$(to_native_path "$shot")" "$base&page=$p" >/dev/null 2>&1
    [ -s "$shot" ] || { echo "failed to capture page $p" >&2; exit 1; }
    printf 'ok  page %s/%s\n' "$p" "$total"
  done

  {
    printf 'stage: %s\n' "$stage"
    printf 'date: %s\n' "$(date -Iseconds)"
    printf 'commit: %s\n' "$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    printf 'pages: %s\n' "$total"
    printf 'params: w=%s h=%s size=%s lh=%s theme=%s justify=%s dpr=%s\n' "$w" "$h" "$size" "$lh" "$theme" "$justify" "$dpr"
    if git diff --quiet -- . 2>/dev/null && git diff --cached --quiet -- . 2>/dev/null; then
      printf 'tree: clean\n'
    else
      printf 'tree: dirty\n'
    fi
    [ -n "$note" ] && printf 'note: %s\n' "$note"
  } >"$out/meta.txt"
fi

# ------------------------------------------------------------------ compare

compare_stage() {
  local before="$1" after="$2"
  local cmpdir="$snapdir/compare/$before-$after"
  mkdir -p "$cmpdir"
  local found=0
  for a in "$snapdir/$before"/page-*.png; do
    [ -e "$a" ] || continue
    local n; n="$(basename "$a")"
    local b="$snapdir/$after/$n"
    [ -e "$b" ] || continue
    found=1
    "$tmpbin/snapshotimg" side -gap 24 -out "$cmpdir/$n" "$a" "$b"
    "$tmpbin/snapshotimg" diff -out "$cmpdir/${n%.png}-diff.png" "$a" "$b"
    printf '%s: %s\n' "$n" "$("$tmpbin/snapshotimg" stats "$a" "$b")"
  done
  [ "$found" = 1 ]
}

if [ -n "$prev_stage" ] && [ "$compare_only" = 1 ]; then
  [ -d "$snapdir/$prev_stage" ] && [ -d "$snapdir/$stage" ] || { echo "missing stage dirs" >&2; exit 2; }
  compare_stage "$prev_stage" "$stage"
elif [ "$compare_only" = 0 ]; then
  # Compare against the most recently captured other stage, if any.
  prev=""
  prev_time=0
  for d in "$snapdir"/*/; do
    [ -d "$d" ] || continue
    name="$(basename "$d")"
    [ "$name" = "$stage" ] && continue
    [ "$name" = "compare" ] && continue
    t="$(stat -c %Y "$d" 2>/dev/null || echo 0)"
    if [ "$t" -ge "$prev_time" ]; then prev_time="$t"; prev="$name"; fi
  done
  [ -n "$prev" ] && compare_stage "$prev" "$stage" || true
fi

# Index: one section per captured stage, newest last.
{
  echo "# Rendering snapshots"
  echo
  echo "Generated by \`scripts/snapshot.sh\`; PNGs are gitignored build products."
  echo
  for d in "$snapdir"/*/; do
    name="$(basename "$d")"
    [ "$name" = "compare" ] && continue
    [ -f "$d/meta.txt" ] || continue
    echo "## $name"
    echo
    echo '```'
    cat "$d/meta.txt"
    echo '```'
    echo
    for p in "$d"page-*.png; do
      [ -e "$p" ] && echo "![$(basename "$p")]($name/$(basename "$p"))"
    done
    if [ -d "$snapdir/compare" ]; then
      for c in "$snapdir/compare/$name"-*; do
        [ -d "$c" ] || continue
        echo
        echo "对比 \`$(basename "$c")\`："
        echo
        for p in "$c"/page-*.png; do
          [ -e "$p" ] || continue
          rel="${p#"$snapdir/"}"
          echo "![$(basename "$p")]($rel)"
        done
      done
    fi
    echo
  done
} >"$snapdir/index.md"

echo "==> snapshots in $out, comparison in $snapdir/compare, index in $snapdir/index.md"
