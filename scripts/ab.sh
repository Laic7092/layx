#!/usr/bin/env bash
# A/B the layx engine against DOM + CSS multi-column ("webview") in the same
# Chromium instance, and print the first batch of metrics from Layx.md §9.
#
#   scripts/ab.sh m1
#   scripts/ab.sh m1 --chars 500000 --turns 200 --repeat 5
#   scripts/ab.sh smoke --chars 50000 --repeat 1 --workers 1,4
#
# Results are JSON in snapshots/ab/<label>.json (gitignored) plus a table on
# stdout. Metrics covered: first-page time, whole-book layout, cold/hot page
# turn p50/p99, font-size reflow, line-height repagination, memory.
#
# --workers 1,4,8 runs each pool size as its own group (separate Chromium
# runs) and merges every group into the same JSON. The pool compiles wasm once,
# then N workers each copy and parse the font; init and layout are timed
# separately. Without --workers the original single-instance path is unchanged.
#
# Not covered yet: app cold start (process/WebView spawn), process-tree RSS,
# scroll jank, multi-threaded native (wasm has GOMAXPROCS=1).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

label="current"
chars=200000
turns=100
repeat=3
w=640 h=900 size=18 lh=1.65
port=${LAYX_AB_PORT:-8099}

workers=""
while [ $# -gt 0 ]; do
  case "$1" in
    --workers) workers=$2; shift 2 ;;
    --chars) chars=$2; shift 2 ;;
    --turns) turns=$2; shift 2 ;;
    --repeat) repeat=$2; shift 2 ;;
    --w) w=$2; shift 2 ;;
    --h) h=$2; shift 2 ;;
    --size) size=$2; shift 2 ;;
    --lh) lh=$2; shift 2 ;;
    --port) port=$2; shift 2 ;;
    --no-build) shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) label=$1; shift ;;
  esac
done

if [ -n "$workers" ]; then
  IFS=',' read -r -a wlist <<< "$workers"
  for n in "${wlist[@]}"; do
    case "$n" in
      ''|*[!0-9]*) echo "invalid --workers entry: '$n' (want e.g. 1,4,8)" >&2; exit 2 ;;
    esac
    [ "$n" -ge 1 ] || { echo "--workers entries must be >= 1" >&2; exit 2; }
  done
fi

find_chrome() {
  local c
  for c in "${CHROME:-}" google-chrome google-chrome-stable chromium chromium-browser \
           "$HOME/.cache/ms-playwright/chromium-"*/chrome-linux64/chrome \
           "$HOME/.cache/ms-playwright/chromium-"*/chrome-linux/chrome \
           "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe" \
           "/mnt/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
    [ -n "$c" ] && [ -x "$c" ] && { printf '%s\n' "$c"; return 0; }
  done
  return 1
}
chrome="$(find_chrome)" || { echo "no Chrome/Chromium found; set CHROME=..." >&2; exit 1; }

outdir="$root/snapshots/ab"
mkdir -p "$outdir"
work="$(mktemp -d)"
server_pid=""
chrome_pid=""
cleanup() {
  [ -n "$chrome_pid" ] && kill "$chrome_pid" 2>/dev/null || true
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# Both sides of the A/B must load the same bytes. The WebView side goes through
# @font-face, and Chrome's OTS sanitizer rejects .ttc collections, so pin a
# single-face .ttf here instead of letting build.sh pick the nicer-looking .ttc.
if [ -z "${LAYX_FONT:-}" ]; then
  for candidate in \
    "/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf" \
    "$root/render/testdata/DejaVuSerif.ttf" \
    "/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf"; do
    if [ -f "$candidate" ]; then
      export LAYX_FONT="$candidate"
      break
    fi
  done
  echo "==> ab: font ${LAYX_FONT:-<none>}"
  echo "         @font-face needs a single-face TTF; the nicest CJK fonts on"
  echo "         this box are .ttc. Droid Sans Fallback has CJK but no Latin, so"
  echo "         Latin shows as tofu on both sides of the A/B. Timings are"
  echo "         unaffected; set LAYX_FONT=<single-face ttf> to get readable text."
fi

./examples/wasm/build.sh

go build -o "$work/server" ./examples/wasm/server
LAYX_AB_OUT="$work/result.json" "$work/server" -addr "127.0.0.1:$port" -dir examples/wasm/web >"$work/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
  curl -fsS -o /dev/null "http://127.0.0.1:$port/ab.html" 2>/dev/null && break
  sleep 0.1
done

base_url="http://127.0.0.1:$port/ab.html?w=$w&h=$h&size=$size&lh=$lh&chars=$chars&turns=$turns"

run_once() {
  local url=$1 outfile=$2
  rm -f "$work/result.json"
  "$chrome" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
    --disable-dev-shm-usage --force-device-scale-factor=1 \
    --window-size="$w,$h" --incognito \
    "$url" >/dev/null 2>&1 &
  chrome_pid=$!
  for _ in $(seq 1 600); do
    [ -s "$work/result.json" ] && break
    sleep 0.1
  done
  kill "$chrome_pid" 2>/dev/null || true
  chrome_pid=""
  if [ ! -s "$work/result.json" ]; then
    echo "run produced no result; server log:" >&2
    cat "$work/server.log" >&2
    exit 1
  fi
  cp "$work/result.json" "$outfile"
}

runs=()
if [ -n "$workers" ]; then
  # Each pool size is its own group so a slow 8-worker init does not pollute
  # the 1-worker numbers. All groups land in the same JSON.
  for n in "${wlist[@]}"; do
    for i in $(seq 1 "$repeat"); do
      outfile="$work/w${n}-run$i.json"
      run_once "$base_url&workers=$n" "$outfile"
      runs+=("$outfile")
      echo "run workers=$n #$i ok"
    done
  done
else
  for i in $(seq 1 "$repeat"); do
    outfile="$work/run$i.json"
    run_once "$base_url" "$outfile"
    runs+=("$outfile")
    echo "run $i ok"
  done
fi

python3 "$root/scripts/ab_report.py" "$label" "$outdir/$label.json" "${runs[@]}"
