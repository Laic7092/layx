# layx

纯 Go 的 reflowable 文档排版 / 渲染内核。输入是宿主已经解析、级联好的 `render.Document`，
输出是分页结果、`DisplayList` 和命中/选区/锚点映射。**不解析 EPUB/XHTML/CSS，不内置光栅器。**

当前进度：**M0、M1、M2、M3 已落地**（分页 + 滚动、三层缓存、tile 与行缓存）。

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

## 包结构

```text
github.com/laix/layx/render        核心：IR、字体、布局、分页、命中、选区
github.com/laix/layx/render/paint  绘制接口与基础类型（无光栅器）
examples/layout                    最小示例：排版并打印页数
```

核心直接依赖只有 `go-text/typesetting`（shaping / UAX #14 断行）和
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

### 中文小说的推荐开关组

引擎不自作主张：标点挤压、行末悬挂、孤行/寡行保护、中西文间距都是**政策**，零值
一律是规范初始值（即全部关闭）。做中文正文时建议在 `Typography.Rules` 里显式打开：

```go
Typography: render.Typography{
    FontFamily: "serif",
    FontSize:   18,
    LineHeight: 1.6,
    Color:      paint.Color{A: 0xff},
    Rules: render.TextRules{
        PunctuationCompression: true,  // 成对全角标点各收半宽，与 Chrome 默认一致
        HangingPunctuation:     false, // 行末标点探出；浏览器默认不悬挂，默认关
        Autospacing:            false, // 中西文 1/8 em；Chrome 不加，混排想对齐浏览器就关
        Widows:                 2,     // 0 = 不保护
        Orphans:                2,     // 0 = 不保护
    },
},
// 首行缩进属于文档样式，不在 Typography 上：
Style{FontSize: 18, FirstLineIndent: 36, TextAlign: render.AlignJustify},
```

`Rules` 整体参与 `BreakKey`，改任何一个开关都会让断行结果失效重算，不会出现
"缓存命中但排版是旧的"。

位置语义：

- `Location{Chapter, Node, Anchor, Offset}`：`Node`/`Offset` 用于当前 layout 内的命中、选区、
  搜索；`Anchor` 是宿主可持久化的稳定锚点（无锚点时为 0）。
- `Layout.Locate(anchor)` → `Location`，`Layout.PageOf(loc)` → 页码。
- `Page.HitTest(x, y)`、`Layout.Selection(from, to)` 返回的是同一套 `Location` / 页面矩形。
- 宿主负责把 `Location` 映射到 CFI、书签或数据库进度。

## M0 已完成

- 扁平节点 IR（block / inline / text / image / br）+ 构建期校验（环、越界、样式、锚点、深度）
- 纯 Go shaping：`go-text/typesetting` + 字体 fallback（按 family / weight / slant / 字形覆盖）
- UAX #14 断行，中英文混排；CJK 行首禁则由 UAX #14 覆盖（测试覆盖 `，。、；：！？）` 等）
- 块布局：margin、嵌套 block、段落间距、首行缩进、左/右/居中/两端对齐
- 图片：按固有宽高比预留、超宽缩放、居中/左右对齐、不跨页
- 分页：页边距、`break-before/after`、`KeepTogether`、`widows/orphans`
- 并发：顶层 block 任务级并行，每 worker 独立 `HarfbuzzShaper` / `LineWrapper` / `FontFace`
- 内存策略：只保留行盒、页映射、段落 rune 和锚点，不保留全书 shaped glyph
- `Page` 懒生成 DisplayList + FIFO PageCache；`Page.Draw` 可并发（每 goroutine 自己的 Painter）
- 命中测试、跨页选区、页面链接、锚点 → 页
- 取消（`context`）、`Limits`、typed error、无 `init()`、无日志、无全局状态
- 基准 / golden / race / workers 一致性测试

## 实测（本机，AMD Ryzen 7 5800H）

