---
id: 74
title: Add the script capability prompt and raise the three prompt budgets together
status: wontfix
priority: high
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #61 的第 2 步。#73 已测得精确增量与提案。没有这段提示词，模型不会用这个工具，或者在只需一次 `read_file` 时写脚本——两者都会让新工具变成负担。

## 目标

落地最终版提示词与 schema，并把三个预算闸门同步调到测量值。

## 实现要点

- 提示段放 `cmd/neo/main.go` 的 `capabilitySections`（`cmd/neo/main.go:442`），只在 `script` 已注册时追加，保持“指令跟随工具存在”的既有约定。
- 内容必须自己写，要点：
  - 适合：对工具结果做过滤/计数/循环/条件；批量读取后汇总；大文件只要结论不要原文。
  - 不适合：单次读、单次改；需要跨调用保持状态。
  - 白名单以实际实现为准（#71/#72 的结论），`bash` 永远不可用。
  - 出错如实报告；被拒绝的调用不要重试绕过。
- 三个预算一起改：`fixedFloorByteBudget`、`corePromptByteBudget`、`chatPromptByteBudget`；注释写测量日期与新数字构成。
- 同步 `docs/performance.md` 表格与 `docs/design/token-budget.md` 的说明。
- 规则 1 验证：新增内容全部进入稳定前缀，两次渲染字节一致。

## 验收标准

- `go test ./cmd/neo -run TestFixedPromptFloorBudget

## Closed
已合并进 #73：测量与落地同一提交，避免中间态测试变红
