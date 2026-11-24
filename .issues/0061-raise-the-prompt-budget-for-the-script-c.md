---
id: 61
title: Raise the prompt budget for the script capability and teach the model when to use it
status: wontfix
priority: high
labels:
    - feature
relations:
    blocks:
        - 62
    depends-on:
        - 60
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

当前固定地板实测 8,084 B，闸门 8,500 B（`cmd/neo/performance_test.go:38`），余量只有 416 B。脚本工具必然同时增加两部分：工具 schema，以及一段“什么时候该写脚本”的提示词。用户已同意调整提示预算。

没有这段提示词，模型不会用这个工具，或者会在只该调一个 `read_file` 时写脚本——两者都让新工具变成负担。

## 目标

加入 `script` 的能力说明与工具 schema，并把预算闸门调整为有依据的新值。

## 实现要点

- 提示词放 `cmd/neo/main.go` 的 `capabilitySections`（`cmd/neo/main.go:442`），只在 `script` 已注册时追加，保持“指令跟随工具存在”的既有约定。
- 内容要点（自己写，不抄外部项目）：
  - 适合：需要对工具结果做过滤/计数/循环/条件判断；同一批读取要做汇总；大文件只想要结论不想把原文塞回上下文。
  - 不适合：单次读取或单次编辑；需要人工确认的写操作；需要跨调用保持状态。
  - 脚本内可用工具是白名单，`bash` 与写操作不可用（或按实际实现为准）。
  - 出错要如实报告，不要靠重试同一调用绕过拒绝。
- 长度控制：目标 300–450 B。`chatSystemPrompt` 现在是 843 B，四个 capability 块合计约 1,703 B。
- 预算调整：按实测把 `fixedFloorByteBudget`、`corePromptByteBudget`、`chatPromptByteBudget` 一起改，并在注释里写上测量日期与新数字的构成；同步 `docs/performance.md` 的表格与 `docs/design/token-budget.md` 的说明。
- 必须验证规则 1：新增内容全部进入**稳定前缀**（工具列表与 capability 块都在缓存基线内），两次渲染字节一致。

## 验收标准

- `go test ./cmd/neo -run TestFixedPromptFloorBudget

## Closed
已拆分为 #73（先测量）+ #74（提示词与预算调整）
