# 电子书渲染引擎（库）设计

> **状态：设计草案**
> 目标：设计一个可被大量 Go 项目引用的电子书排版/渲染库，首先服务 reflowable EPUB。
> 核心边界：**不解析 EPUB，不解析 HTML/CSS，不内置光栅器。**

---

## 0. 结论

做一个“**已解析文档 → 行盒/页映射/绘制命令**”的纯 Go 库，而不是浏览器，也不是 EPUB 解析器。

```text
宿主/编译器：
  EPUB 容器、OPF、spine、nav、资源、CFI
  XHTML/CSS 语法解析、选择器匹配、级联、净化
  ↓ 输出 RenderDoc
渲染引擎：
  shaping / 断行 / 块布局 / 分页 / 滚动窗口
  DisplayList
  命中测试 / 选区矩形 / 锚点映射
  ↓ 输出 Page / Tile
宿主：
  Painter 绘制（GPU / 软件 / e-ink）
```

关键选择：

| 决策 | 选择 | 理由 |
|---|---|---|
| 语言 | **纯 Go，无 cgo** | 复用宿主 Go 运行时，不增加第二运行时 |
| 输入 | **RenderDoc IR** | 解析全部留在宿主；核心依赖少、体积小 |
| 文本 | `go-text/typesetting` | 纯 Go、可并发、shaping/断行可控 |
| 布局 | 自研 block/inline/table + pagination | 分页是电子书一等公民 |
| 绘制 | `Painter` 接口，不自带光栅器 | 体积最小；GPU/软件/e-ink 由宿主决定 |
| 分页/滚动 | 共享同一份 FlowLayout | 模式切换、字号变化、锚点不分裂 |
| 并发 | 章/段落级并行 | 断行独立，实测接近线性扩展 |
| 稳定性 | 小 API + 可选子包/独立 module | 适合大量开发者长期引用 |

**不做**：完整 CSS 引擎、JS、fixed-layout 渲染、EPUB/OPF 解析、DRM、内置字体/词典。

---

## 1. 背景与目标

### 1.1 现状

现有基于 WebView 的阅读器方案通常这样工作：

- 解析在 Go，正文 DOM/分页在 WebView。
- 分页依赖 CSS multi-column，页数由 `scrollWidth / viewportWidth` 得出。
- 位置真相是“章 + 进度”，不是 CFI。
- 选区、搜索高亮、标注仍依赖 DOM。

这套方案能跑，但存在边界：

- 版式不可控，跨平台 CSS 多列行为不一致。
- 冷启动、内存、e-ink 刷新不可控。
- 翻页/重排/模式切换的失效范围不清晰。
- 原生选中、标注、CFI 映射受 DOM 限制。

### 1.2 目标

- 提供纯 Go 的 reflowable 文档排版内核。
- 导入时完成默认排版的 **行盒 + 页映射 + 锚点** 计算。
- 字号/字体/宽度变化时，首屏即时，全书后台并行重排。
- 行高/段间距/上下边距变化时，只重分页，毫秒级完成。
- 分页与滚动共享同一套断行和锚点。
- 作为库：依赖少、体积可控、API 稳定、并发语义清晰。

### 1.3 非目标

- 不做 EPUB/OPF/NCX/nav 解析。
- 不做 XHTML/CSS 语法解析（可选 adapter 不在核心）。
- 不做 JS、脚本化 EPUB、DRM/LCP。
- V1 不做完整 flex/grid、MathML、复杂 SVG、脚注弹层。
- 不预渲染整本书的位图。

---

## 2. 核心原则

1. **解析不属于引擎。**
2. **分页和滚动只有一套排版。**
3. **布局预计算，绘制按需。**
4. **不存全书 glyph，不存全书位图。**
5. **并发按章/段落，不按页。**
6. **核心零全局状态、零 `init()`、零日志。**
7. **重能力拆包，不用就不进二进制。**
8. **先证明，再自研；打不过 WebView 就保留 WebView。**

---

## 3. 边界与数据流

### 3.1 宿主/编译器

负责：

