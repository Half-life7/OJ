# 外部题包导入样例

该目录可在题包导入界面选择“预览内置 A+B 样例”，也可作为自备 ZIP 的目录结构参考。打包时以本目录内容为 ZIP 根：

```text
problem.yaml
problem_zh.md
testdata/
  config.yaml
  1.in
  1.out
```

`problem.yaml` 至少需要 `title`，可包含 `tag`、`difficulty`、`time_limit_ms`、`memory_limit_mb`。外部站点的 `pid`、`owner`、`nSubmit`、`nAccept` 等字段会被忽略，不导入历史统计。

`problem_zh.md` 支持以“题目描述”“输入”“输出”“样例”作为分区标题，并识别 `input1`/`output1` 代码块。`testdata/config.yaml` 使用 `subtasks[].score` 和 `cases[].input/output`；case 可用 `score` 覆盖子任务均分。测试文件名继续遵守项目测试数据规则，只接受根目录成对的小写 `.in/.out`。
