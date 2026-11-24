---
id: 73
title: Add the script capability prompt and raise the three prompt budgets from a measured delta
status: closed
priority: high
labels:
    - feature
    - performance
relations:
    blocks:
        - 75
    depends-on:
        - 71
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

当前固定地板实测 8,084 B / 闸门 8,500 B，余量仅 416 B（`cmd/neo/performance_test.go:38`）。脚本工具会同时增加 schema 与提示词，但具体多少还不知道。

先测再改，理由来自 `docs/design/token-budget.md` 规则 2：“A reduction is justified by a recorded before/after, not by intuition.” 上调预算同样要有记录。

没有这段提示词，模型不会用这个工具，或者在只需一次 `read_file` 时写脚本——两者都会让新工具变成负担。

> ponytail 评审后合并：原“先测（#73）”+“再改（#74）”两步会留下一个提示词已加、闸门未调、`cmd/neo` 测试变红的中间态。测量必须在同一提交内完成并落地，数字写进 issue 评论即可。

## 目标

一次提交内完成：写出最终提示词与 schema、测量精确增量、把三个预算闸门调到测量值、测试全绿。

## 实现要点

- **测量**：先跑 `go test ./cmd/neo -run TestFixedPromptFloorBudget -v` 记录基线（`tools`/`prompt`/`total`），加入提示词与 schema 后再跑一次，算出精确增量。把两组数字与差值写进本 issue 评论。
- **提示词位置**：`cmd/neo/main.go` 的 `capabilitySections`（`cmd/neo/main.go:442`），只在 `script` 已注册时追加，保持“指令跟随工具存在”的既有约定。
- **内容自己写**，要点：
  - 适合：对工具结果做过滤/计数/循环/条件；批量读取后汇总；大文件只要结论不要原文。
  - 不适合：单次读、单次改；需要跨调用保持状态。
  - 脚本最后一条表达式的值即工具输出（与 `RunScript` 的实际语义一致，不要写成“导出函数”）。
  - 白名单以 #71 的最终实现为准，`bash` 永远不可用。
  - 出错如实报告；被拒绝的调用不要重试绕过。
- **长度**：目标 300–450 B；超出就在 issue 里说明原因。
- **三个预算一起改**：`fixedFloorByteBudget`、`corePromptByteBudget`、`chatPromptByteBudget`，按“向上取整到下一个 500 B”的既有惯例（见 `performance_test.go:36` 注释），注释写测量日期与新数字构成。
- **同步文档**：`docs/performance.md` 表格与 `docs/design/token-budget.md` 的说明。
- **规则 1 验证**：新增内容全部进入稳定前缀，两次渲染字节一致。

## 验收标准

- `go test ./cmd/neo -run 'TestFixedPromptFloorBudget|TestChatSystemPromptSizeBudgets' -v` 通过并打印新数字。
- 三处数字一致：`performance_test.go`、`docs/performance.md`、`docs/design/token-budget.md`。
- 测试证明 `script` 未注册时该段提示词不出现（`neo run` 场景）。
- 前缀稳定性测试通过。
- 提示词不含外部项目的具体语法或专有名词。
- issue 评论中有改前/改后两组完整数字。
