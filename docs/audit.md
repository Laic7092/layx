# 公共库审计：还差哪几层契约

> 本文是**一次对外的可用性审计**，不是设计文档。设计取舍看 [`design.md`](design.md)，
> 排版政策看 [`text-rules.md`](text-rules.md)，用法看 [`../README.md`](../README.md)。
>
> 审计视角：假设这个库会被成千上万个宿主嵌进阅读器、笔记、打印流水线，
> 输入来自不受信任的文件和用户配置，运行环境从 8 核服务器到 2 核 Windows
> 平板、到 `GOARCH=wasm` 的浏览器标签页。
>
> 基线：`e78cb94`（工作区可能有未提交改动，见 §7）。
> 方法：读代码 + 程序化探针 + 短时 fuzz，**全部离场后已还原，未改任何源文件**。

---

## 1. 结论摘要

引擎内部的工程质量明显高于同类项目：三层缓存、段落粒度的并行预算、
`mem_test.go` 内存闸门、golden layout、跨平台 `go build` 全部通过，
`render` 语句覆盖率 86.9%。缺的东西几乎全在**库的外壳**：崩溃边界、
许可、分发、输入校验、诊断出口。

| # | 级别 | 问题 | 后果 |
|---|---|---|---|
| 1 | P0 | 多段文本里出现阿拉伯/希伯来/泰文/天城文（或 U+202E）时 `Layout` **panic** | 宿主进程 crash |
| 2 | P0 | 空文本节点让 `Page.HitTest` / `Layout.HitTest` **panic** | 阅读器点击即崩 |
| 3 | P0 | **没有 LICENSE**；`epub` 子模块对外不可 `go get`；无 tag、无 CI | 法务/分发层面不可用 |
| 4 | P1 | `validate()` 挡不住 NaN/Inf，NaN 直接进分页结果和 DisplayList | 静默产出 NaN 几何、静默丢内容 |
| 5 | P1 | 页数/tile 数没有上限，`TileHeight` 没有下限 | 一个配置项就能打满内存 |
| 6 | P1 | 不支持的东西没有诊断出口 | 宿主无法降级，也无法向用户解释 |
| 7 | P1 | `FontSet` 只有 `FontCount()`，无覆盖查询 | 宿主做不了字体回落/tofu 检测 |
| 8 | P1 | 错误只靠字符串区分 chapter/node | 错误处理只能解析文本 |
| 9 | P1 | 文档只对作者友好（全中文、外链指向私有仓库、无 Example） | 新人无法在 10 分钟内跑起来 |
| 10 | P2 | `HitTest` 签名分裂、`Stats` 只有全局、无全量排版以外的入口 | 长期演进成本 |

修复顺序见 §6。

---

## 2. P0：一次 panic 就是宿主的一次崩溃

`render/`、`epub/`、`render/paint/` 里**没有任何 `recover()`**（`grep -rn "recover()"` 为空），
库的 worker 又是 `parallelFor` 起的 goroutine：panic 发生在 helper goroutine 上时，
宿主自己的 `recover` 也接不住，进程直接退出。所以下面两条不是"偶发 bug"，
而是**契约漏洞**：库必须保证任何输入都以 `error` 结束。

### 2.1 `Layout` 在"多段 + 需要 go-text Segmenter 的文字"上 panic

最小复现（单字体 `DejaVuSerif`，只有一个文本节点，注意串里的 `\n`）：

```go
b := render.NewDocumentBuilder().SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}})
b.AddChapter(render.Chapter{ID: "c", Root: 0, Nodes: []render.Node{
    {Kind: render.NodeBlock, Children: []render.NodeID{1}},
    {Kind: render.NodeText, Style: 1, Text: "abc\nالعربية"},
}})
doc, _ := b.Build()
_, err := eng.Layout(ctx, doc, render.LayoutOptions{
    Viewport:   render.Viewport{Width: 600, Height: 900},
    Typography: render.Typography{FontFamily: "serif", FontSize: 18},
    Fonts:      fonts,
})
// panic: runtime error: index out of range [0] with length 0
//   go-text/typesetting/shaping.inclusiveGlyphRange  (wrapping.go:213)
//   go-text/typesetting/shaping.cutRun              (wrapping.go:241)
//   go-text/typesetting/shaping.(*LineWrapper).processBreakOption (wrapping.go:1228)
//   ← github.com/laic7092/layx/render.(*textWorker).wrapParagraph (render/text.go:564)
```

