# layx

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
设计取舍见 [`docs/design.md`](docs/design.md)；标点/断行政策、默认值的规范出处、
中文正文的推荐开关组见 [`docs/text-rules.md`](docs/text-rules.md)。

## 包结构

```text
github.com/laix/layx/render            核心：IR、字体、布局、分页、命中、选区
github.com/laix/layx/render/paint      绘制接口与基础类型（无光栅器）
github.com/laix/layx/render/hyphen/en  可选：TeX 英文断字模式，核心不 import 它
github.com/laix/layx/epub              EPUB/XHTML/CSS → render.Document（nested module）
examples/layout                        最小示例：排版并打印页数
examples/wasm                          wasm + Canvas 2D 原型（含 bench 路径）
tools/{bench_native,snapshotimg,corpora}  开发工具，不属于库
```

`epub/` 有自己的 `go.mod` 和 [`epub/README.md`](epub/README.md)，不在根 `go test ./...` 里。
直接依赖只有 `go-text/typesetting`（shaping / UAX #14 断行）与 `golang.org/x/image/math/fixed`；
`go list -deps ./render` 里的 `image/*`、`encoding/xml` 是 `go-text/typesetting/font` 带进来的
传递依赖。`render/deps_test.go` 对**直接 import** 做了闸门。

## 快速开始

```go
eng, _ := render.New(render.Options{Workers: 0}) // 0 = GOMAXPROCS

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
page := layout.Page(i) // 首次构建，之后按 Options.PageCache（默认 4）做 LRU
page.Draw(painter)
page.HitTest(pt)
page.Links()
```

滚动（`ModeScroll`）：

```go
render.LayoutOptions{Mode: render.ModeScroll, TileHeight: 1024} // 0 = Viewport.Height
layout.ContentHeight()
layout.TileCount()
tile := layout.Tile(i) // 首次构建，之后按 Options.TileCache（默认 8）做 LRU
layout.Tiles(lo, hi)   // 同步预取，不引入隐藏 goroutine
```

**tile 是 DisplayList，不是位图**：`render` 没有光栅器，是否缓存成图片由宿主决定。

内容坐标：滚动模式下 `y = 0` 是整本书正文顶端，章与章连续堆叠，`Margins.Top/Bottom`
只是视口 inset；分页模式下页首尾相接成一条虚拟流。每行只属于一个 tile（按行盒顶边
分配），图片可跨 tile，由 Painter 裁剪。

宿主画家可实现可选的 `paint.Clipped`（`Visible() paint.Rect`）：`Page.Draw` / `Tile.Draw`
会跳过与可见区不相交的 op，字形 op 各自带行盒矩形。不实现该接口的画家仍然收到全部
op，回放语义不变。

## 位置与坐标

- `Location{Chapter, Node, Anchor, Offset}`：`Node`/`Offset` 用于当前 layout 内的命中、
  选区、搜索；`Anchor` 是宿主可持久化的节点级锚点（无锚点时为 0）。**持久化存
  `Anchor` + `Offset`**：`Anchor` 非零优先于 `Node`，所以换书重建 `Document` 后旧 node
  号不会盖掉稳定锚点。锚点按块/段/标题铺，成本 O(#anchors)。
- `Locate(anchor)` → `Location`（只到节点，`Offset` 为 0），`PageOf(loc)` ↔
  `LocationOfPage(page)`；`PageRange(chapter)` → `[first, first+count)`，
  `ChapterPages()` 给出每章首页。滚动模式没有页，后用 `ChapterContentTop(chapter)`。
- 命中与选区：`Page.HitTest`、`Layout.HitTest`、`Layout.Selection(from, to)` 返回同一套
  `Location` / 矩形。`SelectionQuad` 带两套坐标：`Rect` 是页内坐标（仅分页模式），
  `Content` 是内容坐标（两种模式都有；滚动模式减 `Tile.Top()` 得 tile 内坐标）。
- `LocationAt(y)` ↔ `OffsetOf(loc)` 在两种模式下共用一套内容坐标，互为逆运算。
  `Progress(loc)` ∈ [0,1]：分页按「页 + 页内偏移」，滚动按 `y / ContentHeight`；两者
  分母不同，同一位置的百分比会有小差异，**不要持久化百分比**。
- `Document.Text(chapter)` 按阅读序拼接该章文本，并返回每个节点的 rune 区间（非文本
  节点为零宽 run），搜索和复制不必自己遍历 `NodeText`；它不插段落分隔符。
- `Layout.Document()` 的指针身份可判断索引/图片缓存是否过期；`Margins()` /
  `PageContentHeight()` 让宿主不必反推页边距和页内容高。

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
关闭时布局路径不读时钟。

## 性能

同语料 A/B（20 万字中文，640×900，18px/1.65，Droid Sans Fallback，`scripts/ab.sh`）：

- **单核与 Blink 同级**：59.1 vs 58.5 ms；8/16 worker 快 4.2×（14.1 / 13.7 ms）。
- **行高重排快 22×**：1.1 vs 24.3 ms。`LineHeight` 不在 `BreakKey` 里，一次 shaping 都不做。
- **wasm 单线程比原生慢 4.6×**（269.6 ms）：这是运行时不是算法，所以在浏览器里跑 layx
  目前比浏览器自己的排版慢；作为原生库嵌进阅读器才与 Blink 同级。

数字由脚本现场测量，不要跨组比较：`scripts/ab.sh`（浏览器侧）、`tools/bench_native`
（原生侧，同一份字体字节）、`go test ./render -run '^$' -bench 'BenchmarkLayout5M|BenchmarkRelayout'`
（500 万字）。

## 验证

```sh
go test ./... -count=1
go test ./render -race -count=1
go vet ./...
```

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
- **不提供诊断输出**：IR 表达不了的内容不会被报告，宿主构建 `Document` 时自行记录并
  决定降级策略。fixed-layout / 脚本化内容同理，处置是宿主的事。
- `Style` 的零值是"继承"，所以 `FirstLineIndent: 0`、`LetterSpacing: 0`、
  `Hyphenate: false` 这类"显式取消继承"无法表达；请按已级联的样式构建。
- 锚点是节点级、不是 rune 级；跨书版本的稳定性由宿主的 `AnchorID` 保证。
- 标点挤压只压缩成对引号的半宽，规则按 linecmp 量到的 Chromium 行为固定；`widows`/
  `orphans` 零值按 CSS 初始值 2/2，要填满页面写 `1`；悬挂标点只在关闭断字时生效。
- 断字默认关闭，需同时给 `LayoutOptions.Hyphenator`（如 `hyphenen.New()`）和块样式
  `Style.Hyphenate: true`。
- 分页暂按行边界，复杂 `break-inside` 与跨页图片策略仍需细化。
- `Layout` 常驻整本书的行盒与 flow：几十万字的长篇会到几百 MB。按阅读窗口淘汰 flow
  的实现实测收益不足，已移除；要控住内存，目前的做法是宿主按章拆分 `Document` 再分别
  `Layout`。
- `examples/wasm` 的 demo 在改字号/行高时仍走冷布局；benchmark 路径已走 `Relayout`。

## 许可

测试字体 `render/testdata/DejaVuSerif.ttf` 使用 DejaVu 许可，见
`render/testdata/DejaVu-LICENSE.txt`。