Noto Sans CJK SC，18px，600×900 视口，5,000,000 字，只做布局（不含绘制）：

| Workers | 时间 | 分配 |
|---:|---:|---:|
| 1 | 4.37 s | 860 MB |
| 4 | 1.44 s | 864 MB |
| 8 | 0.92 s | 870 MB |
| 16 | 0.87 s | 882 MB |

说明：这是 M0 的**总分配量**，不是常驻内存；行盒/页映射是常驻的，shaped glyph 不保留。
分配量主要来自每段的 shaping/wrapping 临时对象，M1 会做池化与分块复用。

复现：

```sh
LAYX_CJK_FONT=/path/to/NotoSansCJK-Regular.ttc \
LAYX_CJK_FONT_INDEX=2 \
go test ./render -run '^$' -bench 'BenchmarkLayout5M' -benchtime 1x
```

## 渲染快照 / 阶段对比

`scripts/snapshot.sh` 用无头浏览器把引擎输出截成 PNG，并按阶段做前后对比。
快照是构建产物，放在 `snapshots/`（已 gitignore），不会进仓库。

```sh
./scripts/snapshot.sh M0                 # 记录当前阶段
./scripts/snapshot.sh M1 --pages 1,2     # 下一个阶段，自动和 M0 对比
./scripts/snapshot.sh --compare M0 M1    # 只重生成对比图
cat snapshots/index.md                   # 阶段元数据 + 图片 + 像素差异统计
```

依赖：`go`、`curl`，以及 Chrome/Chromium（或 WSL 下的 Windows Chrome/Edge；
也可以用 `CHROME=/path/to/chrome` 指定）。对比图是左右并排 + 差异热力图，
统计输出形如 `changed_pct=6.58 mean_abs_diff=6.988`。

## 与 WebView 的 A/B

`scripts/ab.sh` 在同一个 Chromium 里跑两条路径：`native`（layx 编译成 wasm）
和 `webview`（DOM + CSS multi-column），语料、viewport、字体、字号、页边距完全一致。

```sh
./scripts/ab.sh m1 --chars 200000 --turns 100 --repeat 3
```

输出表格并写 `snapshots/ab/m1.json`（gitignored）。

两边的字体必须是同一份字节。WebView 侧走 `@font-face`，而 Chrome 的 OTS
会拒绝 `.ttc` 字体集，所以 `build.sh` 给 demo 选 WenQuanYi/Noto 这类好看的
`.ttc`，`ab.sh` 则固定一个单 face `.ttf`。本机唯一同时支持 CJK 的单 face
TTF 是 Droid Sans Fallback，它没有拉丁字形：native 侧把英文画成 `.notdef`，
浏览器却会换成别的字体，两侧量的就不是同一段文字了。所以 `ab.js` 会先探测
字体覆盖（渲染像素哈希，见 `fontCoverage`），把字体画不出来的段落从语料里
去掉，并在结果里记 `fontCoverage` / `corpusParagraphsDropped`；`ab_report.py`
在表格下面打一行 note。早先那一版没有这条保护，英文段落贡献了一个**假的**
27% 行数差（详见下一节）。想要可读且覆盖完整的输出，就用
`LAYX_FONT=/path/to/single-face.ttf ./scripts/ab.sh ...`。覆盖：首页耗时、整书布局、
冷/热翻页 p50/p99、字号重排、行高重分页、内存。尚未覆盖：应用冷启动、
进程树 RSS、滚动掉帧、多线程 native（wasm 只有 GOMAXPROCS=1）。

## 与浏览器的断行一致性（linecmp）

`scripts/linecmp.sh` 把同一段文字、同一份字体字节交给两边，逐**行**比对内容：
native 侧由 `render/linecmp_test.go` 导出每个行盒（文本、宽度、y），浏览器侧用
`Range.getClientRects()` 把字符按行分组。A/B 比的是耗时，这里比的是断行。

