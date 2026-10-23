# Web 并行 + 单实例性能 计划

> 状态：待实施
> 关联：`Layx.md` §7（性能设计）、§9（A/B）、§12（里程碑 M2/M3）

## 0. 背景

A/B 第一批数据（20 万字，同字体/同 viewport，3 轮中位）：

| 指标 | native (wasm×1) | webview (Blink) | 比值 |
|---|---:|---:|---:|
| 首页到可交互 | 436 ms | 127 ms | 3.4× |
| 整书布局 | 242 ms | 56 ms | 4.3× |
| 冷翻页 p50/p99 | 4.4 / 9.5 ms | 0.0 / 0.1 ms | — |
| 字号 18→24 | 214 ms | 47 ms | 4.6× |
| 行高重分页 | 186 ms | 19 ms | 9.7× |

同机 native Go（500 万字，只布局）：

| 配置 | 时间 |
|---|---:|
| 1 worker | 1.45 s |
| 8 workers | 0.30 s |

结论：**单个 Go wasm 实例内部 `GOMAXPROCS=1`，拿不到多核；但浏览器可以用 Web Worker
拉起多个 wasm 实例，章节级并行。** 因此 Web 端并行不是“能不能”，而是“怎么编排”。

目标：

1. Web 端支持并行：默认 1 个 worker，整书/批量场景用 worker 池。
2. 提升单个 wasm 实例的性能（worker 池会同比受益）。
3. A/B 增加 `wasm×N` 与 `native binary` 列。

---

## 1. Worker 池

### 1.1 分层

```text
core (render)            不改，保持纯 Go、无浏览器概念
web adapter              examples/wasm/web/
  layout-worker.js       worker 入口：wasm 实例 + initLayout / layoutChapter
  layout-pool.js         主线程：编译一次、拉起 N 个 worker、分片、收集
demo / ab.js             默认单实例；batch 场景切到 pool
```

### 1.2 wasm 新增导出（layout-only，不依赖 canvas）

| 导出 | 签名 | 作用 |
|---|---|---|
| `initLayout` | `(fontBytes)` | 只建 `FontSet`，不需要 canvas/overlay |
| `layoutChapter` | `(text, w, h, size, lh, justify)` | 排一章，返回 `{pages, ms}` |

`boot()` 继续服务带绘制的 demo；worker 里不画图，不碰 canvas。

### 1.3 worker 协议

```text
main → worker   {type:"init", module, fontBytes}
worker → main   {type:"ready", faces, upem}

main → worker   {type:"layout", id, text, w, h, size, lh, justify}
worker → main   {type:"result", id, pages, ms}
worker → main   {type:"error",  id, error}
```

- **模块只编译一次**：主线程 `WebAssembly.compile()`，把 `WebAssembly.Module`
  结构化克隆给 worker；不支持时回退为每个 worker 自己编译。
- **字体**：每个 worker 必须自己 `CopyBytesToGo` + 解析（Go 堆不能共享），
  这是池的固定成本。
- **结果**：`{pages, ms}` 用 JSON 足够；将来传 DisplayList 时改为
  `ArrayBuffer` + Transferable，零拷贝。
- **确定性**：章节彼此独立；主线程按章节顺序累加页数，锚点不受影响。

### 1.4 调度与生命周期

- 默认静态 round-robin 分片；后续可换任务队列，减少长章拖尾。
- 取消：整章粒度，`terminate()` 后重建 worker；不做章内部分回滚。
- 失败：章节回落到主线程重排一次，仍失败则整次布局失败。
- 池常驻但可回收空闲 worker；大小可配。

### 1.5 默认策略

| 场景 | worker 数 |
|---|---|
| 首屏 / 单章重排 / 翻页 | 1（不建池） |
| 导入整本、改字号全书重排 | 桌面 4，移动 2 |
| 后台预取相邻章节 | 池中空闲的 1–2 个 |

### 1.6 验收

- 20 万字整书：`wasm×4` 墙钟 ≤ 单实例的 40%，`wasm×8` ≤ 30%。
- 池初始化（编译 + N×字体解析）单独计时，不混入布局时间。
- 池与单实例的页数完全一致。

