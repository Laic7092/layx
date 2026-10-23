# M2：滚动模式、tile 缓存与行缓存

本阶段在分页之外增加一条**滚动**输出路径。两条路径共享同一份 FlowLayout：
分页只改变“如何取窗口”，不改变断行、块布局和行盒。

## 输出边界

`render` 没有光栅器，所以 tile **不是位图**。

```text
Tile = 一段 DisplayList + 内容坐标区间
```

`Tile.Draw(painter)` 只回放显示列表。是否把 tile 光栅化成图片、缓存多久、
什么时候丢弃，都是宿主（Painter 侧）的决定。核心只缓存 DisplayList。

## 模式与 API

```go
layout, _ := eng.Layout(ctx, doc, render.LayoutOptions{
    Mode:       render.ModeScroll, // 默认 ModePaged
    TileHeight: 1024,              // 0 = Viewport.Height
    ...
})
```

- `ModePaged`：行为与 M1 完全一致。`PageCount()` 返回页数，`Page(n)` 可用。
- `ModeScroll`：`PageCount() == 0`，`Page(n) == nil`；用 `Tile` 系列 API。
- `Options.TileCache`：每个 Layout 保留的 tile 数，默认 8。
- `Options.LineCache`：每个 Layout 保留的 shaped 行数，默认 256。

```go
layout.ContentHeight()      // 滚动流总高（分页模式返回 0）
layout.TileCount()          // ceil(ContentHeight / TileHeight)
layout.Tile(i)              // 首次访问构建，之后按 TileCache 做 LRU
layout.Tiles(lo, hi)        // 同步预取；没有隐藏 goroutine
tile.Draw(painter)
tile.Links()
```

`Tile i` 覆盖内容坐标 `[i*TileHeight, (i+1)*TileHeight)`；最后一个 tile 也是
满高，内容可能提前结束。

## 内容坐标

滚动模式：

- `y = 0` 是**整本书正文的顶端**，不是每章顶端。
- 章与章连续堆叠。每章自己的根 block `MarginTop/MarginBottom` 保留，但页边距
  不会在章间重复。
- `Margins.Top/Bottom` 是页面视口的 inset，不进入内容坐标；`Margins.Left/Right`
  仍然用于 tile 内的 x 定位。

分页模式（`LocationAt` / `OffsetOf` / `HitTest` 使用）：

- 把页面视口首尾相接，看成一条虚拟滚动流：第 p 页覆盖
  `[p*contentHeight, (p+1)*contentHeight)`。
- 页内 x 从内容框左边算起，与 `Page.HitTest` 扣掉 `Margins.Left` 后的坐标一致。

`OffsetOf` 返回行盒**顶边**的 y。因此对任意 `loc`：

```text
y := OffsetOf(loc)
LocationAt(y) -> 同一行
OffsetOf(...) == y
```

## 位置稳定

`Location` 仍然只存 `Chapter/Node/Anchor/Offset`，不存像素。换 `LineHeight`
重建 Layout 后，用同一个 `Location` 调用 `OffsetOf`，会落到新行盒的同一行；
宿主持久化的仍然是位置，不是滚动偏移。

## 行缓存

`renderPage` / tile 构建不再无条件调用 `shapeRange`。

- key：`chapter + paragraph + rune 区间`
- value：**未做两端对齐**的 shaped runs
- 两端对齐在 `buildLine` 取用时计算，因此同一行可以被不同 item 复用
- key 命中时不增加 `Engine.shapes`
- 每个 Layout 一个 LRU，`Options.LineCache` 是硬上限；Layout 被丢弃或变体被
  淘汰时缓存随之释放

## tile 一致性

- 每一行只属于一个 tile：按行盒顶边所在的 tile 分配。跨 tile 的行不会被画两次。
- 图片可能跨 tile；它在每个相交 tile 里都会出现，由 Painter clip。
- 因此：所有 tile 的字形总数 == 所有页的字形总数，内容坐标一致（tile 局部坐标
  只差一个 `tile.Top` 常数）。

## 测试

```sh
go test ./render -run 'TestScroll|TestTileCache|TestLineCache|TestPosition' -count=1
go test ./render -race -count=1
go test ./render -run '^$' -bench 'BenchmarkScroll|BenchmarkTile' -benchtime 1x
```
