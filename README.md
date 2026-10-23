# layx

纯 Go 的 reflowable 文档排版 / 渲染内核。输入是宿主已经解析、级联好的 `render.Document`，
输出是分页结果、`DisplayList` 和命中/选区/锚点映射。**不解析 EPUB/XHTML/CSS，不内置光栅器。**

当前进度：**M0 已落地，M1 排版质量进行中**。

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

输出表格并写 `snapshots/ab/m1.json`（gitignored）。覆盖：首页耗时、整书布局、
冷/热翻页 p50/p99、字号重排、行高重分页、内存。尚未覆盖：应用冷启动、
进程树 RSS、滚动掉帧、多线程 native（wasm 只有 GOMAXPROCS=1）。

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
  全角标点宽度，行末标点会占满一个字宽。
- 两端对齐会把余量分给空格和 CJK 字间隙；标点相邻的间隙按半权重参与。
- `keep-with-next`（标题不与正文分离）尚未实现，只有 `KeepTogether` 与
  `break-before/after`。
- 行高 / 段间距变化目前仍走完整重排；`BreakKey` / `PageKey` 缓存是 M3。
- 分页暂按行边界；复杂 `break-inside` 与跨页图片策略仍需细化。
- 背景色、主题色由宿主通过 `Typography.Background` / `Style.Color` 提供，绘制后端自行决定。
- 当前不做 fixed-layout / 脚本化 EPUB；按设计应走 WebView fallback。

## 许可

核心代码尚未附 LICENSE；测试字体 `DejaVuSerif.ttf` 使用 DejaVu 许可，见
`render/testdata/DejaVu-LICENSE.txt`。
