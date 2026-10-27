# layx

[![CI](https://github.com/Laic7092/layx/actions/workflows/ci.yml/badge.svg)](https://github.com/Laic7092/layx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/laix/layx/render.svg)](https://pkg.go.dev/github.com/laix/layx/render)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**中文** · [English](README.en.md)

纯 Go 的 reflowable 排版内核，输出显示列表。输入是宿主已经解析、级联好的 `render.Document`，
输出是分页结果、`DisplayList` 和命中/选区/锚点映射。**不解析 EPUB/XHTML/CSS，不内置光栅器。**

```text
宿主/编译器                         本库
EPUB / OPF / spine / CSS      →     render.Document
XHTML / 选择器 / 级联                ↓
                                     shaping / 断行 / 块布局 / 分页
                                     ↓
                                     Page + DisplayList + HitTest/Selection
                                     ↓
宿主 Painter（GPU / 软件 / e-ink）
```

已实现：分页与滚动两条输出路径、三层布局缓存、shaped 行缓存、tile 缓存、
命中测试/跨页选区/锚点映射、CJK 断行与标点政策、可插拔断字、边框/背景/文字装饰。
布局常驻内存约 **4 B/字**（整本书，不含文档与字体）。
设计取舍见 [`docs/design.md`](docs/design.md)；标点/断行政策、默认值的规范出处、
中文正文的推荐开关组见 [`docs/text-rules.md`](docs/text-rules.md)。

## 安装

两个 module 独立发布，按需 `go get`：

```sh
go get github.com/laix/layx/render@v0.1.0    # 排版内核（必需）
go get github.com/laix/layx/epub@v0.1.0      # EPUB/XHTML/CSS 解析层（可选）
```

`epub` 是 nested module，tag 形如 `epub/v0.x.y`；它 `require` 根 module 的**已发布版本**，
没有 `replace`，外部消费者可以直接 `go get`。两个 module 一起打版本，升级时一起升。

最低 Go 版本：根 module **Go 1.21**（`render` 实际用到的语法/API 的下限），`epub`
module **Go 1.26**（`golang.org/x/net` 的要求）。仓库提交了 `go.work` 列出两个 module，
克隆后一条 Go 1.26+ 工具链即可开发：

```sh
go test ./...              # 根 module（workspace 模式）
(cd epub && go test ./...) # epub module
```

外部消费者看不到 `go.work`，解析的是上面两个已发布的 module 版本。

## 包结构

```text
github.com/laix/layx/render            核心：IR、字体、布局、分页、命中、选区
github.com/laix/layx/render/paint      绘制接口与基础类型（无光栅器）
github.com/laix/layx/render/hyphen/en  可选：TeX 英文断字模式，核心不 import 它
github.com/laix/layx/epub              EPUB/XHTML/CSS → render.Document（nested module）
examples/layout                        最小示例：排版并打印页数
examples/wasm                          wasm + Canvas 2D 原型（含 bench 路径）
tools/{bench_native,memreport,snapshotimg,genepub}  开发工具，不属于库
```

API 参考见 [`docs/api.md`](docs/api.md)：越界与空结果的统一语义、`IssueCode` 表、
`Limits` 默认值、错误分类、`FontSet` 覆盖查询与 `LayoutRange` 语义。

`epub/` 有自己的 `go.mod` 和 [`epub/README.md`](epub/README.md)，不在根 `go test ./...` 里。
直接依赖只有 `go-text/typesetting`（shaping / UAX #14 断行）与 `golang.org/x/image/math/fixed`；
`go list -deps ./render` 里的 `image/*`、`encoding/xml` 是 `go-text/typesetting/font` 带进来的
传递依赖。`render/deps_test.go` 对**直接 import** 做了闸门。

## 快速开始

```go
eng, _ := render.New(render.Options{Workers: 0}) // 0 = GOMAXPROCS；整个 Engine 的并发预算

fonts, _ := render.NewFontSet(render.FontFace{
    Family: "serif",
    Weight: 400,
    Data:   serifTTF,
})

layout, err := eng.Layout(ctx, doc, render.LayoutOptions{
    Viewport: render.Viewport{Width: 600, Height: 900},
    Typography: render.Typography{
        FontFamily: "serif",
        FontSize:   18,
        LineHeight: 1.6,
        Color:      paint.Color{A: 0xff},
        Rules: render.TextRules{
            PunctuationCompression: true,
            Widows:                 2,
            Orphans:                2,
        },
    },
    Fonts:   fonts,
    Margins: render.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
})
page := layout.Page(0) // 首次访问构建 DisplayList，之后按 PageCache 缓存
page.Draw(painter)     // 只回放，不重排、不 shaping 全书
```

`Document` 用 `DocumentBuilder` 构建：

```go
b := render.NewDocumentBuilder().
    SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}}).
    SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})

err := b.AddChapter(render.Chapter{
    ID:   "ch1",
    Root: 0,
    Nodes: []render.Node{
        {Kind: render.NodeBlock, Children: []render.NodeID{1}},
        {Kind: render.NodeText, Style: 1, Text: "正文……"},
    },
    Anchors: map[render.AnchorID]render.NodeID{100: 1},
})
doc, err := b.Build()
```

引擎不自作主张：标点挤压、行末悬挂、孤行/寡行保护、中西文间距都是**政策**，零值一律是
规范初始值——标点类政策因此默认关闭，孤行/寡行按 CSS 初始值 2/2 打开。中文正文的建议
开关组见 [`docs/text-rules.md`](docs/text-rules.md#中文正文的推荐开关组)。

## 两种输出

`LayoutOptions.Mode` 选择输出方式，断行与块布局完全共享，只有"取窗口"不同。

分页（`ModePaged`，默认）：

```go
layout.PageCount()
page := layout.Page(i) // 越界为 nil；首次构建，之后按 Options.PageCache（默认 4）做 LRU
page.Draw(painter)
page.HitTest(pt)
page.Links()
```

滚动（`ModeScroll`）：

```go
render.LayoutOptions{Mode: render.ModeScroll, TileHeight: 1024} // 0 = Viewport.Height
layout.ContentHeight()
layout.TileCount()
tile := layout.Tile(i) // 越界为 nil；首次构建，之后按 Options.TileCache（默认 8）做 LRU
layout.Tiles(lo, hi)   // 并行构建一段 tile（tile 之间独立，用满并发预算）
layout.Prefetch(ctx, lo, hi)                     // 分页=页、滚动=tile，同步并行
pf := layout.StartPrefetch(ctx, lo, hi)          // 同上，但跑在显式句柄的 goroutine 上
err := pf.Wait()
```

**tile 是 DisplayList，不是位图**：`render` 没有光栅器，是否缓存成图片由宿主决定。

内容坐标：滚动模式下 `y = 0` 是整本书正文顶端，章与章连续堆叠，`Margins.Top/Bottom`
只是视口 inset；分页模式下页首尾相接成一条虚拟流。每行只属于一个 tile（按行盒顶边
分配），图片可跨 tile，由 Painter 裁剪。

宿主画家可实现可选的 `paint.Clipped`（`Visible() paint.Rect`）：`Page.Draw` / `Tile.Draw`
会跳过与可见区不相交的 op，字形 op 各自带行盒矩形。不实现该接口的画家仍然收到全部
op，回放语义不变。

## 并发与预算

`Options.Workers`（默认 `GOMAXPROCS`）是**整个 Engine 的并发预算**，不是每次调用的上限：

- 并发的多个 `Layout` / `Relayout` / `Prefetch` 共用这一份预算，按活跃 phase 数平分。
  一个先开始的长布局不会占满机器：多出来的 helper 在完成当前任务（几十微秒）后交还
  预算，后到的调用随即接手。实测「前台小布局 + 后台整本重排」从 4.2 ms 降到 1.6–2.1 ms
  （单独跑 1.27 ms）。
- 任何 phase 的调用方永远参与计算；helper 抢预算不做阻塞等待，所以**不会死锁**，嵌套
  的 phase（预取里的单页）拿不到预算时就退化成在调用方顺序执行。
- 除了显式 `StartPrefetch` 返回的那个 goroutine，库不起后台 goroutine：所有 helper 在
  phase 返回前 join。`Engine.Close()` 只是丢掉复用的 worker（字体 face 缓存、shaper、
  scratch），可以不调；调了之后 Engine 仍可继续用。

**断行任务的粒度是段落，不是块**。`Layout` 的断行分三段：先按章并行规划（样式继承、
inline 分词、建块树，不做 shaping），再把全书段落汇成一个扁平任务列表让 worker 抢，
最后顺序把行盒贴回块树。所以"整章正文包在一个 `<div>` 里"这种书也能跑满所有核，
不会出现「一个块拖住其余 worker」的尖峰。

`Options{Measure: true}` 时 `Stats` 还给出 `Tasks`（提交给并行 phase 的任务数）、
`Busy`（每个 worker slot 的忙时）与 `Imbalance`（(最忙-最闲)/最忙）。后者是验收负载
是否均衡的指标：段落粒度够细时它接近 0。

## 位置与坐标

- `Location{Chapter, Node, Anchor, Offset}`：`Node`/`Offset` 用于当前 layout 内的命中、
  选区、搜索；`Anchor` 是宿主可持久化的节点级锚点（无锚点时为 0）。**持久化存
  `Anchor` + `Offset`**：`Anchor` 非零优先于 `Node`，所以换书重建 `Document` 后旧 node
  号不会盖掉稳定锚点。锚点按块/段/标题铺，成本 O(#anchors)。
- `Locate(anchor)` → `Location`（只到节点，`Offset` 为 0），`PageOf(loc)` ↔
  `LocationOfPage(page)`；`PageRange(chapter)` → `[first, first+count)`，
  `ChapterPages()` 给出每章首页。滚动模式没有页，后用 `ChapterContentTop(chapter)`。
- 命中与选区：`Page.HitTest(pt)` 收**页内坐标**（含页边距），`Layout.HitTest(pt)` 在分页与
  滚动模式下都收**内容坐标**（x 不含 `Margins.Left`，y 是全书正文坐标）；两者都返回同一套
  `Location`。`Layout.Selection(from, to)` 返回矩形；没有可命中的内容（空章节/空布局）时
  返回 `(Location{}, false)`，不 panic。`SelectionQuad` 带两套坐标：
  `Rect` 是页内坐标（仅分页模式），`Content` 是内容坐标（两种模式都有；滚动模式减 `Tile.Top()`
  得 tile 内坐标）。无法解析的 `Location` 返回空切片。
- `LocationAt(y)` ↔ `OffsetOf(loc)` 在两种模式下共用一套内容坐标，互为逆运算。
  `Progress(loc)` ∈ [0,1]：分页按「页 + 页内偏移」，滚动按 `y / ContentHeight`；两者
  分母不同，同一位置的百分比会有小差异，**不要持久化百分比**。
- `Document.Text(chapter)` 按阅读序拼接该章文本，并返回每个节点的 rune 区间（非文本
  节点为零宽 run），搜索和复制不必自己遍历 `NodeText`；它不插段落分隔符。
- `Layout.Document()` 的指针身份可判断索引/图片缓存是否过期；`Margins()` /
  `PageContentHeight()` 让宿主不必反推页边距和页内容高。

## 越界与空结果

统一原则：**查不到就返回 `(T, false)` 或 `nil`，不 panic、不返回半成品。** 返回 `bool` 的
调用失败时返回值类型零值；返回指针的返回 `nil`；返回切片的返回 `nil` 或长度为 0 的切片
（都能直接 `range`）。完整表见 [`docs/api.md`](docs/api.md)。

| 调用 | 语义 |
|---|---|
| `Document.Chapter(i)` | `(Chapter, bool)`：越界 `false` |
| `Document.Text(chapter)` | `(string, []TextRun, bool)`：越界 `false` |
| `Layout.Page(n)` | 越界 / 分页模式不可用时 `nil`（滚动模式恒 `nil`） |
| `Layout.Tile(i)` | 越界 / 分页模式时 `nil` |
| `Layout.Tiles(lo, hi)` | 区间夹到 `[0, TileCount)`；空区间返回 `nil` |
| `Layout.Locate(anchor)` | 未知锚点 `(Location{}, false)` |
| `Layout.PageOf(loc)` / `LocationOfPage(page)` | 解析不到 `(0, false)` / `(Location{}, false)` |
| `Layout.LocationAt(y)` / `OffsetOf(loc)` / `Progress(loc)` | 解析不到 `(0, false)` |
| `Layout.PageRange(chapter)` / `ChapterContentTop(chapter)` | 越界 `(0, 0, false)` 或 `(0, false)` |
| `Layout.ChapterPages()` | 每**文档章**一项：该章首页；不在本次布局内（按章排版范围外）为 `-1`；滚动模式为 `nil` |
| `Layout.HitTest(pt)` / `Page.HitTest(pt)` | 没有可命中的内容（空章节/空布局）时 `(Location{}, false)` |
| `Layout.Selection(from, to)` | 任一端无法解析：`nil`（空选区） |
| `Layout.Issues()` | 无诊断时长度为 0 的切片（可安全 `range`；`nil` 与空切片等价） |
| `Layout.Document()` | 总是非 `nil`；`PageCount` / `TileCount` / `ContentHeight` 总是有值（另一模式为 0） |
| `Book.ResolveLink(url)` | 外部 URL / 断链 `(LinkTarget{}, false)` |

`Page(n)` / `Tile(i)` 返回 `nil` 是唯一需要"先判空再解引用"的约定，其余查询都用 `bool`。

## 诊断与错误

库不再静默降级：**它决定丢弃或替换的东西都会报告出来**，诊断不改变布局结果。

```go
eng, _ := render.New(render.Options{
    // 在 Layout 返回前、所有 worker 结束后，于调用方 goroutine 上逐条回调。
    OnIssue: func(is render.Issue) { log.Printf("layout: %s", is) },
})
layout, err := eng.Layout(ctx, doc, opts)
if err != nil {
    return err
}
for _, is := range layout.Issues() { // 事后取：同一次调用的完整列表（副本）
    log.Printf("%s chapter=%q node=%d %s", is.Code, is.Chapter, is.Node, is.Detail)
}
```

| `IssueCode` | 含义 |
|---|---|
| `IssueMissingFont` | 请求的 `FontFamily` 在 `FontSet` 里没有 face，文字用回退 face 排了 |
| `IssueMissingGlyphs` | 配置的字体都画不出这些 rune（tofu）；`Detail` 最多列 8 个码点加数量 |
| `IssueDroppedContent` | IR 表达不了或缺数据而丢弃的内容（如没有字节的图片）；`epub` 层的表格线性化、丢图、ruby 也用它 |
| `IssueSuppressed` | 列表被截断时的汇总项，`Detail` 是未保留的条数（单次上限 1024 条） |

`epub.Options.OnIssue func(render.Issue)` 与 `Book.Issues()` 复用同一套 `IssueCode` 和投递语义，
报告 EPUB → IR 转换里被线性化/丢弃/忽略的内容（表格、图片、ruby、`@font-face` 等）。
宿主也可以先查字体覆盖，把 tofu 挡在排版之前：

```go
fonts.HasFamily("serif")                         // 字体族是否存在
fonts.Coverage('好')                              // 是否有任一 face 能画出它
face, ok := fonts.Resolve(render.Style{FontFamily: "serif"}, '好') // 排版实际会选的 face
```

**错误**：每个错误都 wrap 六个哨兵之一——`ErrInvalidDocument`、`ErrInvalidOptions`、
`ErrNoFonts`、`ErrLimitExceeded`、`ErrClosed`、`ErrInternal`——所以 `errors.Is` 可用；
需要定位时用 `errors.As` 取 `*render.DocumentError`：

```go
var de *render.DocumentError
if errors.As(err, &de) {
    log.Printf("kind=%v chapter=%q node=%d style=%d detail=%s",
        de.Kind, de.Chapter, de.Node, de.Style, de.Detail)
}
```

- **panic 契约**：库保证**任何输入都以 `error` 结束，不让宿主进程崩溃**。worker 里的 panic 会被
  恢复成 wrap `ErrInternal` 的错误——它表示**库的 bug**，不是文档非法。`Page.Draw` /
  `Tile.Draw` 里宿主 `Painter` 的 panic 库不恢复：Painter 必须在自己的边界处理。
- **NaN/±Inf 拒绝**：`LayoutOptions` 的视口、边距、`TileHeight`、`Typography` 数值与
  `DocumentBuilder` 里每个 `Style` 的数值都检查有限性；非有限值返回 `ErrInvalidOptions`
  （样式在 `Build` 时报 `ErrInvalidDocument` 并带 style 上下文），不会流进几何或页数。

### 资源上限

`Limits` 是**单次布局的硬上限**：超限返回 `ErrLimitExceeded` 并带实测值与上限，而不是分配
到 OOM。零值等于默认值，`Layout`、`Relayout`、按章排版都生效，缓存命中也绕不过去。

| 字段 | 默认 | 限制对象 |
|---|---|---|
| `MaxNodes` | 1 048 576 | 单次布局遍历的 node 数 |
| `MaxDepth` | 256 | 章节树深度 |
| `MaxTextRunes` | 20 000 000 | 文本 rune 数 |
| `MaxImagePixels` | 67 108 864 | 图片像素总数 |
| `MaxPages` | 100 000 | 分页模式页数 |
| `MaxTiles` | 100 000 | 滚动模式 tile 数（内容高 / `TileHeight`） |
| `MaxLines` | 2 000 000 | 行盒总数 |

`MinTileHeight = 16`：非零 `TileHeight` 低于它返回 `ErrInvalidOptions`（否则多 1 像素内容就多
一个 tile）。确有大文档需求时宿主可以显式调高上限，但内存由宿主承担；超大工具书优先用按章排版。

### 按章排版

```go
layout, err := eng.LayoutRange(ctx, doc, opts, 10, 20) // 半开区间 [10, 20)
// 等价于 opts.Range = &render.ChapterRange{First: 10, Last: 20} 后调用 Layout
```

- 只排范围内的章；范围外不产生页/行，`Locate` / `PageOf` / `LocationOfPage` / `OffsetOf` /
  `LocationAt` 等永远解析不到范围外，返回 `false`；`ChapterPages()` 对范围外的章给 `-1`。
- 页号从范围内第 0 页开始；`Location.Chapter` 仍是**文档章号**，不是范围内偏移。
- 滚动模式内容坐标以范围内第一章顶端为 0；`Relayout` 保持同一范围；`Document()` 仍返回完整文档。
- 越界、`First >= Last`（空范围/反向范围）返回 `ErrInvalidOptions`；`Range == nil` 表示整本。

### 单次统计

`render.Options{Measure: true}` 下，`(*Layout).Stats()` 给出**这一次调用**的分阶段数据：
`Total`、`Keys`、`Break`、`Stack`、`Paginate`、`Assemble`、`Shapes`、`Tasks`、`Pages`、`Tiles`。
`Measure` 关闭时时长为 0，`Shapes`/`Tasks`/`Pages`/`Tiles` 等计数仍然准确（不读时钟）。
`Stack`/`Paginate` 的时长是跨 worker 段之和，并行时可能大于墙钟；相位归因用 `Total` 对齐。
`Engine.Stats()` 仍是整个 Engine 的累计值，两者互不污染。

## 重排与缓存

```text
BreakKey = hash(text, fonts, fontSize, letterSpacing, width, lang, align, indent, rules)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins)
PaintKey = hash(PageKey, theme)
```

`Layout.Relayout(ctx, opts)` 按这三层 key 复用阶段产物：`BreakKey` 命中不 shaping、
不断行；`PageKey` 命中复用行盒与页映射，只重算 y 和页边界；只影响绘制的选项
（颜色、装饰、背景）只进 `PaintKey`。每章保留最近 2 个变体，`breakUnit` 只存 rune
区间和行度量，不存 shaped glyph。`Options.LineCache`（默认 256）缓存未两端对齐的
shaped 行，命中不增加 `Engine.shapes`。

`Typography.Rules` 作为整体进 key，新增一个政策字段会自动参与哈希。换纸/换主题调用
`Layout.Relayout` 即可，不要重建 `Document`（那会丢掉三层缓存）。

### 分阶段计时

`render.Options{Measure: true}` 打开计时，`Engine.Stats()` 返回累计墙钟：`Total`、
`Keys`、`Break`、`Stack`、`Paginate`、`Assemble`，以及独立累计的 `Display`；`Measure`
关闭时布局路径不读时钟。`Tasks`、`Shapes`、`Pages`、`Tiles` 始终计数，`Busy` /
`Imbalance` 只在 `Measure` 打开时统计。`Engine.Stats()` 是 Engine 生命周期内的累计值，
适合做调试面板；要把一次慢布局归因到具体调用，用上一节的 `Layout.Stats()`。

## 性能

同语料 A/B（20 万字中文，640×900，18px/1.65，Droid Sans Fallback，`scripts/ab.sh`）：

- **单核与 Blink 同级**：59.1 vs 58.5 ms；8/16 worker 快 4.2×（14.1 / 13.7 ms）。
- **行高重排快 22×**：1.1 vs 24.3 ms。`LineHeight` 不在 `BreakKey` 里，一次 shaping 都不做。
- **分段不做 bidi**：纯 LTR 文本自己按 script+face 切分（`render/segment.go`），复杂脚本与
  双向控制符回落 go-text 的 `Segmenter`。中文段落上这一步从 39.7 B/rune 的分配降到 0，
  10 MB 文本每次布局少分配约 120 MB。回落路径现在先按 bidi 段落分隔符（LF/CR/U+2029 等）
  切分再逐段调用 `Segmenter.Split`，并保证 shaped runs 覆盖整段，所以**同一段文本里既有换行
  又有阿拉伯/希伯来/天城文/泰文时也能正常排版**（此前会 panic）。
- **布局内存 3.9 B/rune**（500 万字 = 18 MB，改动前 13.2 B/rune / 64 MB）。段落不保留
  `[]rune` 拷贝，行盒 20 B（`start`/`x`/`avail`/`last`/`align` 由样式推导），flow 只存
  每项 4 B 的 y 与 4 B 的 unit 下标，`flowItem` 按需物化。
- **wasm 单线程比原生慢 4.6×**（269.6 ms）：这是运行时不是算法，所以在浏览器里跑 layx
  目前比浏览器自己的排版慢；作为原生库嵌进阅读器才与 Blink 同级。

### 并发与负载（`go test ./render -bench`，8 核 / 16 线程）

冷布局的并行度（20 万字中文，`BenchmarkLayoutScale`；与改前逐点一致，±2% 噪声）：

| workers | 1 | 2 | 4 | 8 | 16 |
|---|---|---|---|---|---|
| 墙钟 | 44.9 ms | 23.7 ms | 13.3 ms | 8.9 ms | 7.8 ms |

它 95% 是 shaping 与 UAX14，8 个物理核上 8 worker 已经 5.1×、16 worker 5.8×，SMT 之后
就到顶了：**单次冷布局的并行上限是硬件，不是任务粒度**。500 万字 / 8 worker 与改前持平
（±2%），分配少 4%。

真正变快的是三处结构性的串行与争抢（同机 `-benchtime 20x` 多次，改前 → 改后）：

| 场景 | 改前 | 改后 |
|---|---|---|
| 重排（100 章 / 4000 段，只改段间距） | 1.16 ms | 0.31–0.38 ms |
| 16 页显示列表（冷，逐页同步建） | 2.60 ms | 2.39 ms |
| 同上，改用 `Prefetch` | — | 0.84 ms |
| 前台小布局 + 后台整本布局 | 4.2 ms | 1.6–2.1 ms |

数字由脚本现场测量，不要跨组比较：`scripts/ab.sh`（浏览器侧）、`tools/bench_native`
（原生侧，同一份字体字节）、`go test ./render -run '^$' -bench 'BenchmarkLayout5M|BenchmarkRelayout'`
（500 万字）。

内存用 `tools/memreport` 量，它按宿主的方式建文档、排版、画几页并改几次字号，打印存活堆、
向系统申请的堆、分配 churn 与峰值 RSS（任务管理器看到的那个数）：

```sh
go run ./tools/memreport -chars 3300000 -turns 5     # 10 MB 纯文本，改 5 次字号
go run ./tools/memreport -chars 3300000 -profile /tmp/alloc.pprof   # 分配归因
go run ./tools/memreport -chars 3300000 -live-profile /tmp/live.pprof   # 存活堆归因
```

10 MB 中文纯文本（330 万字 / 2 万段 / 100 章）的 A/B：

| | 改前 | 改后 |
|---|---|---|
| Layout 存活堆 | 80 MB | 52 MB |
| 单次 Layout 分配 churn | 577 MB | 404 MB |
| 峰值 RSS（一次布局） | 145 MB | 109 MB |
| 峰值 RSS（改 5 次字号后） | 375 MB | 232 MB |

## 验证

CI（[`.github/workflows/ci.yml`](.github/workflows/ci.yml)）在 Linux、macOS、Windows 上跑
`gofmt`、`go vet`、单测与 `-race`，并做交叉编译矩阵（`linux/386`、`linux/arm64`、
`windows/amd64`、`darwin/arm64`、`js/wasm`、`freebsd/amd64`）、覆盖率门、短时 fuzz
（`FuzzLayoutText`、`FuzzConvertChapter`、`FuzzParseCSS`）、benchmark 回归门，以及
`go-min` job（Go 1.21 + `GOWORK=off` 验证根 module 的最低版本承诺）。
本地跑同一套检查：

```sh
./scripts/check.sh          # gofmt / vet / 单测 / 交叉编译
./scripts/check.sh --fuzz   # 再加短时 fuzz 冒烟（FUZZTIME=15s）
```

单独跑：

```sh
go test ./... -count=1
go test ./render -race -count=1
go vet ./...
(cd epub && go test ./... -count=1)
go test ./render -run '^$' -fuzz FuzzLayoutText -fuzztime 30s
(cd epub && go test -run '^$' -fuzz FuzzConvertChapter -fuzztime 30s)
./scripts/benchgate.sh    # 与 scripts/bench_baseline.txt 比较；-update 重录
```

`render/mem_test.go` 是内存闸门：它断言 100 万字文档的布局不超过 6 B/rune（当前约 3.9），
并把实测值打进日志；没有 CJK 字体时跳过。

对照脚本需要 `curl` 和 Chrome/Chromium（或 `CHROME=/path/to/chrome`）：

- `./scripts/linecmp.sh {cjk,latin,mixed-ja,punct}`：同一份字体字节交给引擎与浏览器，
  逐**行**比对内容（native 侧导出行盒，浏览器侧用 `Range.getClientRects()`）。纯中文
  54/54、英文 42/42、日英混排 63/63、对话密集 + 挤压 70/70 行内容一致。
- `./scripts/ab.sh book --chars 200000 --turns 100 --repeat 3`：同一个 Chromium 里跑
  layx-wasm 与 DOM + CSS multi-column。
- `./scripts/snapshot.sh base` / `./scripts/snapshot.sh change --pages 1,2`：无头浏览器
  截图并按阶段做像素 diff，产物在 `snapshots/`（gitignored）。

## 已知限制

- 未实现：表格、ruby、竖排、脚注、flex/grid、RTL 视觉序、浮动环绕、`keep-with-next`。
  表格与脚注只需新样式和分页规则；flex/grid、竖排、ruby 需要新的布局模型。
- 未实现项不再"静默"：`epub` 层会以 `IssueDroppedContent` 报告被线性化的表格、被丢的
  图片/SVG、被忽略的 ruby 与 CSS at-rule；`render` 层报告自己丢弃的内容（如缺字节的图片）。
  IR 表达不了的能力（表格、flex/grid、竖排、ruby）仍然没有布局模型，宿主拿到诊断后自行
  降级；fixed-layout / 脚本化内容同理，处置是宿主的事。
- 诊断是"我们丢掉了什么"的出口，不是能力：`IssueMissingGlyphs` 只告诉你字体覆盖不到，
  fallback 字体仍由宿主挑选并通过 `FontSet` 提供。
- `Style` 的零值是"继承"，所以 `FirstLineIndent: 0`、`LetterSpacing: 0`、
  `Hyphenate: false` 这类"显式取消继承"无法表达；请按已级联的样式构建。
- 锚点是节点级、不是 rune 级；跨书版本的稳定性由宿主的 `AnchorID` 保证。
- 标点挤压只压缩成对引号的半宽，规则按 linecmp 量到的 Chromium 行为固定；`widows`/
  `orphans` 零值按 CSS 初始值 2/2，要填满页面写 `1`；悬挂标点只在关闭断字时生效。
- 断字默认关闭，需同时给 `LayoutOptions.Hyphenator`（如 `hyphenen.New()`）和块样式
  `Style.Hyphenate: true`。
- 分页暂按行边界，复杂 `break-inside` 与跨页图片策略仍需细化。
- `Layout` 常驻整本书的行盒与 flow，约 4 B/字：一百万字的书约 4 MB。剩下的大头不在
  布局里，而在 `Document`（原文）与 `FontSet`（字体字节 + go-text 解析出的 glyf 表，
  一套中文字体约 20 MB，**每个 Engine 共享一份，不要按书或按字号重建**）。
- `Limits` 的默认值面向"单本电子书"：超过默认上限的书会以 `ErrLimitExceeded` 失败，而不是
  慢慢吃内存。超大工具书/网络连载请用 `LayoutRange` 按章排版，确需整本时再显式调高上限。
- 峰值 RSS 主要由单次布局的分配 churn 决定（Go 的 GC 让堆长到存活集的 2 倍左右），
  churn 里约一半是 go-text 的 shaping 与 bidi 之外无法再省的部分。宿主若在 Windows 上
  看到远大于此的数字，先确认没有按字号重建 `FontSet`、没有把 `TileHeight` 设成整本书
  高度（一个 tile 的显示列表就是全书字形），再用 `tools/memreport` 对齐量一次。
- `examples/wasm` 的 demo 在改字号/行高时仍走冷布局；benchmark 路径已走 `Relayout`。

## 更多文档

- [English README](README.en.md) — 同一份入口的英文版
- [`docs/api.md`](docs/api.md) — API 参考：越界语义、`IssueCode`、`Limits`、错误分类、`LayoutRange`
- [`docs/design.md`](docs/design.md) — 设计取舍与不变量
- [`docs/text-rules.md`](docs/text-rules.md) — 排版政策、默认值出处与中文推荐开关
- [`epub/README.md`](epub/README.md) — EPUB 解析层
- [`CONTRIBUTING.md`](CONTRIBUTING.md) · [`CHANGELOG.md`](CHANGELOG.md) · [`SECURITY.md`](SECURITY.md)

## 许可

本库以 **MIT** 许可发布，见 [`LICENSE`](LICENSE)。测试字体
`render/testdata/DejaVuSerif.ttf` 使用 DejaVu（Bitstream Vera 派生）许可，单独见
`render/testdata/DejaVu-LICENSE.txt`；再分发字体字节时请保留该文件。
