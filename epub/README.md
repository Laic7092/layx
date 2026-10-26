# layx/epub

EPUB 解析层，独立 Go module：

```
module github.com/laix/layx/epub
```

它把一个 EPUB 文件读成章节，再把每章的 XHTML 转成
[`github.com/laix/layx/render`](../render) 的 `render.Document`。

## 边界

做：

- EPUB 容器：`META-INF/container.xml` → OPF
- OPF：manifest、spine 顺序、`linear="no"` 跳过、metadata（title / language）
- 目录：EPUB 3 nav 文档，回落到 EPUB 2 NCX；保留顺序、嵌套层级和
  `#fragment`，每个可达目标都分到一个 `render.AnchorID`（`Book.Toc`）
- 章内锚点：`id` 属性和 `<a name>` 映射到导航/链接目标的锚点，供
  `Layout.Locate` 跳转；目标元素不存在时回退到章首，不留死行
- 链接样式：UA 样式表给 `a` 上 `Options.LinkColor` 的颜色和下划线（默认浏览器蓝），
  斜体/粗体仍从外层继承；作者 CSS 的 `a { color: …; text-decoration: none }` 可以覆盖。
  `Options.ThemeLinks` 则只留下划线、不写颜色，链接色由
  `render.Typography.LinkColor` 绘制，换主题不用重建文档
- 链接：`<a href>` 变成第一个可见节点的 `render.Node.Link`，外部 URL 原样
  保留，书内目标解析成归档路径 + fragment，可用 `Book.ResolveLink` 变成
  `(Chapter, render.AnchorID)`；`render` 的 `Page.Links()` 已经带命中矩形
- 封面发现：`cover-image` 属性 → `<meta name="cover">` → guide `cover`；
  guide 指向包装页时取页里的第一张图
- 书架用的 `ReadMetadata`：只读 container.xml、OPF 和封面，一个 spine 页都不打开；
  不是 zip 的文件返回 `ErrNotArchive`，没有封面不算错
- 去掉“只有封面图”的 spine 页，把它交给 UI 单独显示
- XHTML → `render.Document`：段落、标题 h1–h6、引用、列表（真实标记与嵌套计数）、
  `pre` 空白、`hr` 横线、定义列表、`em`/`i`、`strong`/`b`、`br`、`img`、
  内联 SVG 里的图片、空章节占位文本
- CSS 子集：`<link rel="stylesheet">`、`<style>`、`style=""` 三处都加载并级联，
  `@import` 跟两层，同一张样式表一次 Build 只解析一次；选择器、at-rule、单位与
  属性清单见下文“CSS 子集”
- 表格线性化：`caption`、表头加粗、一行一个块、单元格之间用 ` ｜ ` 分隔，
  行尾不留空行；不伪装成网格，没有列宽和跨行跨列
- 图片资源：按 manifest 分配稳定的 `render.ImageID`，`Build` 时读取字节

不做（本 module 之外或下一阶段）。每条都标了出处，是从文档抄的而不是猜的：

- 布局、断行、分页、光栅、字体加载 —— `layx/docs/design.md`“明确不做”表；
  `layx/README.md`“不解析 EPUB/XHTML/CSS，不内置光栅器”。
- CSS 里需要布局模型的部分：浮动 / 定位 / 多栏 / flex / grid、`width`/`height`、
  `background*`、`@font-face`、伪元素 `content`。出处是
  [`layx-reader/docs/css-census.md`](../../layx-reader/docs/css-census.md) 的
  “明确不做”：这些在 12 本语料书里要么 0 次，要么与主题/IR 冲突。`@media` 按
  无条件处理，`!important` 支持。
- 链接的点击 UI：本 module 只给出 `Node.Link`、`ResolveLink` 和（经 render 的）
  `Page.Links()` / `Tile.Links()` 命中矩形；把矩形变成点击、在外部浏览器打开
  URL，是 reader 的事（`layx-reader/links.go` 已经接上）。`<area>` 和没有可见
  内容的 `<a>` 仍然不产生节点；一个 `<a>` 包住多个块时，`Link` 落在第一个可见
  节点上，命中矩形不覆盖后续块。