- EPUB 容器、OPF、spine、nav/NCX、资源解析。
- XHTML/CSS 语法解析、选择器匹配、级联、继承、净化。
- URL 解析、字体混淆、图片解码/尺寸获取。
- CFI 解析/生成。
- 输出不可变 `RenderDoc`。

### 3.2 引擎

只负责：

- 字体匹配/回退（基于宿主提供的 `FontSet`）。
- shaping、断行、两端对齐、ruby、CJK 禁则。
- block/inline/table/image 布局。
- 分页、滚动窗口、tile 范围。
- DisplayList。
- 命中测试、选区矩形、锚点/文本偏移映射。

### 3.3 RenderDoc

```text
RenderDoc
  Chapters[]
    Nodes[]        // 扁平 arena，NodeID 索引
    Styles[]       // 已级联的样式声明
    Anchors[]      // AnchorID -> NodeID / 文本偏移
    Resources[]    // ImageID / FontID / 其他句柄
  Metadata         // 语言、方向、reflowable/fixed 提示
```

约束：

- `Document` 构建后不可变。
- 样式必须已解析/级联；引擎不处理 CSS 选择器。
- 图片只带 `ImageID + 固有尺寸`，不要求引擎解码。
- CFI 不进引擎；宿主把 CFI 映射为 `AnchorID + Offset`。

---

## 4. 功能范围

### 4.1 V1 支持

- 纯文本、段落、标题、列表、引用。
- 图片、图注、简单浮动。
- 表格（固定/简单自动列宽）。
- 字体、字号、行高、字间距、段间距、页边距。
- 左/右/两端对齐、首行缩进。
- 分页、滚动、章内/跨章锚点。
- 中英文断行、基础标点禁则。
- 命中测试、选区、搜索高亮矩形。
- 主题颜色不影响布局。

### 4.2 后置

- 竖排 `vertical-rl`。
- ruby。
- 复杂表格、flex/grid。
- SVG、MathML。
- 脚注/尾注弹层。
- 无障碍树、TTS 边界。

### 4.3 回退

- fixed-layout EPUB、脚本化 EPUB：继续使用 WebView。
- 复杂 CSS 书籍：WebView fallback。
- 引擎只覆盖 reflowable 小说这一主路径。

---

## 5. 架构

### 5.1 包结构

```text
render/                 核心：IR、布局、分页、命中测试
render/paint            绘制接口与基础类型
render/software         可选：纯 Go 软件绘制后端
render/mygo             可选：mygo Painter 适配
render/xhtml            可选：XHTML/CSS -> Document
render/epub             可选独立模块：EPUB -> Document
render/systemfont       可选：系统字体加载
```

核心 `render` 不导入：

- `archive/zip`、`encoding/xml`、`net/http`
- `golang.org/x/net/html`、CSS parser
- `image/png`、`image/jpeg`
- `encoding/json`、`regexp`、`reflect`
- 任何 UI 框架

`render/epub`、`render/xhtml`、后端适配器应作为**独立 Go module**，避免核心依赖图被污染。

### 5.2 文本层

- `go-text/typesetting` 做 shaping。
- `go-text` 的 `LineWrapper` 做 UAX #14 断行。
- 自研补充：
  - 中文禁则/避头尾。
  - 两端对齐。
  - 行首行尾标点压缩。
  - ruby（后置）。
  - 竖排（后置）。
- 断行阶段使用 `ShapeNoExtents`；绘制可见页时再取 glyph extents。
- 每个 worker 独立 `HarfbuzzShaper` / `LineWrapper`，不可共享。

### 5.3 块/行内布局

- block formatting context。
- inline formatting context。
- 图片按固有宽高比预留空间，避免加载跳动。
- 表格 V1 只做简单模型，复杂列宽顺序处理。
- 浮动 V1 限定常见场景（首字下沉、图片环绕不做完整实现）。

### 5.4 分页

- 输入：连续 FlowLayout + 页高约束。
- 输出：页边界的行索引 + y 偏移。
- 支持：
  - `break-before/after/inside`
  - `widows/orphans`
  - 标题不孤行
  - 图片不跨页（可配置）
- 分页是 O(lines)，在断行之后执行，不需要并行。

