#!/usr/bin/env bash
# Compare layx line boxes with Chrome's, line by line, on the same text and the
# same font bytes.
#
#   scripts/linecmp.sh cjk
#   scripts/linecmp.sh punct --trim both
#   scripts/linecmp.sh latin --font /usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf
#   scripts/linecmp.sh mixed-ja --size 20 --w 700
#
# Modes pick a corpus from scripts/corpora and a font that covers it:
#
#   cjk       plain Chinese prose                     Droid Sans Fallback
#   punct     Chinese dialogue, dense punctuation     Droid Sans Fallback
#   latin     English prose                           DejaVu Serif
#   mixed-ja  Japanese with Latin words and digits    IPA Gothic
#
# Both sides must load the same font bytes: the browser picks it up through
# @font-face, and Chrome's OTS sanitizer rejects .ttc collections, so the font
# has to be a single-face .ttf. --font overrides the default.
#
# --trim controls CSS text-spacing-trim on the browser side. "default" leaves
# Chrome's own default (which trims fullwidth punctuation), "space-all" turns
# the trimming off, and "both" runs each and prints both rows: the difference
# between the two rows is exactly what punctuation compression would recover.
# A build that parses the property but ignores it during line breaking gets a
# note, so the space-all row is never read as a real result. Pass --chrome to
# choose the browser, since support differs between them.
#
# Exit status is 0 even when the rows differ, because the punctuation gap is a
# known, documented one. Pass --strict to make a mismatch fail the run.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

mode=cjk
font=""
w=640 h=900 size=18 lh=1.65
trim=default
strict=0
keep=""
while [ $# -gt 0 ]; do
  case "$1" in
    cjk|punct|latin|mixed-ja) mode=$1; shift ;;
    --font) font=$2; shift 2 ;;
    --w) w=$2; shift 2 ;;
    --h) h=$2; shift 2 ;;
    --size) size=$2; shift 2 ;;
    --lh) lh=$2; shift 2 ;;
    --trim) trim=$2; shift 2 ;;
    --chrome) CHROME=$2; shift 2 ;;
    --strict) strict=1; shift ;;
    --keep) keep=$2; shift 2 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

corpus="$root/scripts/corpora/$mode.txt"
[ -f "$corpus" ] || { echo "no corpus for mode $mode" >&2; exit 2; }

if [ -z "$font" ]; then
  case "$mode" in
    cjk|punct) candidates=("/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf") ;;
    latin) candidates=("/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf") ;;
    mixed-ja) candidates=("/usr/share/fonts/opentype/ipafont-gothic/ipag.ttf" "/usr/share/fonts/truetype/fonts-japanese-gothic.ttf") ;;
  esac
  for c in "${candidates[@]}"; do [ -f "$c" ] && { font=$c; break; }; done
fi
[ -n "$font" ] && [ -f "$font" ] || { echo "no font for mode $mode; pass --font FILE" >&2; exit 1; }
case "$font" in
  *.ttc) echo "error: $font is a collection; @font-face needs a single-face .ttf" >&2; exit 1 ;;
esac

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

work="$(mktemp -d)"
server_pid=""
chrome_pid=""
cleanup() {
  [ -n "$chrome_pid" ] && kill "$chrome_pid" 2>/dev/null
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null
  wait 2>/dev/null
  if [ -n "$keep" ]; then
    mkdir -p "$keep" && cp -r "$work"/. "$keep"/ && echo "kept: $keep"
  fi
  rm -rf "$work"
}
trap cleanup EXIT

# The server's document root is a scratch copy: the page, the font bytes and the
# corpus travel together, so nothing generated lands in the repository.
mkdir -p "$work/fonts"
cp examples/wasm/web/lines.html examples/wasm/web/lines.js "$work/"
cp "$font" "$work/fonts/book.font"
cp "$corpus" "$work/corpus.txt"

go build -o "$work/server" ./examples/wasm/server || exit 1

echo "==> linecmp: mode=$mode font=$font viewport=${w}x${h} size=$size lh=$lh"
LPX_TEXT="$corpus" LPX_OUT="$work/native.txt" LPX_FONT="$font" \
  LPX_VIEWPORT="${w}x${h}" LPX_SIZE="$size" \
  go test ./render -run TestLineBoxes -count=1 -v 2>&1 |
  grep -E "line boxes|FAIL|panic" | sed 's/^/    native: /'