- 真正的表格网格：列宽、`rowspan`/`colspan`、表头跨页重复、列对齐。
  `render` 的 IR 只有 block/inline/text/image/break，没有网格模型，
  `layx/docs/design.md` 的「会破坏当前结构的能力」一节明确说表格要先扩 IR（“列组、跨行列，
  本质是网格 + 约束求解”）。本 module 只做线性化，不谎报结构；
  `colgroup`/`col`、`colspan` 被忽略但单元格文本保留。
- ruby（`<ruby>`/`<rt>`）、竖排、脚注。出处：`layx/docs/design.md`「会破坏当前结构的能力」的
  ruby/竖排一条、`layx/README.md` 已知限制、`layx-reader/README.md`“没有脚注、竖排、ruby”。
- MathML、音频 / 视频、字体文件、DRM：没有专门的文档清单，是从代码行为
  推的——`html.go` 的 `skippedTags` 含 `math`，`book.go` 只收
  `media-type` 以 `image/` 开头的资源，manifest 里的字体/音视频不会进
  `Document`。
- 目录的分页与选中：`Book.Toc` 已经给全顺序、层级和 anchor，`Locate` 负责
  变成页或滚动位置；哪一行算“正在读”、翻页后的页码、面板怎么画，都是宿主的
  事（`layx-reader/internal/toc` 已经接上）。
- TXT 等纯文本格式：这里只有 EPUB 路径（本次迁移的明确要求；
  `layx-reader` 的 TXT 解析仍在 `internal/book/txt.go`）。

## XHTML 映射清单

这是 `layx-reader/docs/integration-notes.md` 第 8 条要求的那张表：哪些标签
被映射、哪些被有意丢掉。未列出的标签当作透明容器，只处理子节点。

| XHTML | 结果 |
|---|---|
| `h1`–`h6` | 块，UA 字号 1.6/1.4/1.25/1.1/1/0.95em、加粗、段前后 0.6em、`KeepTogether`；CSS 可覆盖 |
| `p` | 块，UA 首行缩进 2em、段后 0.6em |
| `blockquote` | 块，UA 左缩进 2em、段后 0.6em |
| `ul` / `ol` / `li` | `li` 是块，真实标记：有序列表按嵌套计数（`1. `/`a. `/`I. `），无序列表 `•`/`◦`/`▪`；`list-style-type: none` 不画 |
| `dl` / `dt` / `dd` | 块；UA 给 `dt` 加粗、`dd` 左缩进 2em |
| `pre` | 块，保留空格和制表符，换行变 `NodeBreak`；`<pre>` 后的第一个换行按 HTML 规则丢弃 |
| `div` / `section` / `article` / `header` / `footer` / `aside` / `nav` / `main` / `figure` / `figcaption` | 块，没有额外样式 |
| `hr` | 空块 + 1px 下边框（`Options.RuleColor`），段前后 0.8em；CSS 可改 |
| `table` | 块；子节点按下表线性化 |
| `caption` | 块，UA 0.9em、斜体、段后 0.3em |
| `thead` / `tbody` / `tfoot` / 表格内的 `div` 等包装 | 透明，只处理子节点 |
| `colgroup` / `col` | 忽略（没有网格可言） |
| `tr` | 块，UA 段后 0.3em、`KeepTogether`，一行不跨页 |
| `td` | 内容直接进所在行块，多个单元格之间自动插 ` ｜ ` |
| `th` | 同上，内容包在加粗行内（含块级内容时用块）；加粗来自 UA `th { font-weight: bold }` |
| `em` / `i` | 行内，UA 斜体 |
| `strong` / `b` | 行内，UA 加粗 |
| `br` | `render.NodeBreak` |
| `img` | `render.NodeImage`；只用 `src`/`width`/`height` |
| `svg` | 只取其中第一个 `image`/`img` 的 `xlink:href`/`href`/`src` 变成图片，纯矢量内容丢弃 |
| `head` / `title` / `script` / `meta` / `base` / `math` | 连同子树一起丢弃 |
| `style` / `link` | 不产生节点；内容作为 CSS 加载（见“CSS 子集”） |
| `a` | 行内包装，UA 颜色 + 下划线；`href` 写到内部第一个 text/image 叶子（`render` 的命中矩形来自这些 token） |
| 其他（`span`、`sup`/`sub`、`ruby`/`rt`…） | 透明容器；`sup`/`sub` 只是普通字符（没有基线位移），ruby 注解按行内文字输出 |