### 5.5 滚动

滚动与分页共享 FlowLayout，不另做一套排版。

```text
FlowLayout
  -> Paginator：按页高切片
  -> Scroller：按视口取窗口 + tile 缓存
```

滚动实现要求：

- 整章布局一次，得到内容高度。
- 按 512/1024px 切 tile，只绘制视口 + overscan。
- 滚动帧内只改 offset / 贴 tile，**绝不 layout**。
- 位置存 `chapter + anchor + offset`，不存像素。
- 图片加载完成只重绘对应 tile。
- 跨章预取下一章，不预取整本。
- e-ink 默认分页；滚动作为降级模式。

### 5.6 绘制

引擎输出 DisplayList，宿主用 `Painter` 绘制。

```go
type Painter interface {
    Save()
    Restore()

    ClipRect(r Rect)
    Transform(m Affine)

    FillRect(r Rect, c Color)
    StrokeRect(r Rect, c Color, width float32)
    FillRoundRect(r Rect, radius float32, c Color)

    DrawImage(id ImageID, dst Rect, opts ImageOptions)
    DrawGlyphs(run GlyphRun)
}
```

- 核心不自带光栅器。
- `software` / `mygo` / 平台后端都是可选适配器。
- `Page.Draw` 只回放缓存好的 DisplayList，不重排、不 shaping。
- 传给 Painter 的 slice 只在调用期间有效。

### 5.7 命中测试与选区

- 每个 glyph/line 保留 `NodeID + 文本偏移` 映射。
- `Page.HitTest(x,y)` 返回 `Location`。
- `Layout.Selection(from,to)` 返回跨页 `SelectionQuad`。
- 宿主负责把 `Location` 映射成 CFI。
- 搜索高亮用同一套 `Location` 范围。

---

## 6. 公共 API

### 6.1 最小使用

```go
eng, _ := render.New(render.Options{
    Workers: 0, // 0 = GOMAXPROCS
})

fonts, _ := render.NewFontSet(
    render.FontFace{
        Family: "serif",
        Weight: 400,
        Data:   serifTTF,
    },
)

layout, err := eng.Layout(ctx, doc, render.LayoutOptions{
    Viewport: render.Viewport{Width: 600, Height: 900},
    Typography: render.Typography{
        FontSize:   18,
        LineHeight: 1.6,
    },
    Fonts: fonts,
})
if err != nil {
    return err
}

page := layout.Page(0)
page.Draw(painter)
```

### 6.2 核心类型

```go
type Options struct {
    Workers   int   // 0 = GOMAXPROCS
    PageCache int   // 每 Layout 缓存多少页 DisplayList
    Limits    Limits
}

type Limits struct {
    MaxNodes       int
    MaxDepth       int
    MaxTextRunes   int
    MaxImagePixels int64
}

func New(opts Options) (*Engine, error)
```

```go
type Document struct { /* immutable */ }

type DocumentBuilder struct { /* ... */ }
func NewDocumentBuilder() *DocumentBuilder
func (b *DocumentBuilder) AddChapter(ch Chapter) error
func (b *DocumentBuilder) Build() (*Document, error)
```

```go
type Chapter struct {
    ID      string
    Root    NodeID
    Nodes   []Node
    Anchors map[AnchorID]NodeID
}

type Node struct {
    Kind     NodeKind
    Style    StyleID
    Text     string
    Children []NodeID
    Image    ImageID
    Width    float32
    Height   float32
    Anchor   AnchorID
    Lang     string
}
```

```go
type FontSet struct { /* immutable, safe for concurrent use */ }

// Font 是 FontSet 中的不透明字体句柄，Painter 可通过它取到字体数据与 face index。
type Font struct { /* opaque */ }

type FontFace struct {
    Family string
    Weight int
    Slant  Slant
    Data   []byte
    Index  int
}

func NewFontSet(faces ...FontFace) (*FontSet, error)
```

```go
type LayoutOptions struct {
    Viewport   Viewport
    Typography Typography
    Fonts      *FontSet
}

func (e *Engine) Layout(
    ctx context.Context,
    doc *Document,
    opts LayoutOptions,
) (*Layout, error)
```