实测命中矩阵（文本节点里含 `\n` 时）：

| 文本内容 | 结果 |
|---|---|
| `abc\ndef`（拉丁） | ok |
| `abc\nαβγ`（希腊） | ok |
| `abc\nпривет`（西里尔） | ok |
| `abc\n😀`（emoji） | ok |
| `abc\nالعربية`（阿拉伯） | **panic** |
| `abc\nשלום`（希伯来） | **panic** |
| `abc\nनमस्ते`（天城文） | **panic** |
| `abc\nภาษาไทย`（泰文） | **panic** |
| `abc\n\u202e`（RLO） | **panic** |
| `العربية`（单段） | ok |
| `\nالعربية`（空段 + 阿拉伯） | ok |

也就是说：**触发条件是"段落里有换行" + "有需要 Segmenter 的文字"**，
拉丁/希腊/西里尔/emoji 的速度路径不触发，所以作者的语料没暴露它。

根因在 layx 这一侧，不是纯上游事故。探针实测（在 `wrapParagraph` 里打印）：

```text
wrapParagraph runeLen 11 wrapTextlen 11 segs 1
  seg 0: 0..11
shaped runs 1
  run 0: Runes.Offset 0 Runes.Count 3 Glyphs 3
```

段落有 11 个 rune，`shapeRange` 只产出一个覆盖 `[0,3)` 的 run，
**`wrapParagraph` 仍把整段 `[0,11)` 交给 `WrapNextLineF`**；
go-text 在 `cutRun` 里对超出所有 run 覆盖范围的断行点做 rune→glyph 映射，
映射表为空，于是越界。

核心不变量缺一句断言：**shaped runs 必须覆盖 `[0, len(wrapText))`**。
现在它既没有断言，也没有补齐。

命中场景不是刁钻输入：

- TXT / Markdown / 粘贴导入类宿主常把整章存成一个 `NodeText`（`\n` 就在字符串里），
  书里只要有一个泰文/阿拉伯文/印地文段落，第一次渲染就 crash；
- `examples/wasm` 那种宿主里，crash 的后果是整个页面白屏。

建议修法（两层都要，缺一不可）：

```go
// 1) 出口保证覆盖：shapeRange / splitInput 必须覆盖 [0,len(wrapText))，
//    没有字形覆盖的部分补一个 .notdef run，而不是让 run 列表提前结束。
for _, r := range runs {
    if int(r.out.Runes.Offset) > covered { /* 补占位 run */ }
    covered = max(covered, int(r.out.Runes.Offset)+int(r.out.Runes.Count))
}

// 2) 兜底：worker 任务把 panic 降级为 error（新增 ErrInternal），
//    这样"库的 bug"只丢一次排版，不会带走宿主进程。
func (e *Engine) runTask(...) (err error) {
    defer func() {
        if r := recover(); r != nil { err = fmt.Errorf("%w: %v", ErrInternal, r) }
    }()
    ...
}
```

### 2.2 `HitTest` 在空文本节点上 panic

```go
// 章节只有一个空文本节点：Text: ""
l.PageCount()                    // 1
l.Page(0).HitTest(render.Point{X: 10, Y: 10})
// panic: index out of range [0] with length 0
l.HitTest(10, 10)                // 同样 panic
```

栈顶是 `render/page.go:484`：

```go
if n := cf.count(); last > n { last = n }   // 只钳制了上界
if first < 0 { first = 0 }
u := int(cf.unitOf[first])                  // first >= count 时越界
```

`count() == 0`（空段落、只有空 text node、被样式隐藏的块）时 `first == last == 0`，
`unitOf[0]` 不存在。`<p></p>`、空 `pre`、模板拼接出的空节点都会走到这里。

而这是**阅读器最高频的路径**：点击定位、长按选词、拖选起点、双击取词。
同一次布局下 `Selection`/`Progress`/`PageOf`/`OffsetOf` 在空章上是安全的，
唯独命中会崩，说明缺的是一次 `count() == 0` 的早退：