```sh
./scripts/linecmp.sh cjk          # 纯中文，Droid Sans Fallback
./scripts/linecmp.sh punct --trim both   # 对话密集、标点成串
./scripts/linecmp.sh latin        # 英文，DejaVu Serif
./scripts/linecmp.sh mixed-ja     # 日文 + 英文词 + 数字，IPA Gothic
```

四种语料、字体覆盖各自的文字，结果：

| 语料 | 行数 | 行内容一致 | 页数 |
|---|---|---|---|
| 纯中文 | 54 | 54/54 | 3 == 3 |
| 英文（同字体） | 42 | 42/42 | 2 == 2 |
| 日文 + 英文混排 | 63 | 63/63 | 3 == 3 |
| 中文对话、标点密集 | 70 | 42/70 | 3 == 3 |
| 同上，`text-spacing-trim: space-all` | 70 | **70/70** | 3 == 3 |

最后两行是同一段文字：把浏览器的全角标点挤压关掉之后，逐行完全一致。也就是说
断行本身（UAX #14 断点、行尾宽度、缩进、两端对齐前的自然宽度）与 Chrome 一致；
**唯一的差别是 Chrome 默认的 `text-spacing-trim` 会压缩成对的全角标点**（`。”`、
`：“`、`——`、`……`），我们还没做。对话密集的文本里它每行多放 1–2 个字，页数
可能差一页。这就是“标点挤压”的量化依据，也是 A/B 里剩下的那 3% 页数差。

`--trim space-all` 这行依赖浏览器真的支持该属性：本机的 headless Chromium 145
会解析却忽略它，Windows Chrome 则正确生效，脚本会在那种情况下打印 note。

## 滚动模式（M2）

```go
layout, _ := eng.Layout(ctx, doc, render.LayoutOptions{
    Mode:       render.ModeScroll, // 默认 ModePaged
    TileHeight: 1024,              // 0 = Viewport.Height
    ...
})
layout.ContentHeight() // 整条流的高度
layout.TileCount()
layout.Tile(i)         // 首次访问构建，之后按 Options.TileCache（默认 8）做 LRU
layout.Tiles(lo, hi)   // 同步预取，不引入隐藏 goroutine
```

分页与滚动共享同一份断行与块布局，只是"取窗口"的方式不同；测试用字形
id/cluster/内容坐标逐字比对两条路径。**tile 是 DisplayList，不是位图**：
`render` 没有光栅器，是否把 tile 缓存成图片由宿主决定。

`Options.LineCache`（默认 256）缓存未做两端对齐的 shaped 行，滚动时避免对
同一行重复 shaping；命中不增加 `Engine.shapes` 计数。

## 变体缓存（M3）

`Layout.Relayout` 按三层 key 复用阶段产物：

```text
BreakKey = hash(text, fonts, fontSize, letterSpacing, width, lang, align, indent, rules)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins)
PaintKey = hash(PageKey, theme)
```

`rules` 是 `Typography.Rules`（`TextRules`）的**整体哈希**：新增一个政策字段
会自动进 key。`Widows`/`Orphans` 也只影响分页，但出于同样的安全考虑一起进了
`BreakKey`，代价是改孤行设置时多做一次断行。

- `BreakKey` 命中：不 shaping、不断行，直接复用 `breakUnit`。
- `PageKey` 命中：复用行盒与页映射，只重算 y 和页边界。
- 只保留每章最近 2 个变体；`breakUnit` 只存 rune 区间和行度量，不存 shaped glyph。

实测（本机，500 万字）：行高重排 22ms，段间距重排（20 万字）0.16ms。

## 测试

```sh
go test ./... -count=1
go test ./render -race -count=1
go vet ./...
go test ./render -run '^$' -bench . -benchtime 1x
```

`render/testdata/DejaVuSerif.ttf` 用于确定性 golden 测试；CJK 测试会自动探测常见
Noto 路径，找不到则跳过（可用 `LAYX_CJK_FONT` / `LAYX_CJK_FONT_INDEX` 指定）。

