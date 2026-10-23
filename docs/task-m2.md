# 任务 C：M2 —— 滚动模式 + tile 缓存

> 工作树 `/home/laix/layx-m2`，分支 `m2`。
> 参照 `Layx.md` §5.5（滚动）、§7.4（三层 key）、§12 的 M2 行。

## 目标

分页与滚动共享同一份 FlowLayout，只换"取窗口"的方式。核心交付三件事：

1. **滚动模式**：不分页，整本书是一条连续的流，按固定高度的 tile 切片。
2. **tile 缓存**：tile 的 DisplayList 按 `PageKey + tile 序号` 缓存，有界 LRU。
3. **位置稳定**：`y → Location → y` 在换行高/字号后依然落回同一句话。

## 先明确两个边界决定

**1. 核心只产出 DisplayList，不产出位图。**
`render` 禁止 `image/*`，也没有光栅器。所以"tile"在本项目里是
**一段 DisplayList + 内容坐标区间**；位图缓存是宿主（Painter 侧）的事。
文档和注释里要写清楚，避免用户以为 `Tile` 是图片。

**2. tile 高度由宿主给，核心不猜。**
`LayoutOptions.TileHeight`（0 表示取 `Viewport.Height`）。tile 编号从内容区
顶端开始，第 i 个覆盖 `[i*TileHeight, (i+1)*TileHeight)`。

## 语义细节

- 内容坐标：`y=0` 是**整本书正文的顶端**（不是每章顶端）。章与章之间连续
  排版，不重新应用上/下页边距；`Margins.Top` 只在开头出现一次，
  `Margins.Bottom` 只在结尾出现一次。左右边距照常。
- 滚动模式下 `PageCount()` 返回 0、`Page(n)` 返回 nil；分页模式行为完全不变。
- 不引入隐藏 goroutine（原则 6）。预取是同步 API `Tiles(lo, hi)`，由宿主决定
  何时调用、是否放到自己的 worker 里。

## 公共 API（建议形状，命名可调整但要写进 doc 注释）

```go
type Mode uint8
const (
    ModePaged Mode = iota // 默认，行为与现在一致
    ModeScroll
)

// LayoutOptions 新增
//   Mode       Mode
//   TileHeight float32

// Options 新增
//   TileCache int // 每个 Layout 缓存的 tile 数，0 用默认（建议 8）

func (l *Layout) ContentHeight() float32
func (l *Layout) TileCount() int
func (l *Layout) Tile(i int) *Tile         // 首次访问时构建，按 TileCache 缓存
func (l *Layout) Tiles(lo, hi int) []*Tile // 同步预取
func (l *Layout) HitTest(x, y float32) (Location, bool)   // 内容坐标
func (l *Layout) LocationAt(y float32) (Location, bool)   // 位置稳定用
func (l *Layout) OffsetOf(loc Location) (float32, bool)   // 反向
func (t *Tile) Draw(painter paint.Painter)
func (t *Tile) Links() []Link
```

`LocationAt` / `OffsetOf` 必须对滚动和分页两种模式都可用（分页模式下 y 是
页面内的内容坐标需要说清楚，或者两种模式都接受全书内容坐标——选一种并写进
文档，测试覆盖）。

## 性能工作（M2 的主要价值）

`renderPage` 现在对每一行重新 shaping（`buildLine`）。滚动时这成为主要成本。
本阶段要加**有界的行缓存**：

- key：行所属章 + 段落标识 + rune 区间（对齐、可用宽度已包含在 items 里）。
- value：**未做两端对齐的** shaped runs；对齐在取用时应用。
- 有界 LRU（`Options.LineCache`，默认 256），绝不缓存全书。
- 命中时不增加 `Engine.shapes` 计数（现有测试用这个计数证明缓存有效）。

## 允许改的文件

- `render/scroll.go`（新建）
- `render/layout.go`、`render/page.go`、`render/options.go`、`render/cache.go`
- `render/text.go`（仅当行缓存需要暴露内部结构）
- 新增 `render/scroll_test.go`、`render/scroll_bench_test.go`

## 禁止改的文件

`examples/**`（Web 这条线暂时冻结）、`scripts/**`、`tools/**`、`render/paint/**`。
`docs/` 下只能新增本阶段的说明文件。

## 验收

1. `go vet ./...`、`go test ./...`、`go test -race ./render` 全绿；
   现有 golden 期望值不许改，分页模式的页数与位置不许变。
2. **一致性**：同一本书，滚动模式所有 tile 的字形总数 == 分页模式所有页的
   字形总数；且每个字形的内容坐标一致（允许 tile 局部坐标差一个常数）。
   这条是"分页与滚动共享同一套排版"的证据。
3. **位置稳定**：`y → LocationAt → OffsetOf` 往返误差 < 0.01px；
   然后 `Relayout`（换 LineHeight），再 `OffsetOf(同一个 Location)`，
   必须落在同一行的 y 上（允许 ±1 行高）。
4. **tile 边界**：段落跨 tile 时，行不丢不重；tile 高度小于一行时不崩。
5. **缓存**：重复取同一个 tile 不重新 shaping；tile 缓存淘汰后重新构建正确。
6. 基准（`-benchtime 1x` 或多次取中位，写进提交信息）：
   - 500 万字滚动模式布局时间（应与分页模式相当或更快）
   - 单个 tile（视口 640×900，字号 18）冷构建 p50
   - 命中缓存的 tile 取用耗时
   - 连续 200 个 tile 的平均构建耗时
7. `git status` 干净，提交到 `m2` 分支，**不要 push**。

## 风险清单

- 章间边距语义（见上）容易做错，先写测试再实现。
- tile 与章边界交叉：tile 内容可能来自两章，别假设 tile 属于一章。
- 行缓存的生命周期必须跟 `PageKey` 变体一致，变体淘汰时同步清理，否则内存
  会悄悄涨（用 `Options.LineCache` 硬上限兜底）。
- 分页模式的 `Page.HitTest` 已有坐标系，新增 `Layout.HitTest` 不要悄悄改它的
  语义。
