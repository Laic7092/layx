# layx-raster

[layx](https://github.com/laix/layx) 的纯 Go 软件光栅器。输入是 layx 已经排版好的显示列表
（`render.Page` / `render.Tile`），输出是 `*image.RGBA`。

layx 核心刻意**不内置光栅器**：它只产出 `paint.Painter` 调用序列，字形 ID、位置、颜色都由
shaping 和布局决定。本仓库补上缺的那一半——覆盖率计算和像素混合——所以离线截图、快照、
e-ink 驱动、没有 GPU 的宿主都能直接跑，而不必再写一个 Canvas/OPENGL 后端。

```text
layx                          layx-raster
Document → 布局 → DisplayList → paint.Painter
                                        ↓
                              覆盖率扫描线 / 裁剪掩码 / 混合
                                        ↓
                                  *image.RGBA → PNG
```

## 用法

```go
import (
    "github.com/laix/layx-raster"
    "github.com/laix/layx/render/paint"
)

page := layout.Page(0)
r := raster.New(int(page.Size().Width), int(page.Size().Height))
r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) // 纸
page.Draw(r)                                              // 墨
png.Encode(w, r.Image())
```

滚动的 tile 一样：`tile.Draw(r)`。位图通过 `r.SetImage(id, img)` 注册，
`DrawImage` 会按当前变换做双线性采样。

命令行示例（排版一段文本并输出第 N 页 PNG）：

```sh
go run ./cmd/layx-shot -font book.ttf -text chapter.txt -page 0 -out page.png
```

`cmd/layx-shot` 只做三件事：把纯文本切成段落建 `render.Document`、用 `render.Engine`
排版、把一页画进 PNG。EPUB/XHTML/CSS 仍然由 `github.com/laix/layx/epub` 负责。

## 已实现

| `paint.Painter` | 说明 |
| --- | --- |
| `Save` / `Restore` | 变换与裁剪的栈；裁剪掩码不可变，回退时不复制 |
| `Transform` | 任意仿射（旋转、镜像、缩放、错切都正确） |
| `ClipRect` | 整数轴对齐矩形走零成本快速路径；其余旋转/小数边缘生成逐像素覆盖率掩码 |
| `FillRect` | 轴对齐时按精确面积覆盖率填充，斜置时走多边形扫描线 |
| `StrokeRect` | 描边画成一个环（外轮廓 + 反向内轮廓），重叠处不会二次混合 |
| `FillRoundRect` | 四角三次贝塞尔圆弧，变换后按设备像素细分 |
| `DrawImage` | 逆变换逐像素映射 + 双线性采样（预乘插值，透明边缘不发黑） |
| `DrawGlyphs` | 直接从字体轮廓（`paint.Font.Data`）栅格化；按面/字形缓存轮廓 |
| `Clipped.Visible` | 把当前裁剪框反变换回用户空间，带 1px 余量，供 layx 剔除不可见 op |

其他行为：

- **抗锯齿**：8 条竖向子扫描线 + 水平解析覆盖率；不依赖平台字体光栅器。
- **合成粗体**：`GlyphRun.EmulateBold` 时沿文字自身 x 轴再画一遍（不改变布局）。
- **对齐坐标**：side bearing 与基线都来自 `paint.Glyph`，不会重新 shaping 字符串。
- **不拥有字体**：只读取 `paint.Font.Data`，按「地址 + 长度 + 面索引」解析并缓存。
- **确定性**：相同显示列表、相同字体、相同坐标得到逐字节相同的像素。

## 边界与取舍

- 只有灰度抗锯齿，没有 RGB subpixel 定位。文字位置本来就带小数，但没有 ClearType
  式的三通道滤波；这是为了在不同 DPR 下保持可预测。
- 没有 gamma 校正，混合在 sRGB 空间做预乘运算；不追求与浏览器逐像素一致。
- 只支持轮廓字形：COLR/CPAL 彩色字体、SVG 字形、位图字形会被跳过。
- 没有字体回退、没有 shaping、没有断行——这些都在 layx 里。
- 一个 `Rasterizer` 不是并发安全的；每个 goroutine 用自己的实例。
- 裁剪掩码按整个表面分配（页面尺寸）。这是离线渲染/快照的取舍；实时大画布应改用
  tile 加局部表面。
- 没有路径 API：`paint.Painter` 是唯一入口，本包不会变成通用 2D 绘图库。

## 包结构

```text
layx-raster/
  raster.go       Rasterizer、表面、状态栈、矩形/描边/圆角填充
  clip.go         裁剪的快速路径与覆盖率掩码，Visible
  geometry.go     仿射、box→device、扁平化（二次/三次贝塞尔）、shape
  cover.go        边界表、竖向子采样、水平面积累积、混合
  glyph.go        字体解析缓存、轮廓缓存、字形路径、合成粗体
  image.go        DrawImage 的逆映射与双线性采样
  cmd/layx-shot   文本 → 文档 → 一页 PNG 的最小示例
  testdata/       DejaVu Serif（测试字体，Bitstream Vera 许可，见 testdata/DejaVu-LICENSE.txt）
```

直接依赖只有 `github.com/laix/layx`（`replace` 到本地 `../layx`）和
`github.com/go-text/typesetting`（字体轮廓）。`golang.org/x/image` 是 layx 的间接依赖，
本包自己不用它；也没有 cgo。

## 测试

```sh
go test ./...
go run ./cmd/layx-shot -font testdata/DejaVuSerif.ttf -out /tmp/page.png
```

测试覆盖：矩形/α 混合/半像素覆盖、变换组合、Save/Restore、整数与小数裁剪、
裁剪掩码在 Restore 后不被污染、圆角、描边环、图像 1:1 与缩放、字形确定性/粗体/旋转、
以及从 `render.Document` 到 `Page.Draw` 的整页端到端（含超出画布平移后的剔除正确性）。
