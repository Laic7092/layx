# 样书

联调用的真实 EPUB，公有领域文本，来自 Project Gutenberg。

| 文件 | 来源 | 覆盖什么 |
|---|---|---|
| `aq-zhuan.epub` | [PG #25332](https://www.gutenberg.org/ebooks/25332) 鲁迅《阿Q正傳》 | 真实 EPUB 2：PNG 封面页（`wrap0000.html` 里只有 `<img>`）、Gutenberg 分块 HTML、NCX 目录 |

`TestSamplesOpen` 会逐个打开本目录下的 `.epub` 并 Build，所以截断的文件
不会混进来。TXT 样书属于纯文本解析，不在 `layx/epub` 范围内。

这个目录里的 `.epub` **不提交**（`.gitignore` 已排除）。测试和基准在没有样书时
会 skip，而不是失败。需要跑真实样书相关用例时，从兄弟仓库复制：

```sh
cp ../../../layx-reader/internal/book/testdata/samples/aq-zhuan.epub .
```

`TestSamplesOpen` 会逐个打开本目录下的 `.epub`；`TestTOCOnRealSample`、
`BenchmarkBuildAQ`、`BenchmarkOpenAQ`、`BenchmarkLayoutAQ` 只针对
`aq-zhuan.epub`。
