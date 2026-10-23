# 任务 A：M3 —— BreakKey / PageKey 三层缓存与快速重排

> 分支 `m3`，工作树 `/home/laix/layx-m3`
> 参照 `Layx.md` §7.3、§7.4、§12 的 M3 行。

## 目标

把“改设置”的代价按 `Layx.md` §7.3 的表降下来：

| 变化 | 目标成本 | 手段 |
|---|---|---|
| 行高 / 段间距 / 上下页边距 | **≤30ms**（500 万字） | 断行结果不变，只重分页 |
| 字号 / 字体 / 页宽 / 字间距 | 重断行，但复用未变的章 | `BreakKey` 分章粒度 |
| 主题 / 颜色 | 不重排 | `PageKey` 之内，`PaintKey` 之外 |

核心是三层 key：

```text
BreakKey = hash(text, fonts, fontSize, letterSpacing, width, lang, hyphenation)
PageKey  = hash(BreakKey, lineHeight, paragraphGap, pageHeight, margins, widows/orphans)
PaintKey = hash(PageKey, theme)
```

- `BreakKey` 不变 → 复用行盒（`flowItem` 列表），只重跑 `paginate()`。
- `PageKey` 不变 → 复用页映射，只重绘。
- 只缓存最近 2–4 个变体，不做全组合预计算。

## 允许改的文件

- `render/keys.go`（新建）
- `render/layout.go`
- `render/text.go`（仅当 `BreakKey` 的输入需要暴露）
- `render/options.go`
- 新增测试文件 `render/keys_test.go`
- 可新建 `render/cache.go` 存放变体缓存

## 禁止改的文件

`render/page.go`、`render/paint/**`、`render/flow.go` 的既有语义、
`examples/**`、`scripts/**`、`tools/**`。如果确实必须动，先停下来写进
`docs/task-m3-blocked.md` 并在提交信息里说明，不要直接改。

## 验收

1. `go test ./...` 全绿，现有 golden/布局测试不许改期望值。
2. 新增测试：同一文档，改 `LineHeight` 后重排，**页数、行盒文本区间、每行
   rune 范围与全量重排完全一致**（只有 y 坐标和页边界可以变）。
3. 新增测试：用计数器证明 `LineHeight` 变化时 **shaping 调用次数为 0**。
4. 新增测试：`Relayout` 改字号时，`BreakKey` 未变的章不重新 shaping。
5. 基准：`BenchmarkLayout5M` + 一个 `LineHeight` 变化基准，记录
   `-benchtime 1x` 的实际秒数，写进提交信息。
6. `git status` 干净，提交到 `m3` 分支，**不要 push**。

## 注意

- 保持 `Layout` 的公开 API 兼容；`Relayout` 已经存在，别改签名。
- 缓存要尊重 `context.Context` 取消。
- 不引入新依赖（`render/deps_test.go` 有闸门）。
- 不要为了压数字牺牲正确性：页数或断行位置变了就是 bug。
