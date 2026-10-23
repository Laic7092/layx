# layx

纯 Go 的 reflowable 文档排版 / 渲染内核。输入是宿主已经解析、级联好的 `render.Document`，
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
设计取舍见 [`docs/design.md`](docs/design.md)。

## 包结构

```text
github.com/laix/layx/render        核心：IR、字体、布局、分页、命中、选区
github.com/laix/layx/render/paint  绘制接口与基础类型（无光栅器）
github.com/laix/layx/render/hyphen/en  可选：TeX 英文断字模式，核心不 import 它
examples/layout                    最小示例：排版并打印页数
```

直接依赖只有 `go-text/typesetting`（shaping / UAX #14 断行）和
`golang.org/x/image/math/fixed`。`go list -deps ./render` 里会出现 `image/*`、
`encoding/xml`，这是 `go-text/typesetting/font` 解析彩色/位图字体带进来的传递依赖，
不是本库直接引入。`render/deps_test.go` 对**直接 import** 做了闸门。

## 用法

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
        Color:      render.Color{A: 0xff},
    },
    Fonts:   fonts,
    Margins: render.Insets{Top: 40, Right: 40, Bottom: 40, Left: 40},
})
if err != nil {
    return err
}

page := layout.Page(0)   // 首次访问时生成 DisplayList，之后按 PageCache 缓存
page.Draw(painter)       // 只回放，不重排、不 shaping 全书
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

## 中文正文的推荐开关组

引擎不自作主张：标点挤压、行末悬挂、孤行/寡行保护、中西文间距都是**政策**，零值
一律是规范初始值（即全部关闭）。做中文正文时建议在 `Typography.Rules` 里显式打开：

```go
Typography: render.Typography{
    FontFamily: "serif",
    FontSize:   18,
    LineHeight: 1.6,
    Color:      paint.Color{A: 0xff},
    Rules: render.TextRules{
        PunctuationCompression: true,  // 成对引号收半宽，与 Chrome 默认逐行一致
        HangingPunctuation:     false, // 行末标点探出；浏览器默认不悬挂，默认关
        Autospacing:            false, // 中西文 1/8 em；Chrome 不加，混排想对齐浏览器就关
        Widows:                 2,     // CSS 初始值；要填满整页就写 1
        Orphans:                2,     // 同上，1 是最小合法值
    },
},
// 首行缩进属于文档样式，不在 Typography 上：
Style{FontSize: 18, FirstLineIndent: 36, TextAlign: render.AlignJustify},
```

政策清单、默认值的规范出处、以及"新增开关必须进缓存 key"的约束见
[`docs/text-rules.md`](docs/text-rules.md)。

## 位置语义

- `Location{Chapter, Node, Anchor, Offset}`：`Node`/`Offset` 用于当前 layout 内的
  命中、选区、搜索；`Anchor` 是宿主可持久化的稳定锚点（无锚点时为 0）。
- `Layout.Locate(anchor)` → `Location`，`Layout.PageOf(loc)` → 页码。
- `Page.HitTest(x, y)`、`Layout.HitTest(x, y)`、`Layout.Selection(from, to)` 返回同一套
  `Location` / 矩形。
- `Layout.LocationAt(y)` / `Layout.OffsetOf(loc)` 在分页与滚动两种模式下使用同一套
  内容坐标，并且互为逆运算。
- 宿主负责把 `Location` 映射到 CFI、书签或数据库进度。

## 两种输出

`LayoutOptions.Mode` 选择输出方式，断行与块布局完全共享，只有"取窗口"不同。

### 分页（`ModePaged`，默认）

```go
layout.PageCount()
layout.Page(i)          // 首次访问构建 DisplayList，之后按 Options.PageCache 做 LRU
page.Draw(painter)
page.HitTest(pt)
page.Links()
```

### 滚动（`ModeScroll`）

```go
layout, _ := eng.Layout(ctx, doc, render.LayoutOptions{
    Mode:       render.ModeScroll,
    TileHeight: 1024,              // 0 = Viewport.Height
    ...
})
layout.ContentHeight() // 整条流的高度
layout.TileCount()
layout.Tile(i)         // 首次访问构建，之后按 Options.TileCache 做 LRU
layout.Tiles(lo, hi)   // 同步预取，不引入隐藏 goroutine
```

**tile 是 DisplayList，不是位图**：`render` 没有光栅器，是否把 tile 缓存成图片由宿主决定。

内容坐标：

- 滚动模式：`y = 0` 是整本书正文顶端，章与章连续堆叠；每章自己的根 block 边距保留，
  页边距不在章间重复。`Margins.Top/Bottom` 是页面视口 inset，不进入内容坐标；
  `Margins.Left/Right` 仍用于 tile 内的 x 定位。
- 分页模式：页面视口首尾相接成一条虚拟流，第 p 页覆盖 `[p*contentHeight, (p+1)*contentHeight)`。
- 每行只属于一个 tile（按行盒顶边分配）；图片可跨 tile，由 Painter 裁剪。因此所有
  tile 的字形总数等于所有页的字形总数。

## 重排与缓存

```text
BreakKey = hash(text, fonts, fontSize, letterSpacing, width, lang, align, indent, rules)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins)
PaintKey = hash(PageKey, theme)
```

`Layout.Relayout(ctx, opts)` 按这三层 key 复用阶段产物：

- `BreakKey` 命中：不 shaping、不断行，直接复用 `breakUnit`。
- `PageKey` 命中：复用行盒与页映射，只重算 y 和页边界。
- 只保留每章最近 2 个变体；`breakUnit` 只存 rune 区间和行度量，不存 shaped glyph。
- `Options.LineCache`（默认 256）缓存未做两端对齐的 shaped 行，命中不增加 `Engine.shapes`。