```go
func (l *Layout) PageCount() int
func (l *Layout) Page(n int) *Page
func (l *Layout) Locate(anchor AnchorID) (Location, bool)
func (l *Layout) PageOf(loc Location) (int, bool)
func (l *Layout) Selection(from, to Location) []SelectionQuad
func (l *Layout) Relayout(ctx context.Context, opts LayoutOptions) (*Layout, error)
```

```go
type Location struct {
    Chapter int
    Anchor  AnchorID
    Offset  int // rune offset within anchor
}
```

### 6.3 输出

```go
type Page struct { /* immutable, cached display list */ }

func (p *Page) Number() int
func (p *Page) Size() Size
func (p *Page) Draw(painter Painter)
func (p *Page) Links() []Link
func (p *Page) HitTest(pt Point) (Location, bool)
```

```go
type GlyphRun struct {
    Font   *Font
    Size   float32
    Color  Color
    X, Y   float32
    Glyphs []Glyph
}

type Glyph struct {
    ID      uint32
    X, Y    float32
    Advance float32
    Cluster int32
}
```

### 6.4 API 稳定性规则

- `v1` 后不做破坏性变更。
- 公共接口只保留扩展点（`Painter`、`ImageStore`），不随意加方法。
- 新能力优先加具体类型方法或 `Options` 字段。
- 不导出内部 box tree / line box / display list。
- `experimental/` 放不稳定 API。
- CI 用 `apidiff` 检查破坏性变更。
- 不用 `any` 作为公共热路径参数。

---

## 7. 性能设计

### 7.1 微基准

环境：

- AMD Ryzen 7 5800H，8 核 16 线程
- Go 1.27
- `go-text/typesetting` v0.3.5
- Noto Sans CJK SC
- 18px，480px 行宽，约 400 字一段
- 只测 **shaping + 断行**，不含块布局/分页/绘制/解析

| 文本量 | 1 线程 | 4 线程 | 8 线程 | 16 线程 |
|---|---:|---:|---:|---:|
| 100 万字 | 0.97s | 0.27s | 0.16s | 0.10s |
| 500 万字 | 3.9s | 1.2s | 0.7s | 0.5s |
| 1000 万字 | ~7.8s | ~2.4s | ~1.4s | ~1.0s |

结论：断行不是瓶颈，并发扩展性接近线性。

### 7.2 全书布局估算

加上块布局/分页，粗估乘 1.3–1.5：

| 文本量 | 4 线程 | 8 线程 | 16 线程 |
|---|---:|---:|---:|
| 100 万字 | 0.4s | 0.25s | 0.15s |
| 500 万字 | 1.5–1.8s | 0.9–1.1s | 0.65–0.8s |
| 1000 万字 | 3–3.6s | 1.8–2.2s | 1.3–1.6s |

复杂表格、图片、大量样式 run 再乘 1.5–2。  
低端 2 核设备，500 万字约 3–5s。

### 7.3 设置变化成本

| 变化 | 需要重做什么 | 500 万字估算 |
|---|---|---|
| 字号 | 重新 shaping + 断行 + 分页 | 0.5–0.9s（16 线程） |
| 字体 | 重新 shaping + 断行 + 分页 | 同上 |
| 左右页边距/宽度 | 重新断行 | 同上 |
| 字间距 | 重新断行 | 同上 |
| 行高 | 断行不变，只重算 y 和页映射 | 10–30ms |
| 段间距/gap | 断行不变，只重算 y 和页映射 | 10–30ms |
| 上下页边距 | 页高变化，只重分页 | 10–30ms |
| 主题/颜色 | 不重排，只重绘 | <1 帧 |
| 当前章优先重排 | 只排当前章 | 1–10ms |

### 7.4 三层 key

```text
BreakKey = hash(text, font faces, fontSize, letterSpacing, width, lang, hyphenation)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins, widows/orphans)
PaintKey = hash(PageKey, theme)
```

- `BreakKey` 不变 → 复用行盒，只重分页。
- `PageKey` 不变 → 复用页映射，只重绘。
- 只缓存最近 2–4 个变体，不做全组合预计算。

