---
id: 96
title: Treat no-result as a value in glob and keep tool-native errors out of script output
status: open
priority: low
labels:
    - bug
relations:
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

#91 的测量里，solo 模式出现过一次 `Error: open package.json: no such file or directory`，以及一次 `glob` 在空目录上返回冗长的 ripgrep 原生报错：

```
Error: rg: rg: No files were searched, which means ripgrep probably applied a filter you didn't expect.
Running with --debug will show why files are being skipped.
```

两条都是"找不到东西"，而"找不到"在探索阶段是**正常结果**，不是错误。默认模式下这同样会发生，只是 `bash` 让模型更容易自查。单工具模式下模型只有脚本可用，一条把等待放在错误路径上的信息会直接消耗一次调用。

`grep` 已有正确先例：无匹配时返回字符串 `"no matches"`，而不是错误。本任务把同样的处理扩展到其余位置。

## 目标

让"目标不存在/无结果"返回可判断的值，而不是抛错或回吐工具原生报错。

## 实现要点

- **`read_file` 不存在时**：仍然报错是合理的（读一个不存在的文件是明确失败），但错误应简短、可行动，并且**不要**泄漏宿主路径以外的工具内部细节。评估是否改为返回 `""` 加一句说明；若改为返回值，必须同时确认 `edit_file` 的 stale-read 语义不被绕过。
- **`run()` 的非零退出码**已经是数据，不需要改。
- **`glob`/`grep` 在无结果时**：`grep` 已是 `"no matches"`；`glob` 目前把 ripgrep 的 stderr 原样抛出，应统一为 `"no matches"`（或等价的短句）。
- 不要在错误信息里出现 `rg:` 这样的工具名重复前缀。
- 不改 `MaxOutputBytes` 与截断策略。

## 验收标准

- 单测：`glob` 在空目录返回 `"no matches"` 而非错误。
- 单测：`grep` 在空目录同样返回 `"no matches"`（现有行为的回归断言）。
- 单测：`read_file` 路径不存在时的错误文案包含文件名，且不含内部实现细节。
- 默认模式的既有测试全部通过：这一改动会影响顶层 `grep`/`glob` 的调用，需确认 `docs/developer/tools.md` 的描述仍然准确。
- `go test ./...` 通过；`qlty check` 退出码为 0。