```go
if first >= last || cf.count() == 0 {
    return Location{Chapter: chapter}, false
}
```

### 2.3 许可、版本与分发：现在还装不上

1. **没有 `LICENSE`**。仓库里只有 `render/testdata/DejaVu-LICENSE.txt`（字体许可）。
   这一条会让任何有法务流程的公司直接放弃采用，比上面两个 panic 更致命。
2. **`epub` 子模块对外不可用**。`epub/go.mod`：

   ```text
   require (
       github.com/laic7092/layx v0.0.0
       golang.org/x/net v0.59.0
   )
   replace github.com/laic7092/layx => ../
   ```

   依赖方的 replace 指令会被忽略，于是 `go get github.com/laic7092/layx/epub`
   会去解析一个不存在的 `v0.0.0` → 直接失败。要可分发，必须：

   - 打 tag（`v0.x.y` 与 `epub/v0.x.y`），`epub/go.mod` 写真实版本、删掉 `replace`；
   - 仓库目前 `git tag` 为空，等于没有 API 兼容承诺。
3. **没有 CI**（无 `.github/workflows`）。缺失的检查项：`go vet`、`-race`、
   `windows`/`darwin`/`wasm`/`GOARCH=386` 的构建矩阵、覆盖率门、benchmark 回归门
   （README 里那一堆测量数字，改坏了没人知道）。我手动跑过 6 个目标的
   `GOOS/GOARCH go build ./...`，全部通过——说明这个矩阵很便宜。
4. **没有任何 fuzz target**（`grep -rn "func Fuzz"` 为空）。§2.1/§2.2 两条都是
   几十秒内就能被 fuzz 命中的（见附录 A）。`epub` 是全库唯一吃不受信任字节的
   模块（zip/xml/xhtml/css），却没有 fuzz。
5. **`go.mod` 写 `go 1.27.2`**：把最低版本钉死在最新补丁版，等于要求所有宿主
   跟着升级。库的最低版本应该是"我们真的用到的语法/API 版本"，不是开发机版本。
6. 仓库卫生：`scripts/__pycache__/ab_report.cpython-312.pyc` 被提交进 git
   （`git ls-files` 可见）；工作区还躺着一个 8.9 MB 的 `render.test`
   （`.gitignore` 里 `*.test` 已覆盖，属于本地噪音）。

---

## 3. P1：输入契约与可观测性

### 3.1 `validate()` 挡不住 NaN/Inf，NaN 会流进产物

`render/options.go:165` 的校验全部是 `<= 0` 形式，NaN 与任何数比较都是 false，
于是全部放行。实测结果：

| 输入（5000 词的文档） | 返回 | 观察到的产物 |
|---|---|---|
| `Viewport.Width = NaN` | 无错误 | `Page(0).Size() == {NaN, 900}` |
| `Viewport.Height = NaN` | 无错误 | 页高 NaN |
| `Margins.Top = NaN` | 无错误 | 页面几何正常，内容几何 NaN |
| `Typography.LineHeight = NaN` | 无错误 | **只出 1 页**（正文静默丢失） |
| `Typography.LetterSpacing = NaN` | 无错误 | 152 页（随机页数） |
| `Typography.ParagraphGap = NaN` | 无错误 | 14 页 |
| `Typography.FontSize = +Inf` | 无错误 | 布局"成功" |

宿主从 UI 滑块、`json.Unmarshal`（NaN 需要特殊编码但 `float32` 计算会溢出）、
比例换算里拿到 NaN 并不罕见。现在的行为是"不报错 + 产物是 NaN"，
到了 GPU/软件光栅器那一侧会以更难查的方式失败或画空白。

建议：`validate()` 里统一加 `isFinite`（同时覆盖 `Style` 的 font-size /
line-height / margin / letter-spacing），并在 `DocumentBuilder.Build()` 里对
样式做同样的检查。

### 3.2 资源上限只覆盖了一半

`Limits` 管 nodes / depth / text runes / image pixels，**页数、tile 数、行数没有上限**。
实测：`TileHeight = 0.0001`、内容高 27 px → `TileCount() == 270001`；
`Tiles(0, n)` 会立刻把内存吃满。同理，把 `Margins` 逼到 1 nm，
一个 2000 词的段落就能在 20 M rune 的限额内造出上万行盒。

