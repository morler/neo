---
id: 90
title: Show the model when a turn's calls chained on earlier output, as a turn-end note not a prompt section
status: open
priority: low
labels:
    - llm
relations:
    depends-on:
        - 83
    related-to:
        - 86
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

`/codemode status` 现在会报告本会话 `script` 调用次数（#81），但那是给人看的：模型自己不知道“这一轮里有三次调用的参数依赖上一次结果”。在工作流里，模型没有任何反馈回路提示它本可以合并。

唯一在会话内可以合法追加信息的位置是**轮末**，因为会话中途改缓存前缀会破坏 `docs/design/token-budget.md` 规则 1。`internal/agent/agent.go:987` 的 `repeatReminder` 已经示范了这种做法：追加到 conversation 末尾，不动系统提示、工具列表和历史。

## 目标

在轮末追加一句可观察的提示，让模型在下一轮能调整策略。

## 实现要点

- **优先在 CLI 边界观察**，不要在 `internal/agent` 里加策略判断：`MISSION.md` 明确“`internal/agent` 保持 policy-free”，`token-budget.md` 规则 4 同义。可行路径是在 `cmd/neo` 的事件消费侧统计“同一轮内存在 tool_result 文本被后续 tool call 的输入显著引用”这类信号，再通过已有的轮末追加通道注入。
- 若上述判断无法做可靠且低误报，**放弃本任务**，不要用启发式制造噪声。误报比沉默更糟：它会让模型在无用任务上也开始写脚本。
- 判据示例（择一，实现前先定）：同一轮内 ≥2 次 `grep`/`read_file` 的结果各自被后续调用的参数使用；或同一文件被读两次。
- 文案短，一句话，不写教程，不复述白名单。
- 不改 prompt、schema 字节；`TestFixedPromptFloorBudget` 与 `TestScriptFloorBudget` 字节数不变。

## 验收标准

- 单测：构造一个“读后按结果再读”的回合，断言轮末追加了什么；构造一个无依赖的并行读回合，断言**不追加**。
- 单测：追加发生在 conversation 末尾，系统提示与工具列表逐字节不变（复用现有缓存前缀测试的渲染路径）。
- 本任务合入前需先有 #83 的基线，否则无法判断这条提示是否让模型在无收益任务上也开始写脚本。
- 若判定不可靠，用 `issues close --wontfix` 并写明理由，不留半成品。
