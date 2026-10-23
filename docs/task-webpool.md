# 任务 B：Web Worker 池 —— 浏览器端章节级并行

> 分支 `webpool`，工作树 `/home/laix/layx-webpool`
> 参照 `docs/plan-web-parallel.md` §1。

## 背景

单个 Go wasm 实例内部 `GOMAXPROCS=1`，拿不到多核；但浏览器可以起多个
Web Worker，每个 worker 跑一个独立 wasm 实例，章节之间互不依赖，可以并行。
A/B 实测（20 万字，3 轮中位）：整书布局 wasm 242ms vs Blink 56ms。

## 目标

1. wasm 增加两个 **不依赖 canvas** 的导出：
   - `initLayout(fontBytes)` —— 只建 `FontSet`
   - `layoutChapter(text, w, h, size, lh, justify)` —— 排一章，返回 `{pages, ms}`
2. `examples/wasm/web/layout-worker.js`：worker 入口，收 `WebAssembly.Module`
   并用 `new WebAssembly.Instance` 复用编译结果。
3. `examples/wasm/web/layout-pool.js`：主线程编排 —— 编译一次、拉起 N 个
   worker、章节 round-robin 分片、按章序累加页数、错误回落主线程。
4. `scripts/ab.sh --workers 1,4,8`：每组配置单独跑，结果进同一份 JSON。
5. `scripts/ab_report.py` 增加 `wasm×N` 列，并单列“池初始化”与“池墙钟”。

## 允许改的文件

- `examples/wasm/bench.go`
- `examples/wasm/web/layout-worker.js`（新建）
- `examples/wasm/web/layout-pool.js`（新建）
- `examples/wasm/web/ab.js`、`examples/wasm/web/ab.html`
- `scripts/ab.sh`、`scripts/ab_report.py`
- 新增 `examples/wasm/web/pool_test.html` 或 node 侧自检脚本

## 禁止改的文件

`render/**`（一行都不许动）、`examples/wasm/main.go` 的既有导出、
`examples/wasm/painter.go`、`examples/wasm/server/main.go` 的既有路由
（如需新路由先说明）。

## 验收

1. `node --check` 通过所有新 JS。
2. `./examples/wasm/build.sh` 成功，wasm 体积不超过 7.2 MB。
3. 新增自检：同一章分别用 `layoutChapter` 与整书单实例布局，**页数一致**。
4. 池与单实例对同一本书的**总页数完全一致**（确定性，允许 ±0）。
5. `./scripts/ab.sh smoke --chars 50000 --repeat 1 --workers 1,4` 跑通，
   输出包含两组数字；`wasm×4` 墙钟不高于单实例的 60%。
6. `git status` 干净，提交到 `webpool` 分支，**不要 push**。

## 注意

- 每个 worker 必须自己 `js.CopyBytesToGo` + 解析字体（Go 堆不能共享），
  池初始化要单独计时，不能混进布局时间。
- `WebAssembly.Module` 结构化克隆可能不被支持，必须有“每 worker 自己编译”
  的回退路径，并在页面里打一行提示。
- worker 里不碰 canvas / overlay。
- 移动端 worker 数要能降到 1；不要写死 8。
- 现有 A/B 的单实例路径必须保持可用，`--workers` 缺省行为不变。