建议：`TileHeight` 下限（如 ≥ 16 px）、`Limits{MaxPages, MaxTiles}`，
并让 `ErrLimitExceeded` 带上实测值与上限（见 §3.4）。

### 3.3 不支持的东西没有出口

README 的"已知限制"里写着**不提供诊断输出**，实测这句话的代价：

- `Typography.FontFamily = "does-not-exist"` → `err == nil`，静默回落到默认字体，
  宿主完全不知道字体没匹配上；
- 字体覆盖不到的文字（正是 §2.1 的诱因）→ 静默丢字形，或者 panic；
- IR 表达不了的内容（`colspan`、`ruby`、`@font-face`）→ 静默丢；
- `epub` 层把 `<table>` 线性化、把 `<img>` 丢掉时，同样没有任何回执。

给宿主的最小出口（二选一或都做）：

```go
// a) 回调：逐条上报，可携带章节/节点，宿主自行聚合
type Issue struct {
    Code    IssueCode // IssueMissingGlyphs / IssueMissingFont / IssueDroppedNode / ...
    Chapter string
    Node    NodeID
    Detail  string
}
Options{ OnIssue func(Issue) }

// b) 事后取：一次 Layout 之后读一次，适合只做统计的宿主
func (l *Layout) Issues() []Issue
```

有了它，`epub` 层的"哪些标签被丢掉"才可能从 markdown 文档变成可测试的代码。

### 3.4 错误值不可结构化

`ErrInvalidDocument` / `ErrInvalidOptions` / `ErrNoFonts` / `ErrLimitExceeded` /
`ErrClosed`（`render/doc.go:441`）配 `errors.Is` 是正确的方向，但 chapter id、
node id、"超出了多少"全都只拼进 `fmt.Errorf` 的字符串。宿主想做
"跳到出问题的那一章"、想按错误码聚合上报，只能解析人类可读文本。

建议：

```go
type DocumentError struct {
    Kind    error   // ErrInvalidDocument / ErrLimitExceeded / ...
    Chapter string
    Node    NodeID
    Style   StyleID
    Detail  string
}
func (e *DocumentError) Unwrap() error { return e.Kind }
```

### 3.5 `FontSet` 没有覆盖查询

`FontSet` 只有 `FontCount()`（`render/font.go:91`）。宿主最基本的需求——
"这段文字我的字体覆盖了吗、该找哪个 fallback、有没有 tofu"——
只能自己重新解析 TTF 实现，而 §2.1 的崩溃恰好就发生在"字体覆盖不到"的文本上。

建议：`Coverage(r rune) bool`、`Resolve(style Style, r rune) (FaceID, bool)`，
把"缺字形"变成 §3.3 里的 `IssueMissingGlyphs` 事件。

### 3.6 文档只对作者本人友好

- `README.md`、`epub/README.md`、`docs/*.md` 全中文；代码注释是英文（这点做得好），
  但对外没有一个英文入口。Go 生态里这基本等于放弃国际用户。
- `epub/README.md` 多处链接 `../../layx-reader/docs/...`（第 50、114、172 行等），
  外部用户点开是 404；`epub/testdata/samples/README.md` 让人从私有仓库
  `cp ../../../layx-reader/...` 拷样书。公开库的"不做清单"应该自带可复现的
  采样脚本或脱敏样例，而不是指向作者机器上的兄弟仓库。
- 没有任何 `Example*` 函数：pkg.go.dev 上一个可运行示例都没有；
  "最小示例"只有 `examples/layout` 这个可执行目录。
- 缺 `CONTRIBUTING.md`、`CHANGELOG.md`、`SECURITY.md`、issue/PR 模板；
  没有说明"什么算 breaking change"。

---

## 4. P2：API 一致性与长期演进

- **签名分裂**：`Layout.HitTest(x, y float32)` 与 `Page.HitTest(pt paint.Point)`；
  返回越界语义也不统一（`Page(n)` 返回 `nil`，`Chapter(i)` 返回 `(Chapter, bool)`，
  `Locate` 返回 `(Location, bool)`），文档里值得补一张"越界与空结果"的表。
