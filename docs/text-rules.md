# 行内文本规则与开关

> 本文是**原则**，不是开关的实现说明。
> 它约束"哪些排版行为由核心决定、哪些交给宿主"，以及这些开关如何与三层缓存
> key 保持一致。

## 一句话原则

> **核心给机制，宿主定政策。零值等于规范默认。**
>
> 规范性的排版行为可以默认关闭，但不能不存在；能不能用、默认开不开，由使用
> 这个库的人决定。

这条原则有两个推论：

1. 引擎不替用户做审美判断。孤行保护、标点挤压、中西文间距都是**政策**，不是
   **正确性**。
2. 不做默认值，不等于不做实现。每一类被规范描述过的排版行为都应该有代码路径
   和开关，只是零值走"规范默认"。

## 三类行为，不能一视同仁

| 类别 | 例子 | 可否关闭 | 归属 |
|---|---|---|---|
| **正确性** | 断点集合（UAX #14）、行盒半行距（CSS Inline Layout L3）、字形 advance、双向文本（UAX #9）、字形 fallback | **不可** | 核心，无开关 |
| **排版政策** | 标点挤压、行末悬挂、widows/orphans、首行缩进、段间距、两端对齐策略、中西文间距、断字 | **可以，且默认按规范初始值** | 核心提供开关，值由宿主给 |
| **表现** | 颜色、背景、下划线、图片处理 | 与排版无关 | 宿主 / Painter |

判断标准：**关掉它会不会让引擎不再是排版器？** 会，就是正确性；不会，就是政策。

## 开关清单

零值必须是规范的初始值，这样"什么都不配"就等于"按规范渲染"。

| 开关 | 零值（规范默认） | 规范出处 | 影响 |
|---|---|---|---|
| `TextAlign` | `start`（左对齐） | CSS Text L3 §7.1 | BreakKey |
| `FirstLineIndent` | `0` | CSS `text-indent` 初始值 | BreakKey |
| `Hyphenation` | off | CSS Text L3 §5 | BreakKey |
| `LetterSpacing` | `0` | CSS | BreakKey |
| `Autospacing`（中西文间距） | **off** | CLREQ 建议项，非 CSS | BreakKey |
| `PunctuationCompression`（标点挤压） | **off** | CSS Text L4 `text-spacing-trim`、JLREQ/CLREQ | BreakKey |
| `HangingPunctuation`（行末悬挂） | **off** | CSS Text L4 `hanging-punctuation` | BreakKey |
| `JustifyStrategy` | 只分配空格 | CSS Text L3 §7.3 | 绘制期，不影响断行 |
| `ParagraphGap` | `0` | CSS 由样式给 margin，引擎不自作主张 | PageKey |
| `Widows` / `Orphans` | **关闭** | CSS Fragmentation L3 §4.4；浏览器多不实现 | PageKey |

几点说明：

- `PunctuationCompression` 与 `HangingPunctuation` 是**两件事**，规范来源不同，
  应能单独开关。挤压调整行内宽度分配，悬挂允许标点探出边界，二者对断行的
  影响不同。
- 挤压的影响已经量化，不是估计：用 `scripts/linecmp.sh punct` 跑同一段对话密集
  的中文，浏览器默认（`text-spacing-trim: normal`）每行比我们多 1–2 个字、
  70 行里有 28 行断点不同；把浏览器挤压关掉（`--trim space-all`）后 **70/70 行
  完全一致**。所以"引擎不自作主张、默认不挤压"这条政策是有数据支撑的取舍，
  实现时按 `text-spacing-trim: normal` 的规则（成对标点各收半宽），并用同一份
  语料做验收。
- `Autospacing`（1/8 em）曾经是无条件执行的，与"零值 = 规范默认"冲突；现在由
  `TextRules.Autospacing` 驱动，默认 off。
- `Widows` / `Orphans` 的零值就是"关闭"：`Typography.withDefaults()` 不再把 0
  提升为 2/2，浏览器本来也不实现这两条保护。
- `FirstLineIndent` 只存在于每个节点的 `Style` 上，`Typography` 不提供默认值：
  首行缩进属于文档样式，引擎不注入。

## 开关必须进入缓存 key

