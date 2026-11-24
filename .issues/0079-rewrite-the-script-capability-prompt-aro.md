---
id: 79
title: Rewrite the script capability prompt around task shapes with a copyable example
status: open
priority: high
labels:
    - feature
    - llm
relations:
    blocks:
        - 80
    depends-on:
        - 78
    related-to:
        - 76
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

当前提示段（`cmd/neo/main.go:104-113` 的 `scriptCapability`）只给了机制描述：

> When a later call depends on an earlier result, write a script instead of asking for the result back ...

问题有三：

1. 模型难以把“依赖前一步结果”映射到手上的具体任务，缺少可识别的任务形状。
2. 只有一条负向判据（"not over one read"），没有对称的正向清单。
3. 没有可复制的最小示例；`docs/performance.md:95` 记录模型为发现调用约定花了三次失败调用（误以为有 `glob`、写裸 `read_file`、用 `await`）。

#76 的结论明确写了“改进提示词后再测一次”，本任务是那条结论的落地。

## 目标

把提示段改写成“任务形状 + 一个最小示例”，并让 `Spec()` 与 `scriptCapability` 不再重复同一份工具清单。

## 实现要点

- 正向三条，用可识别的情境而非抽象机制：
  - 需要计数或判定（“是否恰好一处匹配”“共几处 TODO”），在脚本内取长度，不把全文送回对话让模型自己数。
  - 跨 N 个文件取同一处（`glob` 出候选 → 逐个 `grep`/`read_file` → 只返回命中项）。
  - 读后按内容分支再改，同一脚本内完成读-判断-`edit_file`。
- 负向只留一条：单次读、单次搜、单次 `bash` 直接调对应工具。
- 附一个 3 行、可直接改参数就用的示例，示例须真实可跑（用 `tools.` 前缀、同步、`grep` 结果是文本行需自行切分）。
- 工具清单以 #78 修正后的 `Spec()` 为准，提示段只讲“何时用”，不复述白名单，避免两处再次漂移。
- 措辞保持陈述而非命令；不要写“必须优先使用”，依据见 `docs/performance.md:95-96`。

## 验收标准

- 提示段含上述三条正向情境与一个可跑示例，且示例与 `internal/tools/script.go` 的实际 API 一致（在 `script_test.go` 中以同一源码跑通，证明示例不是编造的）。
- `chatPromptByteBudget`（当前 3,000，实测富余）与 `scriptFloorByteBudget`（9,500，实测 9,227）通过；超出则按 `docs/performance.md:178` 的规则同步 `scripts/performance.sh` 与文档，不静默放宽。
- `scriptCapability` 中不再出现逐项工具清单，只有指向 schema 的一句。
- `go test ./cmd/neo` 通过。