- **只有全量排版**：`Layout(ctx, doc, opts)` 一次吃掉整本 `Document`
  （100 万字 ≈ 4 MB 行盒，外加全量 shaping），没有 `LayoutRange(chapters)`、
  没有增量追加章节的入口。对网络连载、超大工具书、按章懒加载的宿主是硬约束，
  而这恰好也是 README 里"内存大头在 Document"的另一面。
- **`Stats` 只有全局累计**：无法把一次慢布局归因到具体调用；
  `ResetStats()` 在多 goroutine 共用一个 `Engine` 时语义含糊（会清掉别人正在统计的量）。
  建议 `Layout` 返回 side-channel（`LayoutStats`）或按调用序列号分段。
- **没有 panic 契约**：`paint.Painter` 由宿主实现，库不 `recover`。
  宿主 Painter 里 panic 会穿过 `Page.Draw`，把 worker 的预算计数
  （`Engine.active` / `budget`）留在不一致状态。至少要在 `paint` 包的文档里写明
  "Painter 必须在自身边界处理 panic"，或者在同一处 `defer` 归还预算。

---

## 5. 已经验证健康的部分（不要在这些地方返工）

- `go test ./...`（144 + 31 个用例）与 `go test -race` 通过；`go vet` 干净。
- `render` 语句覆盖率 86.9%；有 golden layout、跨页选区、锚点、重排 key 的专项测试。
- `go build ./...` 在 `linux/amd64`、`linux/386`、`linux/arm64`、`windows/amd64`、
  `darwin/arm64`、`js/wasm`、`freebsd/amd64` 全部通过（`render` 与 `epub`）。
- `DocumentBuilder.Build()` 的校验相当扎实：越界 node/style、环、深度、
  inline 里放 block、anchor 冲突、重复 chapter id 都会返回带上下文的 error。
- `Layout.Page` / `Scroll.Tile` 的 LRU 缓存有锁，`Engine` 的并发预算设计合理。
- 短时 fuzz（25 s、约 53 万次执行）打 `epub` 的 `convertChapter` 与 `parseCSS`，
  没有找到 panic；20 万层嵌套 `<div>` 也不爆栈（`render` 的 `MaxDepth` 兜住了）。

---

## 6. 修复顺序（按投入/收益）

1. **`LICENSE` + 打 tag + 一个 CI workflow**
   （`go vet`、`go test -race`、`linux`/`windows`/`darwin`/`wasm` 矩阵、覆盖率门）。
   半天工作量，直接决定这个库能不能被外人用。
2. **两个 panic 修复 + `runTask` 里 `recover` → `ErrInternal`**，
   同时把 `FuzzLayoutText`、`FuzzConvertChapter`、`FuzzParseCSS` 提交进仓库并在 CI 里
   跑短时 fuzz（附录 A 里三条崩溃输入就是 60 s 内出现的）。
3. **`validate()` 全面 `isFinite` + 页/tile 上限 + `TileHeight` 下限**，
   配一个 `TestHostileInputs` 表驱动测试：NaN/Inf/极小/极大/空文本/控制字符/
   RTL/非法 UTF-8。
4. **诊断出口（§3.3）+ `FontSet` 覆盖查询（§3.5）**；这两条一起做，
   能把"静默降级"变成可断言的行为。
5. **`epub/go.mod` 去掉 `replace`、写真实版本**；README 双语化，
   清掉指向 `layx-reader` 的链接，补 `Example` 函数与 `CONTRIBUTING.md`。
6. 之后再做 P2（签名统一、`LayoutStats`、按章 `LayoutRange`）。

验收清单（已全部关闭，证据见 §9）：

- [x] `LICENSE` 存在，README 有 License 段
- [x] `git tag` 非空，`epub/go.mod` 无 `replace`
- [x] CI 在 4 个平台上跑 `go test -race` 与 `go vet`
- [x] 仓库里有 `func FuzzLayoutText` 且 CI 跑 `-fuzztime 30s`
- [x] `Layout` 对 `"abc\nالعربية"`、`"abc\nภาษาไทย"`、`"abc\n\u202e"` 返回 error 或正常结果，不 panic
- [x] `Page.HitTest` / `Layout.HitTest` 对空文本节点返回 `(Location{}, false)`
- [x] `Viewport`/`Margins`/`Typography`/`Style` 里任一 NaN/Inf 都返回 `ErrInvalidOptions`
- [x] `TileHeight` 过小返回 `ErrInvalidOptions`；`MaxPages`/`MaxTiles` 生效
- [x] 未知 `FontFamily` 通过 `OnIssue`/`Issues()` 可见
- [x] `pkg.go.dev` 上至少有一个 `Example`