## 已知限制 / 下一步

- 尚未实现：表格、ruby、竖排、flex/grid、RTL 视觉序、浮动环绕、脚注、链接命中透传。
- 标点压缩 / 行末标点悬挂（`text-spacing-trim`）尚未实现；目前依赖字体自带的
  全角标点宽度。这一项的影响已经量化（见上一节）：对话密集的中文文本里，Chrome
  靠挤压成对标点每行多放 1–2 个字；把挤压关掉后两边逐行一致。
- 两端对齐会把余量分给空格和 CJK 字间隙；标点相邻的间隙按半权重参与。
- **两个默认值是故意偏离旧行为的**（`docs/text-rules.md` 的决策）：
  `Typography.Rules.Widows` / `Orphans` 的零值现在是"关闭"，不再提升为 2/2；
  `Autospacing`（中西文 1/8 em）默认 off。需要旧行为就显式赋值：孤行/寡行写
  `Widows: 2, Orphans: 2`，中西文间距写 `Autospacing: true`。golden 里凡是含
  中西混排或依赖 2/2 分页的布局都会随这两条变化，测试已改为显式传值。
- `keep-with-next`（标题不与正文分离）尚未实现，只有 `KeepTogether` 与
  `break-before/after`。
- 行高 / 段间距变化走 `Layout.Relayout` 的变体缓存（500 万字约 22ms）；但
  `examples/wasm` 目前仍直接调用 `Engine.Layout`，所以浏览器 demo 没有用上这条路径。
- 分页暂按行边界；复杂 `break-inside` 与跨页图片策略仍需细化。
- 页面背景与主题色由 `Typography.Background` / `Typography.Color` 提供；块背景、
  边框与文字装饰由 `Style.Background` / `Style.Border*` / `Style.TextDecoration`
  提供，绘制后端自行决定。
- fixed-layout / 脚本化 EPUB 不在 IR 的表达范围内：核心只接受已解析、可重排的
  `Document`，这类内容的处置是宿主的事。
- 与浏览器的差距：**已补齐** —— 边框/背景/圆角、下划线与删除线、
  `word-spacing`、合成粗体标记、可插拔断字（`render/hyphen/en` 内置 TeX
  英文模式）；**能补没补** —— 标点挤压/悬挂（影响已量化，见 linecmp 一节）、表格、
  脚注；**IR 不表达** —— flex/grid、竖排、ruby、transform、脚本。
  “IR 不表达”只说明核心不认识这些结构，不说明宿主该怎么办：那是宿主的决定。
  核心的责任是把“这个文档里有我表达不了的东西”报告出来，而不是默默丢掉或
  自行降级——参见下面的“诊断”。
- 早先记的“A/B 里总行数比 Chrome 多 27%”是测量错误，已查清并修掉：语料里那段
  纯英文用的是 Droid Sans Fallback，它没有拉丁字形，native 画 `.notdef`、浏览器
  换字体，两边量的不是同一段文字。加上字体覆盖探测后同一份 A/B 的页数是 61 vs 63
  （3%），剩下的差异就是上一条的标点挤压。断行本身逐行与 Chrome 一致。
- 断字默认关闭。宿主需同时给 `LayoutOptions.Hyphenator`（例如
  `hyphenen.New()`）和块样式 `Style.Hyphenate: true`；两者都齐才会在词内加
  连字符，且只有整词放不下时才启用。
- 性能上：单核与 Blink 打平（500 万字 1.54s vs 外推 1.41s），8 核快 3–5×，
  改行高快一个数量级；shaping 用 go-text（HarfBuzz 的 Go 移植），单核上限低于
  Blink；滚动/翻页的合成优势在宿主，核心只提供 tile DisplayList。

## 许可

核心代码尚未附 LICENSE；测试字体 `DejaVuSerif.ttf` 使用 DejaVu 许可，见
`render/testdata/DejaVu-LICENSE.txt`。
