# 实现语言：Go 与 Rust 的实测对照

结论先说：**产物大小和内存 Rust 明显赢，wasm 性能 Rust 赢一档，原生性能两者差不多。**
所以目前不移植；等浏览器端成为一等目标时再回来。下面是可以复查的数据和不能忽略的口径。

## 一句话结论

| 轴 | Go | Rust | 差距 | 值不值得重写 |
|---|---|---|---:|---|
| wasm 产物（等价 shaping-only 程序） | 6.4 MB | **0.75 MB** | 8.5× | 是 |
| wasm 峰值内存（同语料同字体） | 112.5 MB | **5.6 MB** | 20×* | 是 |
| wasm 速度（shaping 20 万字） | 55.6 ms | **22.6 ms** | 2.5× | 视场景 |
| 原生速度（shaping 20 万字） | 17.7 ms | **13.3 ms** | 1.33× | 否 |

\* 两边的数字都含一份 5 MB 的字体副本。Go 那 112 MB 里很大一部分是 GC 余量而不是活对象，
所以「20×」是**足迹差**，不是「活对象差」；这条下面单独解释。

## 怎么测的

同一份语料（`tools/bench_native` 的中文正文，≈20 万字、2273 段）、同一个字体
（Droid Sans Fallback Full）、同一个 Node 24.21.0 进程内跑 wasm。

- Go：`go-text/typesetting` v0.3.5，`HarfbuzzShaper.ShapeNoExtents`（比生产路径的
  `Shape` 更快，也就是对 Go 有利的取值），每段一个 `shaping.Input`。
- Rust：`rustybuzz` 0.20.1 + `ttf-parser` 0.25.1，每段一个 `UnicodeBuffer`，
  `opt-level=3`、`lto`、`codegen-units=1`、`panic=abort`。
- 只测 shaping：断行、分页、DisplayList、绘制都在 shaping 之外，而 shaping 是我们
  profile 里的最大单块（31%）。两边的速度**比值**可以外推，绝对值不等于整引擎耗时。

Go 侧两种取值的完整数据，供校准：

| | shaping 20 万字 |
|---|---:|
| Go 原生，`Shape`（带 extents） | 20.5 ms |
| Go 原生，`ShapeNoExtents` | 17.7 ms |
| Go + wasm，`ShapeNoExtents` | 55.6 ms |
| Rust 原生 | 13.3 ms |
| Rust + wasm | 22.6 ms |

## 为什么原生只差 1.33×

因为算法同构。`rustybuzz` 是 HarfBuzz 的移植，`go-text/typesetting` 也是 HarfBuzz 的
移植，两边跑的是同一套 shaping pipeline（script itemization → lookup 应用 → positioning）。
换语言只换掉边界检查、内联和分配策略的差价，换不掉 pipeline 本身。

这也意味着：**语言不是那个 10× 杠杆。** 我们的 55 ms 里 83% 在 `go-text/typesetting`
里面（shaping 31%、`LineWrapper` 33%、UAX#14 与 bidi 19%），其中大半是「通用 shaping +
通用断行」对 CJK 做的无用功。这笔钱在 Rust 里一样要付，除非换算法；换算法是
README「性能」一节的活，与语言无关。

## 为什么 wasm 的差距是结构性的

Go 的 `GOOS=js` 目标没有线程：`GOMAXPROCS=1`，没有 shared memory 方案，所以浏览器里
拿不到多核。运行时本身也贵——同一个 shaping 负载，Go wasm 是原生的 3.4×，Rust wasm
只有 1.8×。

内存那一项不是 GC 调参能救的，实测过：

| Go wasm 的 `SetGCPercent` | 耗时 | 峰值内存 |
|---|---:|---:|
| 100（默认） | 58.3 ms | 112.5 MB |
| 30 | 86.4 ms | 168.5 MB |
| -1（关闭） | 84.4 ms | 381.5 MB |

默认值已经是最优点，收紧或关闭都更慢——是分配器与内存增长方式的问题，不是阈值问题。

## 如果移植，代价在哪

1. 10,683 行非测试 Go（`render` + `render/paint` + `render/hyphen/en`）要重写，13 个
   测试文件、117 个测试要重建。
2. **断行结果会变**，这是最花时间的部分。`scripts/linecmp.sh` 现在是逐行和 Chromium
   比对（纯中文 54/54、英文 42/42、日英 63/63、对话密集 70/70），它钉住的是
   `go-text` 的 UAX#14 行为 + 我们自己的标点挤压/自动间距规则。换成
   `unicode-linebreak` 之后这套 fixture 要重跑重调；`rustybuzz` 对 CJK 的 `palt`/`vert`
   支持也不如 HarfBuzz 完整。
3. 跨语言边界：`Painter` 是回调接口，跨 FFI 会撞上 per-call 开销。好的一面是
   `DisplayList` 已经是扁平数据结构（`docs/design.md`），这条边界可以做成数据拷贝而不是
   逐字形回调——这是唯一让移植不至于灾难的设计，也是当初把绘制接口定成 DisplayList 的
   额外收益。
4. 双实现维护。真移植就该整体切换，不要 Go 原生 + Rust wasm 各一份。

## 决定

**不移植。** 按收益/成本排序：

1. 先做语言无关的算法优化（自有断行循环、CJK 简单 run 快路径、字形 arena），
   在 Go 里就能拿到 5–7×，且输出可做到逐字节不变。
2. 如果做完了、浏览器端仍然是主战场，再带着一个已经很瘦的设计移植。那时候 Rust 的
   收益是纯运行时收益，移植面也更小。
3. 反过来先移植，等于把同样的浪费搬过去，然后在 Rust 里把同一份优化再做一遍。

## 没有测的

- 端到端布局（断行 + 分页 + DisplayList）的 Rust 数字：只测了 shaping。比值可用，
  绝对值不能直接引用。
- Rust 原生内存：没测。预期比 Go 低 1.5–2×（Go 的 GC 余量），但做了字形 arena 之后
  Go 这一侧的差距会缩小。
- Rust wasm 多线程（SharedArrayBuffer + `wasm-bindgen-rayon`）：需要宿主给 COOP/COEP
  响应头，属于部署决策。真要走浏览器路线，这是让「对 Chrome 10×」成立的最后一环。

## 复现

上面所有数字来自 `tools/bench_native` 同一份语料的两个一次性 spike（Go/wasm 与
Rust/wasm 各一个 `main`，35–90 行，未入库）。复现要素：

```sh
# 语料：三段落循环到 20 万字，按 \n 分段
# 字体：/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf
# Go：go-text/typesetting v0.3.5, ShapeNoExtents, 18px, Droid face
# Rust：rustybuzz 0.20.1, UnicodeBuffer::push_str, Direction::LeftToRight
# 计时：node 内 WebAssembly.instantiate 后连跑 5 次取中位数（首次为 warmup）
```