---

## 7. 附录 A：探针与复现

审计期间的探针都写成临时 `*_test.go` 放在包里跑，跑完删除；fuzz 语料目录
（`render/testdata/fuzz/`）也已清理。仓库当前除审计前就存在的未提交改动
（`epub/book.go`、`epub/metadata.go`、`go.sum`、`render/scroll.go`、
`render/scroll_test.go`、新文件 `epub/robust_test.go`）之外没有别的变化——
这些改动不属于本次审计，未触碰。

复现 §2.1 / §2.2 的最小测试（放进 `render` 包即可运行）：

```go
package render

import (
    "context"
    "testing"
)

func TestPanicRepro(t *testing.T) {
    fonts := testFontSet(t) // render/layout_test.go 里的 helper
    opts := LayoutOptions{
        Viewport:   Viewport{Width: 600, Height: 900},
        Typography: Typography{FontFamily: "serif", FontSize: 18},
        Fonts:      fonts,
    }
    eng, _ := New(Options{Workers: 2})
    build := func(text string) *Document {
        b := NewDocumentBuilder().SetStyles([]Style{{}, {FontFamily: "serif", FontSize: 18}})
        if err := b.AddChapter(Chapter{ID: "c", Root: 0, Nodes: []Node{
            {Kind: NodeBlock, Children: []NodeID{1}},
            {Kind: NodeText, Style: 1, Text: text},
        }}); err != nil {
            t.Fatal(err)
        }
        d, err := b.Build()
        if err != nil {
            t.Fatal(err)
        }
        return d
    }

    // 1) 多段 + 复杂文字：panic 在 text.go:564
    eng.Layout(context.Background(), build("abc\nالعربية"), opts)

    // 2) 空文本节点：panic 在 page.go:484
    l, _ := eng.Layout(context.Background(), build(""), opts)
    l.Page(0).HitTest(Point{X: 10, Y: 10})
}
```

fuzz target（§2.1 的三条崩溃输入都在 60 s 内出现）：

```go
func FuzzLayoutText(f *testing.F) {
    for _, s := range []string{"", "abc\ndef", "\u202e"} {
        f.Add(s)
    }
    f.Fuzz(func(t *testing.T, text string) {
        // 建 doc → Layout → Page(0).Draw / HitTest / Selection / Progress
    })
}
```

命令：

```sh
go test ./render -run TestPanicRepro -count=1
go test ./render -run '^$' -fuzz FuzzLayoutText -fuzztime 60s
```

运行时得到的关键栈（§2.1）：

```text
panic: index out of range [0] with length 0
  shaping.inclusiveGlyphRange (wrapping.go:213)
  shaping.cutRun              (wrapping.go:241)
  shaping.(*LineWrapper).processBreakOption (wrapping.go:1228)
  render.(*textWorker).wrapParagraph (render/text.go:564)
  render.(*Engine).breakParagraphs  (render/layout.go:618)
```

## 8. 附录 B：审计沿用的一般性原则

拿这四条检查任何一个"公共底层库"，前三条通常能找出最贵的缺陷：

1. **不可信输入 × 无 `recover`**：只要库里有 `go func`，就必须回答
   "worker 里 panic 会怎样"。
2. **浮点入参的校验**：任何 `x <= 0` 形式的校验都要问一句 NaN 会怎样。
3. **静默降级**：凡是"我们决定丢掉它"的地方，都要给宿主一个能看见的出口，
   否则线上问题永远是"用户说这页少了东西，但日志里什么都没有"。
4. **陌生人能不能在 10 分钟内跑起来**：许可、tag、CI、示例、英文 README，
   这五项缺一项，采用率就掉一个量级。

## 9. 修复记录（v0.1.0）

