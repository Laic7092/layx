#!/usr/bin/env python3
"""Aggregate scripts/ab.sh runs and print the A/B table.

Usage: ab_report.py <label> <out.json> <run.json>...

Runs are grouped by their `params.workers` value. A single group produces the
original merged document; `--workers 1,4` produces one merged document per
group under `groups`, with each group's worker-pool numbers reported separately
(pool init, pool wall clock, page-count match).
"""
import json
import statistics
import sys


def med(vals):
    vals = [v for v in vals if v is not None]
    if not vals:
        return None
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


def group_runs(runs):
    groups = {}
    for r in runs:
        workers = r.get("params", {}).get("workers", 0) or 0
        groups.setdefault(int(workers), []).append(r)
    return groups


def merge(runs, label, workers):
    def mm(*keys):
        return med([field(r, *keys) for r in runs])

    def allof(side, key):
        out = []
        for r in runs:
            out.extend(r[side][key])
        return out

    merged = {
        "label": label,
        "workers": workers,
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
            "fontCoverage": runs[0].get("fontCoverage"),
            "corpusParagraphsDropped": runs[0].get("corpusParagraphsDropped"),
            "readyMs": mm("readyMs"),
            "ua": runs[0]["ua"],
        },
    }

    # Single-instance multi-chapter baseline, present only for --workers runs.
    books = [r.get("native", {}).get("book") for r in runs]
    if books and all(books):
        merged["native"]["bookMs"] = med([b["ms"] for b in books])
        merged["native"]["bookLayoutMs"] = med([b["layoutMs"] for b in books])
        merged["native"]["bookBuildMs"] = med([b["buildMs"] for b in books])
        merged["native"]["bookPages"] = med([b["pages"] for b in books])
        merged["native"]["bookChapters"] = books[0]["chapters"]
        merged["native"]["bookChapterPages"] = books[0]["chapterPages"]

    pools = [r.get("pool") for r in runs if r.get("pool")]
    if workers > 0 and pools and any("error" in p for p in pools):
        merged["pool"] = {
            "workers": workers,
            "error": next(p.get("error") for p in pools if "error" in p),
            "fallbackToSingle": True,
        }
    elif workers > 0 and pools:
        def pm(*keys):
            return med([field(p, *keys) for p in pools])

        wall = pm("layoutMs")
        book_ms = med([b["ms"] for b in books]) if books and all(books) else None
        book_pages = med([b["pages"] for b in books]) if books and all(books) else None
        worker_inits = [p.get("workerInitMs") for p in pools if p.get("workerInitMs")]
        pages_match = []
        chapter_match = []
        for r in runs:
            p = r.get("pool") or {}
            book = r.get("native", {}).get("book") or {}
            if "error" in p:
                pages_match.append(False)
                chapter_match.append(False)
                continue
            pages_match.append(p.get("pages") == book.get("pages"))
            chapter_match.append(bool(p.get("chapterPagesMatch")))
        merged["pool"] = {
            "workers": workers,
            "chapters": pools[0].get("chapters"),
            "initMs": pm("initMs"),
            "compileMs": pm("compileMs"),
            "workerInitMs": [
                med([wi[i] for wi in worker_inits]) for i in range(len(worker_inits[0]))
            ] if worker_inits else [],
            "moduleShared": all(p.get("moduleShared") for p in pools),
            "layoutMs": wall,
            "workerMs": pm("workerMs"),
            "pages": pm("pages"),
            "fallbacks": sum(p.get("fallbacks", 0) for p in pools),
            "bookMs": book_ms,
            "bookPages": book_pages,
            "pagesMatch": all(pages_match),
            "chapterPagesMatch": all(chapter_match),
            "chapterPages": pools[0].get("chapterPages"),
            "speedup": round(book_ms / wall, 2) if book_ms and wall else None,
            "wallRatio": round(wall / book_ms, 3) if book_ms and wall else None,
        }
    else:
        merged["pool"] = None
    return merged


def coverage_note(merged):
    """Warn when the corpus had to skip paragraphs the pinned face cannot draw."""
    cov = merged.get("meta", {}).get("fontCoverage")
    dropped = merged.get("meta", {}).get("corpusParagraphsDropped")
    if cov is None:
        return None
    missing = [k for k, v in cov.items() if not v]
    if not missing and not dropped:
        return None
    parts = []
    if missing:
        parts.append("font lacks " + ", ".join(missing))
    if dropped:
        parts.append(f"{dropped} paragraph(s) dropped from the corpus")
    return ("; ".join(parts) +
            " - the browser substitutes another face for those glyphs, so the"
            " two sides would measure different text")