### 7.5 并发模型

- 按章并行；大章内部按段落并行。
- 每 worker 独立 `HarfbuzzShaper` / `LineWrapper`。
- 分页/页号合并顺序执行，O(pages)，不是瓶颈。
- 每任务带 generation，设置变化时旧结果丢弃。
- 所有任务响应 `context.Context`；每段检查一次取消。
- 不做隐藏后台 goroutine；需要异步由调用方控制。

### 7.6 内存

- 只存行盒/页映射/锚点，不存全书 shaped glyph。
- 500 万字全书 shaped glyph 约 450 MB，不可接受。
- 500 万字行盒约 3–4 MB；页映射 <1 MB。
- 一章一章流式处理，保留紧凑映射，丢掉正文。
- `PageCache` 限制 DisplayList 数量；位图只缓存可见页。
- 建议 `GOMEMLIMIT=128–256MiB`，worker 数按内存预算收敛。

### 7.7 导入即计算完毕

“计算完毕”定义为：

```text
默认排版下的：
  断行结果
  行盒/块盒尺寸
  章内页映射
  全书页号映射
  锚点/文本偏移映射
```

不是“所有页渲染成位图”。

流程：

```text
按章并行 shaping/断行 -> 行盒
章内块布局 -> 页映射
全书页号 = 各章页数前缀和
持久化 layout cache
```

首章优先，后台继续；用户打开时优先保证当前章。
字号切换时当前章同步重排，全书后台重排。

---

## 8. 体积控制

### 8.1 预算

| 组合 | 目标增量 |
|---|---:|
| `render` 核心 | ≤ 3 MB |
| core + `render/software` | ≤ 4.5 MB |
| parser/adapters | 独立 module，不计入核心 |

以 CI 对最小示例的实际二进制为准，不以“库文件大小”估算。

参考：最小 Go 二进制约 1.2 MB；仅引入 `go-text` shaping 的最小程序约 2.7 MB（stripped），即 `go-text` 约 +1.5 MB。一个现有 Go 桌面阅读器 stripped 约 26 MB，因此核心 + 一个后端控制在 3–4.5 MB 增量是现实目标。

### 8.2 禁止项

- 核心禁止 `encoding/json`、`regexp`、`reflect`、`net/http`、`image/*`。
- 禁止 `init()` 注册全局字体/后端。
- 禁止内置字体、断词词典、emoji 表。
- 禁止无界全局缓存。
- 禁止隐藏 goroutine。
- 重能力（SVG、MathML、软件光栅、XHTML 解析）全部独立包。

### 8.3 CI 闸门

- `go list -deps ./render` 白名单检查。
- 最小示例 `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`，检查二进制大小。
- 超预算直接失败。

---

## 9. 证明：和浏览器 A/B

不能先验断言“自研更快”。必须用目标书实测。

### 9.1 开关

```text
BOOK_RENDERER=webview | native
```

同一本书、同一 viewport、同一字体/字号/页边距。

### 9.2 语料

- 纯文字中文小说。
- 英文小说（连字、断词、两端对齐）。
- 图片/图注多的书。
- 表格/列表/引用。
- 诗歌/短行。
- 竖排/ruby（如果目标包含日文）。
- 格式不规范的 EPUB。
- 大章节。

### 9.3 指标

| 指标 | 说明 |
|---|---|
| 包增量 | 最终二进制/安装包 |
| 冷启动首页 | 启动到第一页可交互，p50/p95 |
| 首页布局 | 收到 RenderDoc 到 DisplayList 完成 |
| 热翻页 | 连续 100 页，p50/p99 |
| 字号重排 | 14→24px，整书完成时间 |
| 行高/gap 重分页 | 整书完成时间 |
| 内存 | RSS；WebView 要算 browser/renderer/GPU 整个进程树 |
| 滚动 | 连续滚动 10s 丢帧、帧 p99 |
| 文本质量 | 断行、禁则、对齐、孤行、图片跨页 |
| 一致性 | 跨平台页数/断行是否一致 |
| 覆盖率 | 无布局错误/崩溃的比例 |

