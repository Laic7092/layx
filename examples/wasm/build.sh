#!/usr/bin/env bash
# Build the wasm binary and stage the browser assets.
#
#   ./examples/wasm/build.sh            # auto-detect a font
#   LAYX_FONT=/path/to/font.ttf ./examples/wasm/build.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
out="$here/web"

mkdir -p "$out/fonts"

echo "==> go build (js/wasm)"
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$out/layx.wasm" "$here"

goroot="$(go env GOROOT)"
cp "$goroot/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
cp "$goroot/lib/wasm/wasm_exec_node.js" "$out/wasm_exec_node.js"

# The demo mixes Chinese and Latin text, so prefer a CJK font. The last two
# entries are Latin-only fallbacks: the demo still runs, but CJK renders as tofu.
font="${LAYX_FONT:-}"
if [ -z "$font" ]; then
  for candidate in \
    "$HOME/.local/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf" \
    "/usr/share/fonts/truetype/fonts-japanese-gothic.ttf" \
    "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc" \
    "/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc" \
    "/usr/share/fonts/truetype/arphic/uming.ttc" \
    "/System/Library/Fonts/PingFang.ttc" \
    "/System/Library/Fonts/NotoSansCJK-Regular.ttc" \
    "C:/Windows/Fonts/msyh.ttc" \
    "$root/render/testdata/DejaVuSerif.ttf" \
    "/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf"; do
    if [ -f "$candidate" ]; then
      font="$candidate"
      break
    fi
  done
fi

case "$font" in
  *DejaVuSerif.ttf)
    echo "warning: no CJK font found; Chinese text will render as tofu." >&2
    echo "         set LAYX_FONT to a CJK .ttf/.otf/.ttc to fix it." >&2
    ;;
esac

if [ -z "$font" ] || [ ! -f "$font" ]; then
  echo "error: no font found; set LAYX_FONT to a .ttf/.otf/.ttc file" >&2
  exit 1
fi

cp "$font" "$out/fonts/book.font"
echo "==> font: $font"
echo "==> wasm: $(du -h "$out/layx.wasm" | cut -f1), font: $(du -h "$out/fonts/book.font" | cut -f1)"
echo "==> serve: go run ./examples/wasm/server  (or: python3 -m http.server -d $out 8088)"