[ -s "$work/native.txt" ] || { echo "native dump is empty" >&2; exit 1; }

run_browser() {
  local trimarg=$1 out=$2
  local url="http://127.0.0.1:$port/lines.html?w=$w&h=$h&size=$size&lh=$lh"
  [ -n "$trimarg" ] && url="$url&trim=$trimarg"
  rm -f "$work/result.json"
  "$chrome" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
    --disable-dev-shm-usage --force-device-scale-factor=1 \
    --window-size="$w,$h" --incognito "$url" >/dev/null 2>&1 &
  chrome_pid=$!
  for _ in $(seq 1 300); do [ -s "$work/result.json" ] && break; sleep 0.1; done
  kill "$chrome_pid" 2>/dev/null
  chrome_pid=""
  [ -s "$work/result.json" ] || { echo "browser produced no result" >&2; exit 1; }
  python3 - "$work" "$out" <<'PY'
import json, sys
work, out = sys.argv[1], sys.argv[2]
d = json.load(open(work + '/result.json'))
open(work + '/' + out, 'w').write("\n".join(l['t'] for l in d['lines']) + "\n")
state = {k: v for k, v in d.items() if k not in ('lines', 'perPara')}
open(work + '/' + out + '.json', 'w').write(json.dumps(state))
PY
}

port=${LAYX_LINECMP_PORT:-8091}
LAYX_AB_OUT="$work/result.json" "$work/server" -addr "127.0.0.1:$port" -dir "$work" >"$work/server.log" 2>&1 &
server_pid=$!
ready=0
for _ in $(seq 1 100); do
  curl -fsS -o /dev/null "http://127.0.0.1:$port/lines.html" 2>/dev/null && { ready=1; break; }
  sleep 0.1
done
if [ "$ready" = 0 ]; then
  echo "server did not start; log:" >&2
  cat "$work/server.log" >&2
  exit 1
fi

case "$trim" in
  both) trims=("" "space-all") ;;
  default) trims=("") ;;
  space-all) trims=("space-all") ;;
  *) trims=("$trim") ;;
esac

printf '\n%-9s %-10s %8s %7s %7s %10s %8s\n' "mode" "trim" "native" "chrome" "pages" "identical" "chars/l"
fail=0
for t in "${trims[@]}"; do
  label=${t:-default}
  run_browser "$t" "chrome-$label.txt"
  python3 - "$work/native.txt" "$work/chrome-$label.txt" "$work/chrome-$label.txt.json" "$mode" "$label" <<'PY' || fail=1
import json, sys
nat_path, chr_path, meta_path, mode, label = sys.argv[1:6]
nat = [l.rstrip('\n').split('\t') for l in open(nat_path)]
ntext = [r[6] for r in nat]
ctext = [l.rstrip('\n') for l in open(chr_path)]
meta = json.load(open(meta_path))
same = sum(1 for a, b in zip(ntext, ctext) if a == b)
n = max(len(ntext), len(ctext)) or 1
cov = meta.get('coverage', {})
printf = ('%-9s %-10s %8d %7d %7s %10s %8.1f'
          % (mode, label, len(ntext), len(ctext), meta.get('pageCount', '?'),
             '%d/%d' % (same, n),
             (sum(len(t) for t in ntext) / max(1, len(ntext)))))
print(printf)
if label != 'default' and not meta.get('trimSupported', True):
    print('    note: this browser ignores text-spacing-trim: %s' % label)
if same != n:
    for i, (a, b) in enumerate(zip(ntext, ctext)):
        if a != b:
            print('    first difference at line %d' % (i + 1))
            print('      layx:  %r' % a)
            print('      chrome:%r' % b)
            break
wanted = {'cjk': 'cjk', 'punct': 'punct', 'latin': 'latin', 'mixed-ja': 'kana'}.get(mode)
if wanted and not cov.get(wanted):
    print('    warning: the font does not draw %s; the browser substitutes another face' % wanted)
sys.exit(0 if same == n else 1)
PY
done

if [ "$fail" = 1 ] && [ "$strict" = 1 ]; then
  echo "==> linecmp: line breaking differs" >&2
  exit 1
fi
exit 0