### 1.7 风险

- 每个 worker 约 30–80 MB 常驻（运行时 + 字体表）；iOS 限制更紧，必须可降级到 1。
- 池初始化可能 1 s+（N 次字体解析），只适合导入/重排，不适合首屏。
- 旧浏览器可能不支持克隆 `WebAssembly.Module`，需要回退路径。

---

## 2. 单实例性能

### 2.1 先 profile，不改代码

```sh
go test ./render -run '^$' -bench BenchmarkLayout5M -benchtime 1x \
  -cpuprofile /tmp/cpu.out -memprofile /tmp/mem.out
go tool pprof -top /tmp/cpu.out
go tool pprof -top -alloc_space /tmp/mem.out
```

native 与 wasm 热路径一致，先看 native。重点：shaping、`wrapParagraph`、
`buildLine`、`justifyWeights`、分配点。

### 2.2 候选优化（按预期收益）

**1. 消除双重 shaping（最大嫌疑）**

现状：`wrapParagraph` 为断行 shaping 整段；`buildLine` 在显示某页时对每行
重新 `shapeRange`。

- A. 给“最近构建的段落”加小 LRU，缓存 shaped runs 供 `buildLine` 复用。
- B. 布局时顺手建好前 N 页（首屏）的 DisplayList，趁 shaped 数据还热。
- C. 章内保留 shaped 输出，直到该章所有页构建完（内存换时间，需评估）。

先做 A，改动小、可回退。

**2. 分配池化（M1 承诺项）**

- 复用 `[]shapedRun`、`[]paint.GlyphRun`、`[]paint.Glyph`、`[]float32`
  （justify 权重）、`[]glyphPos`。
- 5M 字当前 ~950 MB 总分配，目标 ≤600 MB。
- 池必须 per-instance（worker 场景下不能全局共享）。

**3. 字体查找**

- `workerFontmap.ResolveFace` 对每个 rune 调 `NominalGlyph`；加有界
  rune→face 缓存（如 4k 项），中文书命中率高。
- `HarfbuzzShaper.SetFontCacheSize(8)` 可按字体数调大。

**4. 小热点**

- `justifyWeights` 每行分配切片 → 复用缓冲区。
- `addAutospacing` 单独一遍遍历 → 折进 `shapeRange` 收尾循环。
- wasm 批量布局时临时 `debug.SetGCPercent(200)`，结束后恢复（仅 batch 模式）。

**5. 明确不做**

- 不用 `ShapeNoExtents` 换性能（线宽 bug 根源）。
- 不在单章内部再切线程；native 侧已有顶层 block 并发。

### 2.3 验收

| 指标 | 现在 | 目标 |
|---|---:|---:|
| wasm×1 整书 20 万字 | 242 ms | ≤150 ms |
| 冷翻页 p50 | 4.4 ms | ≤3 ms |
| 5M 字总分配 | ~950 MB | ≤600 MB |
| native 5M @8 workers | 0.30 s | ≤0.22 s |

---

## 3. A/B 扩展

- 新增 `examples/bench` CLI（`-chars -workers -json`），复用同一语料与参数，
  作为 `native binary` 列。
- `scripts/ab.sh --workers 1,4,8`：每个配置跑 `repeat` 次取中位。
- `scripts/ab_report.py` 输出四列：`webview | wasm×1 | wasm×N | native`，
  并单列“池初始化”和“池墙钟”。

---

## 4. 执行顺序

1. Worker 池（wasm 导出 + worker + pool + `ab.sh --workers`）。
2. profile + 单实例优化（双重 shaping、池化、字体缓存）。
3. A/B 四列，重跑 20 万字与 5M 字，对照 `Layx.md` §9.4 门槛。

---

## 5. 待定

1. **池大小策略**：固定 4？还是 `min(hardwareConcurrency-1, 8)` 再按内存降级？
2. **章节分片接口**：宿主按章节切好传入，还是 web adapter 自己切？
   后者需要约定章节边界与页号偏移规则。
