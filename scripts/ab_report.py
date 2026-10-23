#!/usr/bin/env python3
"""Aggregate scripts/ab.sh runs and print the A/B table.

Usage: ab_report.py <label> <out.json> <run.json>...
"""
import json
import statistics
import sys


def med(vals):
    return statistics.median(vals)


def field(run, *keys):
    v = run
    for k in keys:
        v = v[k]
    return v


def pct(values, p):
    if not values:
        return 0.0
    values = sorted(values)
    i = min(len(values) - 1, max(0, -(-len(values) * p // 100) - 1))
    return values[i]


def main():
    label, out_path, *paths = sys.argv[1:]
    runs = [json.load(open(p)) for p in paths]
    if "error" in runs[0]:
        print(json.dumps(runs[0], indent=2))
        return 1

    def mm(*keys):
        return med([field(r, *keys) for r in runs])

    def allof(side, key):
        out = []
        for r in runs:
            out.extend(r[side][key])
        return out

    merged = {
        "label": label,
        "params": runs[0]["params"],
        "runs": len(runs),
        "webview": {
            "setupMs": mm("webview", "setupMs"),
        "fontLoadMs": mm("webview", "fontLoadMs"),
        "firstPageMs": mm("webview", "setupMs") + mm("webview", "fontLoadMs"),
            "pages": mm("webview", "pages"),
            "coldP50": round(pct(allof("webview", "cold"), 50), 2),
            "coldP99": round(pct(allof("webview", "cold"), 99), 2),
            "hotP50": round(pct(allof("webview", "hot"), 50), 2),
            "hotP99": round(pct(allof("webview", "hot"), 99), 2),
            "reflowMs": mm("webview", "reflowMs"),
            "repageMs": mm("webview", "repageMs"),
            "heapMB": mm("webview", "heapMB") if runs[0]["webview"]["heapMB"] is not None else None,
        },
        "native": {
            "bootMs": mm("native", "bootMs"),
            "buildMs": mm("native", "buildMs"),
            "layoutMs": mm("native", "layoutMs"),
            "pages": mm("native", "pages"),
            "coldP50": round(pct(allof("native", "cold"), 50), 2),
            "coldP99": round(pct(allof("native", "cold"), 99), 2),
            "hotP50": round(pct(allof("native", "hot"), 50), 2),
            "hotP99": round(pct(allof("native", "hot"), 99), 2),
            "reflowMs": mm("native", "reflowMs"),
            "repageMs": mm("native", "repageMs"),
            "wasmMB": mm("native", "wasmMB") if runs[0]["native"]["wasmMB"] is not None else None,
            "heapMB": mm("native", "heapMB") if runs[0]["native"]["heapMB"] is not None else None,
        },
        "meta": {
            "corpusChars": runs[0]["corpusChars"],
            "readyMs": mm("readyMs"),
            "ua": runs[0]["ua"],
        },
    }
    json.dump(merged, open(out_path, "w"), indent=2, ensure_ascii=False)

    nv, wv = merged["native"], merged["webview"]
    rows = [
        ("first page to ready (ms)",
         round(nv["bootMs"] + nv["buildMs"] + nv["layoutMs"] + nv["coldP50"], 2),
         round(wv["firstPageMs"], 2)),
        ("whole-book layout (ms)", round(nv["buildMs"] + nv["layoutMs"], 2), round(wv["setupMs"], 2)),
        ("pages", nv["pages"], wv["pages"]),
        ("cold turn p50 / p99 (ms)",
         f'{nv["coldP50"]:.2f} / {nv["coldP99"]:.2f}',
         f'{wv["coldP50"]:.2f} / {wv["coldP99"]:.2f}'),
        ("hot turn p50 / p99 (ms)",
         f'{nv["hotP50"]:.2f} / {nv["hotP99"]:.2f}',
         f'{wv["hotP50"]:.2f} / {wv["hotP99"]:.2f}'),
        ("font size 18->24 (ms)", round(nv["reflowMs"], 2), round(wv["reflowMs"], 2)),
        ("line height 1.65->1.98 (ms)", round(nv["repageMs"], 2), round(wv["repageMs"], 2)),
        ("memory (MB)", f'wasm {nv["wasmMB"]} / heap {nv["heapMB"]}', f'heap {wv["heapMB"]}'),
    ]

    print()
    print(f'A/B - {label}  corpus={merged["meta"]["corpusChars"]} chars, runs={len(runs)}')
    print(f'{"metric":32} {"native":>22} {"webview":>22} {"native/webview":>14}')
    print("-" * 94)
    for name, n, w in rows:
        ratio = ""
        if isinstance(n, (int, float)) and isinstance(w, (int, float)) and w:
            ratio = f"{n / w:.2f}x"
        print(f"{name:32} {str(n):>22} {str(w):>22} {ratio:>14}")
    print()
    print(f"written {out_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