def print_table(merged, out_path):
    nv, wv = merged["native"], merged["webview"]
    pool = merged.get("pool")
    workers = merged.get("workers", 0)
    wlabel = f"wasm×{workers}" if workers else "wasm×N"
    single = merged.get("native", {}).get("bookMs")

    def poolv(key, fmt="{:.2f}"):
        if not pool or "error" in pool or pool.get(key) is None:
            return "-"
        return fmt.format(pool[key])

    rows = [
        ("first page to ready (ms)",
         round(nv["bootMs"] + nv["buildMs"] + nv["layoutMs"] + nv["coldP50"], 2),
         round(wv["firstPageMs"], 2),
         "-"),
        ("whole-book layout (ms)", round(nv["buildMs"] + nv["layoutMs"], 2), round(wv["setupMs"], 2), "-"),
        ("pages", nv["pages"], wv["pages"], poolv("pages", "{:.0f}")),
        ("cold turn p50 / p99 (ms)",
         f'{nv["coldP50"]:.2f} / {nv["coldP99"]:.2f}',
         f'{wv["coldP50"]:.2f} / {wv["coldP99"]:.2f}',
         "-"),
        ("hot turn p50 / p99 (ms)",
         f'{nv["hotP50"]:.2f} / {nv["hotP99"]:.2f}',
         f'{wv["hotP50"]:.2f} / {wv["hotP99"]:.2f}',
         "-"),
        ("font size 18->24 (ms)", round(nv["reflowMs"], 2), round(wv["reflowMs"], 2), "-"),
        ("line height 1.65->1.98 (ms)", round(nv["repageMs"], 2), round(wv["repageMs"], 2), "-"),
        ("memory (MB)", f'wasm {nv["wasmMB"]} / heap {nv["heapMB"]}', f'heap {wv["heapMB"]}', "-"),
    ]
    if pool and "error" not in pool:
        rows.extend([
            ("chapters", "-", "-", poolv("chapters", "{:.0f}")),
            ("pool init (ms)", "-", "-", poolv("initMs")),
            ("pool wall (ms)", "-", "-", poolv("layoutMs")),
            ("pool pages match single", "-", "-", "yes" if pool["pagesMatch"] else "NO"),
            ("pool chapters match single", "-", "-", "yes" if pool["chapterPagesMatch"] else "NO"),
            ("pool speedup vs single", "-", "-", f'{pool["speedup"]:.2f}x' if pool.get("speedup") else "-"),
        ])

    print()
    print(f'A/B - {merged["label"]}  corpus={merged["meta"]["corpusChars"]} chars, '
          f'runs={merged["runs"]}, workers={workers or "-"}')
    header = f'{"metric":32} {"native":>22} {"webview":>22} {wlabel:>12} {"native/webview":>14}'
    print(header)
    print("-" * len(header))
    for name, n, w, p in rows:
        ratio = ""
        if isinstance(n, (int, float)) and isinstance(w, (int, float)) and w:
            ratio = f"{n / w:.2f}x"
        print(f"{name:32} {str(n):>22} {str(w):>22} {str(p):>12} {ratio:>14}")
    note = coverage_note(merged)
    if note:
        print(f"note: {note}")
    if pool and "error" in pool:
        print(f"pool failed: {pool['error']} (fell back to the single-instance numbers)")
    print()
    print(f"written {out_path}")


def print_summary(groups):
    if len(groups) < 2:
        return
    print()
    print("worker pool summary")
    header = (f'{"workers":>8} {"chapters":>9} {"pages":>8} {"pool init":>11} '
              f'{"pool wall":>11} {"single":>9} {"wall/single":>12} {"pages ok":>9} {"module":>8}')
    print(header)
    print("-" * len(header))
    for g in groups:
        p = g.get("pool")
        if not p:
            print(f'{g.get("workers", 0):>8}          -        -           -           -'
                  f'         -            -         -        -')
            continue
        if "error" in p:
            print(f'{g["workers"]:>8}          -        -           -           -'
                  f'         -            -        NO   error')
            continue
        ratio = p.get("wallRatio")
        print(f'{g["workers"]:>8} {str(p.get("chapters")):>9} {p["pages"]:>8.0f} '
              f'{p["initMs"]:>10.1f} {p["layoutMs"]:>10.1f} {p["bookMs"]:>9.1f} '
              f'{(f"{ratio:.2f}x" if ratio is not None else "-"):>12} '
              f'{"yes" if p["pagesMatch"] and p["chapterPagesMatch"] else "NO":>9} '
              f'{"shared" if p["moduleShared"] else "own":>8}')


def main():
    label, out_path, *paths = sys.argv[1:]
    runs = [json.load(open(p)) for p in paths]
    bad = next((r for r in runs if "error" in r), None)
    if bad:
        print(json.dumps(bad, indent=2))
        return 1

    grouped = group_runs(runs)
    merged = [merge(grouped[w], label, w) for w in sorted(grouped)]

    if len(merged) == 1:
        out = merged[0]
    else:
        out = {
            "label": label,
            "params": runs[0]["params"],
            "runs": len(runs),
            "groups": merged,
        }
    json.dump(out, open(out_path, "w"), indent=2, ensure_ascii=False)

    for group in merged:
        print_table(group, out_path)
    print_summary(merged)
    return 0


if __name__ == "__main__":
    sys.exit(main())