`rules`（`Typography.Rules`）作为整体进 key：新增一个政策字段会自动参与哈希。

## 文本排版能力

- 纯 Go shaping（`go-text` + HarfBuzz 移植），按 family/weight/slant/字形覆盖做 fallback。
- UAX #14 断行，中英日混排；行首禁则由 UAX #14 覆盖。
- 块布局：margin（相邻折叠）、嵌套 block、段落间距、首行缩进、左/右/居中/两端对齐。
- 两端对齐把余量分给空格和 CJK 字间隙，标点相邻的间隙按半权重参与。
- 图片：按固有宽高比预留、超宽缩放、居中/左右对齐、不跨页。
- 分页：页边距、`break-before/after`、`KeepTogether`、`widows/orphans`。
- 可选：标点挤压、行末悬挂、中西文间距、`word-spacing`、断字（`render/hyphen/en`）。
- 表现层：块背景/边框/圆角、下划线/删除线、合成粗体标记（`EmulateBold`，是否加粗由宿主决定）。
- 并发：顶层 block 任务级并行，每个 worker 独立 `HarfbuzzShaper` / `LineWrapper` / `FontFace`。
- 取消（`context`）、`Limits`、typed error、无 `init()`、无日志、无全局状态。

## 性能

本机（Ryzen 7 7800X3D，8 核 16 线程），文泉驿微米黑，18px，600×900 视口，
5,000,000 字，只做布局（不含绘制）：

| Workers | 时间 | 分配 |
|---:|---:|---:|
| 1 | 1.17 s | 722 MB |
| 4 | 0.34 s | 726 MB |
| 8 | 0.23 s | 731 MB |
| 16 | 0.20 s | 742 MB |

分配量是总量，非常驻内存；行盒/页映射常驻，shaped glyph 不保留。同一台机器上
500 万字行高重排 22 ms，20 万字段间距重排 0.17 ms（均只重算 y 和页映射）。

复现（换成任何覆盖中文的字体）：

```sh
LAYX_CJK_FONT=/usr/share/fonts/truetype/wqy/wqy-microhei.ttc \
LAYX_CJK_FONT_INDEX=0 \
go test ./render -run '^$' -bench 'BenchmarkLayout5M|BenchmarkRelayout' -benchtime 1x
```

## 验证

```sh
go test ./... -count=1
go test ./render -race -count=1
go vet ./...
```

三个对照脚本需要 `curl` 和 Chrome/Chromium（或 `CHROME=/path/to/chrome`）：

- `./scripts/linecmp.sh {cjk,latin,mixed-ja,punct}`：同一份字体字节交给引擎与浏览器，
  逐**行**比对内容（native 侧导出行盒，浏览器侧用 `Range.getClientRects()`）。
  纯中文 54/54、英文 42/42、日英混排 63/63、对话密集 + 挤压 70/70 行内容一致。
- `./scripts/ab.sh book --chars 200000 --turns 100`：同一个 Chromium 里跑 layx-wasm 与
  DOM + CSS multi-column，比对首页耗时、整书布局、冷/热翻页、字号重排、行高重分页。
  脚本用像素哈希探测字体覆盖并剔除画不出的段落，避免两侧量的不是同一段文字。
- `./scripts/snapshot.sh base` / `./scripts/snapshot.sh change --pages 1,2`：无头浏览器
  截图并按阶段做像素差异对比，产物在 `snapshots/`（gitignored）。

## 已知限制

- 尚未实现：表格、ruby、竖排、flex/grid、RTL 视觉序、浮动环绕、脚注。
- `keep-with-next`（标题不与正文分离）未实现，只有 `KeepTogether` 与 `break-before/after`。
- 分页暂按行边界；复杂 `break-inside` 与跨页图片策略仍需细化。
- 标点挤压只压缩"成对引号"的半宽，规则按 linecmp 量到的 Chromium 行为固定；全角括号/
  逗号/句号不压缩，因为 Chromium 在这套字体上也没压它们。悬挂只在关闭断字时生效。
- `Typography.Rules` 的零值按规范初始值处理（`Widows`/`Orphans` 为 2/2）。想要"填满
  页面、不做孤寡行保护"写 `1`。
- `examples/wasm` 的浏览器 demo 仍直接调用 `Engine.Layout`，没有使用 `Relayout` 的
  变体缓存。
- fixed-layout / 脚本化 EPUB 不在 IR 的表达范围内：核心只接受已解析、可重排的
  `Document`，这类内容的处置是宿主的事。
- **不提供诊断输出**：IR 表达不了的内容（表格、ruby、竖排等）目前不会被报告，宿主需要
  在构建 `Document` 时自行记录并决定降级策略。
- 与浏览器的差距：能补没补的是表格与脚注；IR 不表达的是 flex/grid、竖排、ruby、
  transform、脚本。两者的区别是前者只需要新样式/分页规则，后者需要新的布局模型。
- 断字默认关闭，需同时给 `LayoutOptions.Hyphenator`（如 `hyphenen.New()`）和块样式
  `Style.Hyphenate: true`；只有整词放不下时才在词内加连字符。
- 性能上：单核与 Blink 打平，多核快 3–5×，改行高快一个数量级；滚动/翻页的合成优势
  在宿主，核心只提供 tile DisplayList。

## 许可

核心代码尚未附 LICENSE；测试字体 `render/testdata/DejaVuSerif.ttf` 使用 DejaVu 许可，
见 `render/testdata/DejaVu-LICENSE.txt`。