属性方面读 `img` 的 `src`/`width`/`height`、`a` 的 `href`、`svg` 的 `viewBox`
和内层 `image` 的尺寸/href、导航/链接目标元素的 `id`（`<a name>` 也算）、
`class` 与 `style`（级联）、`rel`（样式表链接）、`epub:type`（目录的 `nav`）。
`lang`/`dir` 仍然忽略。空白按 HTML 规则折叠，`<pre>` 和 `white-space: pre` 除外。

## CSS 子集

语料依据是 [`layx-reader/docs/css-census.md`](../../layx-reader/docs/css-census.md)：
12 本 Project Gutenberg 书、2391 条声明、38 个属性。第一批子集按那份账本的
“做这 11 项”落地，顺序和取舍都以语料为准，而不是照 CSS 规范铺开。

来源与权重：内置 UA 样式表（`cascade.go` 的 `uaCSS`）< 作者样式表（按文档顺序）
< `style=""`；`!important` 优先于普通声明。`@media` / `@supports` 按无条件处理
（语料里的 11 条 `@media` 只分 screen/print，没有宽度查询）；`@charset`、`@page`、
`@font-face` 跳过。相对路径先按 `<link>` 所在文档解析。

选择器：标签、`.class`（可多个）、`#id`、`[attr]`（存在 / `=` / `~=` / `^=` /
`$=` / `*=` / `|=`）、后代、`>`、`+`、`~`、`:link` / `:visited` /
`:first-child` / `:last-child`。伪元素、`:hover`、`:not()` 等静态书里不成立的
规则整条丢弃，避免“半条规则生效”。

| 组 | 属性 |
|---|---|
| 字体 | `font-size`（`%`/`em` 相对父级，另有关键字表）、`font-weight`、`font-style`、`font-family`（取第一个） |
| 行与字距 | `line-height`（无单位与 `em` 当倍数）、`letter-spacing`、`word-spacing`、`hyphens` |
| 段 | `text-align`、`text-indent`（可负）、`color` |
| 盒 | `margin*`、`padding*`（折算进 margin，IR 没有 padding）、`border*`（宽度 + 颜色，线型一律实线） |
| 分页 | `page-break-before/after`、`break-before/after`、`page-break-inside`、`break-inside`、`widows`、`orphans` |
| 结构 | `display`（`none` / `block` / `inline` / `list-item` / `table`）、`list-style(-type)`、`white-space`（`pre` 族） |

单位：`px`、`em`、`rem`、`%`、`pt`/`pc`/`in`/`cm`/`mm`/`q`；`vw`/`vh` 语料里 0 次，
按 0 处理。百分比边距/缩进需要正文栏宽，由 `Options.ContainerWidth` 提供；为 0
时按 0（不猜）。`auto` 边距按 0。

颜色：常见命名色、`#rgb`/`#rrggbb`/`#rrggbbaa`、`rgb()`/`rgba()`、`transparent`；
`currentColor` 和 `initial` 当“宿主墨色”（零值）。明确忽略：`background*`、
`width`/`height`/`max-width`、`float`/`position`/`columns`/`flex`/`grid`、
`vertical-align`、`font-variant`、`content`、`visibility`、`clear`、`all`——
依据见 census 的“明确不做”表。

宿主开关（`Options`）：

- `LinkColor`：UA 里 `a` 的颜色与下划线颜色；作者 CSS 可覆盖。
- `ThemeLinks`：不把链接色写进文档，交给 `render.Typography.LinkColor`；
  阅读器自己的纸色变了只重绘、不重建文档。