### 9.4 验收门槛（示例）

- 包增量 ≤ 2–3 MB。
- 冷启动首页 ≤ WebView 的 50%。
- RSS ≤ WebView 的 60%。
- 字号重排 500 万字 ≤ 1s（8–16 线程）。
- 行高/gap 重分页 ≤ 30ms。
- 热翻页不超过 WebView 的 1.2 倍（浏览器合成很快，不强求赢）。
- 95% 语料无布局错误。
- 同字体同 viewport 跨平台页数/断行一致。

### 9.5 Kill criteria

如果 5 个核心指标里没有 3 个明显赢，就停止自研，保留 WebView。  
浏览器在复杂 CSS、表格、shaping 上很强；自研只在电子书子集上有机会。

---

## 10. 可靠性、安全与限制

- 不 panic；坏 IR 返回 typed error。
- 不打印日志。
- `Limits` 默认有上限，防止超大节点/文本/图片。
- 缺失图片/字体回退不应让整本书失败。
- 引擎不解析不可信文件；但需防御已解析 IR 的资源耗尽。
- 不执行 JS、不加载远程资源。
- `Document`/`FontSet`/`Layout` 生命周期与并发语义写入 GoDoc。
- `Painter` 不要求线程安全；`Page.Draw` 可并发，但每个 Painter 独立。

---

## 11. 测试与 CI

- `go test -race ./...`
- `go vet`、`staticcheck`、`govulncheck`
- `apidiff` 公共 API 兼容性
- `go list -deps` 核心依赖白名单
- 最小示例二进制体积
- `BenchmarkLayout5M`、`BenchmarkRelayoutGap`、`BenchmarkPageDraw`
- golden layout：页数、行断点、行盒位置
- fuzz `Document` 校验
- 不同 `Workers` 数量下结果完全一致
- 真实语料回归（不含解析器）

---

## 12. 里程碑

| 阶段 | 交付 | 验收 |
|---|---|---|
| **M0** | 单章 spike：文本 + 图片 + 基础分页 | 5M 字微基准、首屏 <20ms、内存可控 |
| **M1** | 核心布局 + 分页 + `Painter` | golden 测试；A/B 首批指标 |
| **M2** | 滚动 + tile 缓存 | 滚动帧 p99 不高于 WebView |
| **M3** | BreakKey/PageKey 缓存 + 快速重排 | 行高/gap <30ms；字号首屏 <20ms |
| **M4** | 可选后端/适配器拆包 | 核心依赖白名单 + 体积闸门 |
| **M5** | API v1 冻结 | apidiff、文档、示例、迁移指南 |

---

## 13. 待确认

1. 目标平台：桌面端，还是 Android/e-ink/iOS 都要？
2. 体积上限：core ≤ 2 MB 还是 3 MB？
3. 输入 IR 是否由现有 parser/编译器直接产出？
4. 字体来源：系统字体、书内嵌字体，还是两者？
5. fixed-layout/脚本化 EPUB 是否确认走 WebView fallback？
6. 目标语料：纯小说为主，还是包含大量表格/竖排/ruby？

---

## 14. 决策记录

| 日期 | 决策 | 原因 |
|---|---|---|
| 本次 | 引擎不解析 EPUB/HTML/CSS | 解析不属于渲染；核心体积和依赖可控 |
| 本次 | 纯 Go，无 cgo | 复用宿主 Go 运行时；体积和构建简单 |
| 本次 | 不内置光栅器 | 体积最小；宿主 Painter 决定 GPU/软件/e-ink |
| 本次 | 分页/滚动共享 FlowLayout | 位置、锚点、字号变化不分裂 |
| 本次 | 只预计算行盒/页映射，不预渲染位图 | 整书位图内存不可接受 |
| 本次 | 用 go-text 做 shaping/断行 | 纯 Go、可并发、实测扩展性好 |
| 本次 | 行高/gap 只重分页，字号才重断行 | 大幅降低设置切换成本 |
| 本次 | 先 A/B 证明，再全面自研 | 浏览器在复杂版式上仍强 |
