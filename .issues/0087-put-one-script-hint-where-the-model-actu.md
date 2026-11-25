---
id: 87
title: Put one script hint where the model actually chooses a tool, not only in the system prompt
status: open
priority: low
labels:
    - llm
relations:
    depends-on:
        - 86
    related-to:
        - 89
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

缓存前缀在会话中不能变（`docs/design/token-budget.md` 规则 1），所以 `scriptCapability` 那 564 字节只在会话开头说了一次。模型真正做选择时读到的是相邻工具的描述：

- `bash`：`Use for git, tests, builds, file inspection beyond Read.`（`internal/tools/bash.go:45`）
- `grep`、`read_file`、`glob`：各自只描述自己。

两者都不提脚本，于是“要不要包一层脚本”这个判断在决策发生的那一刻没有输入。这是 #80 里模型全程选 `bash` 的最朴素解释。

## 目标

在决策点加一句最短的判据，代价可控且可测。

## 实现要点

- 首选方案：在 `bash` 的 schema 描述末尾加一句，指向“下一步依赖这次输出”的场景并提及 `script`。`bash` 是模型最常选的工具，也是脚本最该替代的路径。
  - 必须只在 `script` 已注册时出现——用 `registryFor` 现有的组合方式决定描述文本，不要让默认 floor 支付这笔字节。
  - 实现上注意 `tools.Bash` 的 `Spec()` 目前是常量文本，加可配置字段时要保证 `registryFor` 是唯一决定处，和它注释里“single decision about the script tool”的说法一致。
- 备选方案（若首选超出预算）：在 `grep` 描述上加同样一句，理由是计数/过滤是最常见的依赖场景。
- 这句话只说“何时”，不复述白名单、不给示例——细节留在 `scriptCapability` 与 `script` 的 schema 里，避免三处漂移。
- **默认 floor 不得增长。** `TestFixedPromptFloorBudget`（8,084 / 8,500）必须不变；`TestScriptFloorBudget`（9,499 / 10,000）允许小幅增长，超出则按 `docs/performance.md:178` 同步预算与文档。

## 验收标准

- 单测：`script` 关闭时，`bash`（或所选工具）的描述**逐字节**与改动前相同。
- 单测：`script` 开启时，描述包含那句指向脚本的判据。
- `TestFixedPromptFloorBudget` 字节数不变；`TestScriptFloorBudget` 通过。
- `go test ./internal/tools ./cmd/neo` 通过。
- 合入后用 #83 的任务集复测一次，数据写进本 issue；不改 `scriptCapability` 本身（那是 #84 的范围），以便归因。