- `RuleColor`：`<hr>` 和没写颜色的边框；默认中性灰。
- `IgnoreColor`：丢掉作者声明的 `color`，让主题决定墨色；UA 的链接色不受影响。
- `ContainerWidth`：解析 `%` 长度的栏宽。

已知上限：`render.Style` 的零值表示“继承”，所以 `font-style: normal` 无法取消
父级的斜体；`text-decoration` 在 CSS 里本来就不能被后代取消，这里同样如此。
`font-variant: small-caps`、`text-transform`、真正的 `white-space: nowrap`
没有对应字段。

## 性能基准

`bench_test.go` 把成本拆成两段：本 module 的 Open/Build，和 `render` 的冷布局。

```
go test -run '^$' -bench 'Open|Build' -benchtime=10x
go test -run '^$' -bench Layout -benchtime=2x
```

`Layout` 那组需要系统 CJK 字体（`wqy-*`），没有就 skip；它量的是 `render` 的
shaping/断行，不是本 module 的工作。滚动模式的每帧成本在 `render`/宿主画家，
`layx-reader/docs/scroll-performance.md` 有完整测量（tile 冷建 0.19–0.26 ms、
缓存命中 6 ns、整帧 3.15 ms，瓶颈在 painter）。

2026-07 本机（Ryzen 7 7800X3D，Go 1.27，17px、620×700）参考值：`aq-zhuan.epub`
Open 1.4 ms + Build 2.0 ms；冷 scroll 布局 10.8 ms（paged 11.0 ms）。500 章、
57 万字合成书 Open 13.8 ms + Build 24 ms。Open 里约有一次额外章节遍历用于
收集链接目标（`prepareLinks`），aq 上约 0.5 ms。Build 比没有 CSS 时多出的部分
是：每章一次样式表引用扫描、链接样式表的解析（同一路径一次 Build 只解析一次）、
UA 样式表一次 Build 解析一次、每个元素的级联与样式去重；没有作者 CSS 的章节走
UA 快路径，不进选择器匹配和排序。布局/滚动每帧的路径没有变化。

## 和 layx/render 的关系

依赖方向是单向的：

```
layx/epub  ──imports──▶  layx/render
```

`render` 不 import `epub`，排版核心不知道 EPUB 存在。`epub` 只负责把书
变成 `*render.Document` 加上 `map[render.ImageID][]byte`，调用方把
Document 交给 `render.Engine`，把图片字节交给自己的 painter。

```go
b, err := epub.Open("book.epub")
if err != nil { ... }

doc, images, err := b.Build(18) // 基准字号 18 CSS px
// doc   → render.Engine.Layout
// images→ paint.SetImage(id, data)
```

`Book` 上的公开面：

- `Open(path) (*Book, error)`
- `Book.Title`、`Book.Language`、`Book.Chapters`
- `Book.Cover`、`Book.HasCover`
- `Book.Toc []TOCEntry`：目录顺序、层级、目标章节和锚点
- `Book.ResolveLink(url) (LinkTarget, bool)`：`Node.Link` 里的书内目标 →
  `LinkTarget{Chapter, Anchor}`；外部 URL 返回 false
- `Book.Build(fontSize float32) (*render.Document, map[render.ImageID][]byte, error)`
- `Book.Document()` / `Book.DocumentSized(base)`：只取 Document 的便捷方法

`TOCEntry` 的字段：

```go
type TOCEntry struct {
    Title   string
    Depth   int             // 0 = 顶层，嵌套目录不再靠标题猜
    Href    string          // 归档路径 + 可选的 #fragment
    Chapter int             // Chapters 下标；-1 = 封面/linear="no" 等不可达目标
    Anchor  render.AnchorID // 交给 Layout.Locate 的锚点
}
```

锚点规则：章首用 `chapterIndex+1`（reader 现在写的 `AnchorID(i+1)` 继续有效），
fragment 锚点从 `len(Chapters)+1` 开始，在 `Book` 生命周期内稳定。目标元素
不在 XHTML 里时，锚点回退到章首，所以每一行都可点，不会出现 disabled 行。