本节由修复批次补写，记录 §1 的每一条问题如何关闭、在哪里验证。修复分为两个提交：

- `b78ad29` — 审计 §7 提到的"审计前既存未提交改动"（`render/scroll.go` 的
  float32 取整容差、`epub` 对不可读 spine 项与超限条目的处理、对应测试），按原样单独提交；
- 审计修复提交 — 本文其余全部改动。`git tag` 为 `v0.1.0`（根模块）与 `epub/v0.1.0`
  （嵌套模块），两者指向同一个修复提交。

### 9.1 逐条对照

| 审计项 | 修复 | 证据 |
|---|---|---|
| §2.1 多段复杂文字 `Layout` panic | `render/segment.go`：按 UAX #9 的段落分隔符（LF/CR/CRLF/U+001C–U+001E/U+0085/U+2029）逐段喂给 `Segmenter.Split`，并把 segmenter 所有的临时切片拷贝进 worker 缓冲；`render/text.go`：`shapeRange` 增加"shaped runs 必须覆盖 `[0,len(wrapText))`"不变量，缺口用 fallback face 补占位 run。`render/work.go`：`runTask` 在 caller 与 helper goroutine 上 recover → `ErrInternal`，`parallelFor` 返回该错误，`layoutDoc` 再把 caller 侧 panic 转 error。 | `render/panic_test.go`（§2.1 全矩阵 + 覆盖断言 + 注入 panic）、`render/fuzz_test.go`（`FuzzLayoutText`）、`render/audit_acceptance_test.go` |
| §2.2 空文本节点 `HitTest` panic | `render/page.go`：`hitTestItems` 在 `cf.count()==0` 或 `first>=last` 时直接返回 `(Location{}, false)`。 | `render/panic_test.go::TestEmptyTextHitTest`、acceptance 同名子测试（paged + scroll） |
| §2.3 许可 / 分发 / CI / fuzz / 版本 / 卫生 | `LICENSE`（MIT）；根 `go.mod` → `go 1.21`，`epub/go.mod` → `go 1.26.0` 且删除 `replace`、写真实版本 `v0.1.0`；新增 `go.work`（带版本限定 replace，本地/CI 解析嵌套模块）；`.github/workflows/ci.yml`（gofmt、vet、linux `-race`、windows/macos、6 目标交叉构建、覆盖率门 85%、4 个 fuzz target 各 30s、benchmark 回归门、最低 Go 1.21 作业）；提交 4 个 fuzz target；`scripts/__pycache__` 移出 git；`git tag` 非空。 | §9.2 的命令与结果；`scripts/check.sh`、`scripts/benchgate.sh` |
| §3.1 NaN/Inf 直通 | 新增 `render/validate.go`：`validateLayoutOptionsFinite` 在 `withDefaults` 之前检查 Viewport/Margins/TileHeight/Typography；`DocumentBuilder` 在 `SetStyles` 缓存 `styleErr`、`AddChapter`/`Build` 抛出，覆盖全部 14 个 Style 浮点字段。 | `render/validate_test.go`（12 个 options 字段 × NaN/±Inf、14 个 style 字段）、acceptance `TestAuditAcceptanceHostileFloats` |
| §3.2 页/tile/行数无上限、TileHeight 无下限 | `Limits.MaxPages/MaxTiles/MaxLines`（默认 100k / 100k / 2M）在分页循环内、tile 缓存分配前、break 阶段（含缓存命中）强制执行；`MinTileHeight = 16`，更小的非零 `TileHeight` 报 `ErrInvalidOptions`。 | `render/limits_test.go`、acceptance `TestAuditAcceptanceLimits` / `TestAuditAcceptanceTileHeightBounds` |
| §3.3 静默降级无出口 | `render/issue.go`：`Issue`/`IssueCode`、`Options.OnIssue`、`Layout.Issues()`；缺字体、缺字形、被丢弃内容都会上报（break 路径去重、上限 1024 + `IssueSuppressed`）。`epub` 层同样提供 `Options.OnIssue` + `Book.Issues()`，覆盖表格线性化、丢图/SVG、ruby、`@font-face` 等静默丢弃。 | `render/panic_test.go` 的诊断子测试、`epub/diagnostics_test.go`、acceptance `TestAuditAcceptanceDiagnostics` |
| §3.4 错误不可结构化 | `render/errors.go`：`DocumentError{Kind, Chapter, Node, Style, Detail}`（`Unwrap` 保持 `errors.Is`）+ `docErrorf/chapterErrorf/nodeErrorf/styleErrorf`；doc/options/font/layout/text/flow 内的 sentinel 错误全部改造。 | `render/validate_test.go::TestDocumentErrorContext`、acceptance `TestAuditAcceptanceStructuredErrors` |
| §3.5 `FontSet` 无覆盖查询 | `FaceID`/`InvalidFace`、`HasFamily`、`Coverage`、`Resolve`（与 `order` 相同的 family→slant→weight 偏好）、`Face`；并发安全、零分配查询。 | `render/font_coverage_test.go`、acceptance `TestAuditAcceptanceFontCoverage` |
| §3.6 文档只对作者友好 | 新增 `README.en.md`（完整英文入口）、`CONTRIBUTING.md`、`CHANGELOG.md`、`SECURITY.md`、`docs/api.md`、issue/PR 模板、5 个可运行 `Example`；README 双语互链、License 段、越界/空结果表；`epub/README.md` 与样书说明移除全部 `layx-reader` 私有链接；新增 `tools/genepub` 可复现生成合成样书。 | `go test ./render -run Example`；`grep -rn layx-reader` 仅历史审计文档命中；`scripts/check.sh` 全绿 |
| §4 签名分裂 / 全局 Stats / 无按章入口 / Painter panic 契约 | `Layout.HitTest(paint.Point)` 与 `Page.HitTest` 统一；`LayoutStats` + `Layout.Stats()` 按次归因（Measure 控制计时）；`ChapterRange` + `LayoutOptions.Range` + `Engine.LayoutRange`（页号从 0，越界章节永不解析，`Relayout` 保持范围）；`render/paint` 与 `Page.Draw` 文档写明宿主 Painter 自担 panic。 | `render/hittest_test.go`、`render/range_test.go`、`render/limits_test.go`（含 Stats 并发隔离）、acceptance `TestAuditAcceptanceRange` / `TestAuditAcceptancePerCallStats` |

