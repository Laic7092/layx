# FreeType：同一份工作的参照

这三个 C 文件不是 `layx/raster` 的依赖，也不参与任何构建。它们是尺子：
同一字体、同一字号、同一批字形，用平台自己的光栅器（FreeType 的灰度模式）
做一遍，好让「我们的速度与画质」是一个可以复查的数字，而不是一句自评。

FreeType 是 Linux/ChromeOS 上浏览器和 GTK 应用画文字的底层；Windows 上对应的
是 DirectWrite，macOS 上是 CoreText。挑它当参照，是因为它只有灰度覆盖率——
和这个包做的是同一件事，不掺 subpixel 和 gamma 的差别。

## 三个工具

| 文件 | 做什么 | 用途 |
| --- | --- | --- |
| `ftbench.c` | 一份 gid 列表，`FT_Load_Glyph` + `FT_Render_Glyph` 计时 | 每字形冷栅格成本 |
| `ftpage.c` | 一页的 glyph run（gid x y size，设备像素）合成到图上 | 整页成本与墨量 |
| `ftdump.c` | 每个字形一张 80×80 PGM，笔位 (20,20) | 与真值、与我们的覆盖率逐像素比 |

```sh
cc -O2 -o ftbench ftbench.c $(pkg-config --cflags --libs freetype2)
cc -O2 -o ftpage  ftpage.c  $(pkg-config --cflags --libs freetype2)
cc -O2 -o ftdump  ftdump.c  $(pkg-config --cflags --libs freetype2)
```

## 量出来的是什么

`layx/raster` 的对照项是仓库里的 `BenchmarkGlyphRasterizer`、
`BenchmarkPageRender`（在 `layx-reader` 里）与 `TestGlyphFidelity`。
2026-10-26，本机（amd64，Go 1.24，FreeType 2.13.2），字体 wqy-microhei，
《阿Q正傳》第 5 页：474 个字形实例、216 个不同 gid、34 px（17 DIP @ DPR 2）。

| | 每字形冷栅格 | 同一页（474 字形） | 与 16×16 点采样真值 | 墨量/真值 |
| --- | ---: | ---: | ---: | ---: |
| layx/raster | **3.4 µs** | **1.9 ms**（含纸面与墨色修正） | **0.085/255** | 1.0022 |
| FreeType | 5.0 µs | 3.3–3.6 ms（只有栅格与混合） | 0.122/255 | 1.0049 |

两件事值得说明：

- **为什么我们的误差更小**：`Coverage` 对每条边做精确面积积分，FreeType 的灰度
  光栅器是 4×4 子采样加点采样。0.085 与 0.122 的差别就是「面积」与「采样」的差别，
  不是调参调出来的。
- **为什么整页差得比每字形多**：同一页的 1.9 ms 里还包括一次整页纸面填充和逐 run
  的墨色修正，FreeType 那 3.3 ms 只有字形本身。参照物比的是「画这些字形要多久」。

## 复现

每字形成本：

```sh
awk '{print $1}' runs.txt | sort -u > gids.txt     # 任意页码的字形 id
./ftbench /usr/share/fonts/truetype/wqy/wqy-microhei.ttc 34 gids.txt 40
go test -run XXX -bench BenchmarkGlyphRasterizer -benchtime 2s .   # 在 raster/
```

画质对真值：

```sh
LAY=34
LAYX_GIDS=gids.txt LAYX_FONT=/usr/share/fonts/truetype/wqy/wqy-microhei.ttc \
  LAYX_PX=$LAY LAYX_DUMP=/tmp/ref go test -run TestDumpReferencePGM .   # 写 ours-/ref-
./ftdump /usr/share/fonts/truetype/wqy/wqy-microhei.ttc gids.txt $LAY /tmp/ref  # 写 ft-
go run compare.go /tmp/ref
```

`runs.txt` 是显示列表里的字形位置，任何能遍历 layx 的 `Page` 的宿主都能生成：

```go
func (p *runDumper) DrawGlyphs(run paint.GlyphRun) {
	for _, g := range run.Glyphs {
		fmt.Fprintf(p.w, "%d %d %d %d\n", g.ID,
			int(math.Round(float64(run.X+g.X)*scale)), // 设备像素
			int(math.Round(float64(run.Y+g.Y)*scale)),
			int(math.Round(float64(run.Size)*scale)))
	}
}
```

整页墨量（用 `PageRenderer` 的同一条显示列表）：

```sh
./ftpage font.ttc runs.txt 840 1200 ffffff 000000 /tmp/ft-ref.ppm nohint
```

`ftpage` 不套墨色修正：FreeType 的灰度模式没有这一层，比的是光栅器本身。
修正值多少，看 `layx-reader` 的 `LAYX_TEXT_TONE=0` 与 `LAYX_TEXT_GROW`。