目录跳转只需要 `render` 已有的能力：

```go
for _, e := range b.Toc {
    if e.Chapter < 0 { /* 不可达目标，显示但不跳 */ }
    loc, ok := layout.Locate(e.Anchor) // 页模式用 layout.PageOf(loc)
    _ = ok
}
```

正文链接同理。`Page.Links()` 给出矩形和 `URL`，书内目标交给
`ResolveLink`，外部 URL 交给宿主自己的打开器：

```go
for _, l := range page.Links() {
    if t, ok := b.ResolveLink(l.URL); ok {
        loc, _ := layout.Locate(t.Anchor) // 跳到 t.Chapter 的锚点
        _ = loc
    } else {
        // https://、mailto: 等，或者断链
    }
}
```

图片 ID 在 `Book` 的生命周期内稳定，字节在每次 `Build` 时重新读取；同一个
ID 在一次 build 里只会读一次。`Build` 只返回编码后的字节，媒体类型和像素
尺寸不单独返回：尺寸已经写进 `render.NodeImage` 的 `Width`/`Height`，
painter 自己解码时也能拿到。

## 在别的 module 里使用

本 module 用 `replace` 指向仓库根目录的 `layx`（`render` 在那里）：

```
require github.com/laix/layx/epub v0.0.0
replace github.com/laix/layx/epub => ../layx/epub
```

`epub/go.mod` 里相应的行是：

```
require github.com/laix/layx v0.0.0
replace github.com/laix/layx => ../
```

按自己仓库的相对位置改写 `replace` 即可；没有发布 tag 之前，`v0.0.0`
占位就够了。`epub` 是 nested module，不参与 `layx` 根目录 `go test ./...`
的构建。

## 测试

```
cd epub
go mod tidy
go build ./...
go test ./...
```

`testdata/mini.epub` 覆盖 nav 标题、metadata、两章正文和 h1 缩放；
`testdata/samples/aq-zhuan.epub` 是真实的 EPUB 2 样书（PNG 封面页 +
Gutenberg 分块 HTML + NCX），`TestSamplesOpen` 会把 `testdata/samples`
下的每个 `.epub` 打开并 Build 一遍。

mini.epub 的 `<h1>` 与 nav 文字相同，所以它证明不了“nav 优先”；
`TestNavTitlesWinOverHeadings` 和 `TestNCXTitlesFromSpineToc` 现场生成
标题不同的书，分别覆盖 nav 和 spine `toc` → NCX 两条取标题的路径。

`TestTableLinearizedIntoRows` 现场生成一张带 `caption`、`thead`、`colspan`、
以及单元格内嵌段落的表，断言行数、caption、表头加粗和“苹果 ｜ 3”这种
单元格分隔，并用 `DocumentBuilder` 验证嵌套块能过校验。

`css_test.go` 覆盖 CSS 层：`@import`/`@media`/注释的解析、标签/class/id/后代/
子代/相邻/属性/伪类选择器、`style=""` 的优先级、`em`/`%` 相对父级与栏宽、
`display:none` 与 `display:block`、`hr` 边框、`pre` 空白、有序/无序列表标记、
`IgnoreColor` 与无栏宽时的百分比。

目录相关：`TestTOCFragmentsBecomeAnchors` 生成一个文件里带两个 `id` 的章节
和嵌套 nav，断言 5 行目录、真实 Depth、fragment 各自拿到不同锚点、缺失的
fragment 回退章首；`TestLinksPreservedAndResolvable` 断言跨章链接、同章
fragment、外部 URL 和断链四种 `Node.Link` 都保留，且前两种能经
`ResolveLink` 落到具体节点；`TestTOCOnRealSample` 拿 `aq-zhuan.epub` 断言 7 条 NCX
目录（旧实现只剩 2 条章级标题）、6 个 fragment 锚点，且每个锚点都出现在
`Document` 的对应章里。