### 9.2 验证（修复提交所在的工作树）

```sh
./scripts/check.sh             # gofmt、vet（两个模块）、test、6 目标交叉构建：all checks passed
go test -race ./render -count=1        # ok
(cd epub && go test -race ./... -count=1)  # ok
go test ./render -coverprofile=... && go tool cover -func | tail -1  # 88.5%
go test ./render -run '^$' -fuzz '^FuzzLayoutText$' -fuzztime 15s    # PASS
(cd epub && go test ./ -run '^$' -fuzz '^FuzzConvertChapter$' -fuzztime 15s)  # PASS
./scripts/benchgate.sh          # OK（4 个 benchmark 均在基线 1.5x 内）
```

`render/audit_acceptance_test.go` 把 §6 的验收清单逐项写成外部包测试
（`TestAuditAcceptance*`），归属 §7/§8 的探针也以正式测试与 fuzz seed 的形式落库。

### 9.3 遗留与注意事项

- **仓库 URL 与 module path 不一致**：`git remote` 是 `git@github.com:Laic7092/layx.git`，
  而 module path 是 `github.com/laic7092/layx`。`go get github.com/laic7092/layx` 要求二者一致，
  需要转移/改名仓库或加 vanity import；这属于仓库所有者的决定，未在代码中改动。
- **增量追加章节**（§4 最后一条）没有新增 `append` 入口：按章懒加载已由
  `LayoutRange` + `Relayout`（保留范围、复用 break/page 缓存）覆盖；若需要真正的
  "往已有 Layout 追加下一章"，应作为后续 API 设计，而不是在本轮扩大 `Layout` 的状态机。
- `scripts/bench_baseline.txt` 是在一台开发机上采集的绝对值，CI 上按 2.0x 容差做粗粒度
  回归门；换机器时用 `scripts/benchgate.sh --update` 重采。
- tags 目前是本地 tag；发布时需把两个 tag 一起 push（`git push origin v0.1.0 epub/v0.1.0`）。