这是本文最重要的一条工程约束。

> 任何改变断行或分页结果的开关，只要没有进入 `BreakKey` / `PageKey`，
> `Relayout` 就会在缓存命中时返回**错误的布局，且不会报错**。

对应关系：

| 开关类别 | 进入 |
|---|---|
| 影响断行（宽度、断点、字距、间距） | `BreakKey` |
| 只影响垂直位置（行高、段间距、页高、边距、孤行） | `PageKey`（含 `BreakKey`） |
| 只影响绘制（颜色、背景） | `PaintKey`，**不得**进入 `PageKey` |

防护手段（不是约定，是结构）：

1. 所有断行/分页政策收进 `render/textrules.go` 的 `TextRules`，`BreakKey` 哈希
   **整个结构体**。新增字段自动进 key，不依赖有人记得改哈希函数。只影响分页的
   字段（`Widows`/`Orphans`）也一并进 `BreakKey`：这会让改孤行设置时多做一次
   断行，但不会出现"缓存命中却分页错误"的静默 bug。安全优先于缓存粒度。
2. 穷举测试：`render/keys_test.go` 逐字段改值，断言 `BreakKey` 变化。
3. 反向测试：只影响绘制的选项（颜色、装饰、背景、圆角、边框色）取值变化时，
   `PageKey` 必须不变（否则换主题会触发重排）。

## 预设不属于核心

核心不提供 `Profile: Book` 之类的命名预设。`Book` 是审美判断，核心不该有。

- 预设放在文档、`examples/`，或独立的 `layx/presets` 小包。
- 好处：预设可以随审美演进、可以由社区贡献多个，核心保持中立。

## 默认全关的代价

开箱即用的中文排版会比浏览器"看起来差一点"：标点不挤压、没有孤行保护、中西
文之间不加间距。这是**正确性的代价**，需要接受，并用文档补上：

- README 首屏给出"做中文小说请打开这四个开关"的代码块；
- 用文档承担审美，而不是用默认值承担。

## 开关只调参数，不换模型

可以给用户参数，不能给用户两套互相矛盾的世界观。例如：

- 分页与滚动必须共享同一份断行结果 —— 这是模型，不是开关。
- "分页用一套断行、滚动用另一套"这类需求应被拒绝。

## 已知与浏览器的差异（待补）

当规范与浏览器实现冲突时，**跟规范**，并把差异记录在这里。已知候选：

| 差异点 | 规范 | 浏览器 | 我们的选择 |
|---|---|---|---|
| `widows` / `orphans` | CSS Fragmentation 3 §4.4 定义 | Chromium 长期未在 multicol 中实现 | 跟规范，默认关闭 |
| margin collapsing 边界情况 | CSS 2.1 §8.3.1 | 各家实现有细节差异 | 跟规范 |
| `text-spacing-trim` | CSS Text L4 | 实现较新，默认值在演进 | 默认 off，跟初始值 |

## 决策

| 决定 | 结果 | 理由 |
|---|---|---|
| 标点挤压与行末悬挂 | **拆成两个独立开关** | 规范来源不同（`text-spacing-trim` vs `hanging-punctuation`），对断行的影响也不同 |
| `Autospacing` 默认 | **off** | 它来自 CLREQ 的建议，不是 CSS 初始值；默认开启会让"零值 = 规范默认"失效 |
| `Widows` / `Orphans` 零值 | **关闭（0 即不启用保护）** | 对齐浏览器实际行为；需要保护的宿主显式给值 |
| 中文首行缩进 2em | **完全交给样式，核心不提供默认值** | 首行缩进是文档排版决策，不该由引擎注入；`Typography` 不新增该字段 |

## 实施状态

五条待办已完成：`Widows`/`Orphans` 零值即关闭、`Autospacing` 改由开关驱动并
默认 off、依赖旧默认值的测试改为显式传值、`TextRules` 收口并整体哈希进
`BreakKey`（含穷举/反向测试）、`examples/` 显式设置 `FirstLineIndent` 且 README
给出中文小说推荐开关组。

`PunctuationCompression` 与 `HangingPunctuation` 也已按本文的开关约定实现，
默认 off；挤压的验收见 README 的 linecmp 一节。
