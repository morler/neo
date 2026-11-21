---
id: 12
title: Add the internal/goal state machine, transitions, and blocked audit
status: open
priority: high
labels:
    - feature
relations:
    blocks:
        - 13
        - 14
        - 16
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

`docs/design/goal-runner.md` §5/§6/§7 决定把 `/goal` 做成显式产品状态：每个会话至多一个 goal，状态与迁移由 `internal/goal.Manager` 串行化维护，TUI、`goal` 工具、调度器都只通过它改变状态。仓库目前没有该包，后续所有任务都依赖这里的类型与迁移规则。

## 目标

新增 `internal/goal` 包：`State` 与快照类型、合法状态迁移、attempt 计数、blocked 阈值，以及驱动续跑的最小查询接口和完整单测。

## 实现要点

- 类型按草案 §5：`State{ID, Objective, Contract, Status, Attempts{Used,Max}, Blocked{Signature,Count,LastReason}, LastReason, CreatedAt, UpdatedAt}`；`Status` 常量 active/paused/complete/blocked/budget_limited/usage_limited/cleared。
- ID 用 `goal_<hex>`，由 Manager 生成；`Start(objective, limits)` 覆盖已有 goal 并重置计数。
- Manager 用 mutex 串行化：`Start/Replace/Pause/Resume/Clear/ConsumeAttempt/RequestComplete/RequestBlocked/Snapshot`。
- 所有权边界：模型只能把 active 迁到 complete 或 blocked；active、paused、budget_limited、cleared 只能由用户或调度器产生。终态（complete/blocked/cleared）不再接受模型更新。
- `RequestComplete`/`RequestBlocked` 必须带 `goal_id`；与当前 goal ID 不一致时返回明确的 stale 错误。
- `complete` 要求非空 reason（作为证据说明）；`blocked` 用归一化后的 reason 作签名，同一签名连续出现 `BlockedThreshold`（2）次才生效，否则返回「继续工作」的结果，不同签名重新计数。
- `ConsumeAttempt` 在自动续跑入队时调用；达到 Max 时把状态置为 budget_limited。`Resume` 只允许从 paused/blocked/budget_limited 恢复到 active，并把 Used 归零、blocked 清零；complete/cleared/无 goal 时拒绝。
- `Clear` 移除 goal（之后 `Snapshot()` 返回 nil）；`Status=cleared` 只用于向 transcript 解释这次迁移。
- `NewManager(initial *State, limits Limits)` 接受 `neo resume` 从会话 JSON 还原的状态与预算上限：拷贝一份而不是持有调用方的指针，恢复时校验 ID/Status 合法性，非法状态返回错误而不是静默重来；`initial == nil` 表示新会话没有 goal。
- `State` 各字段带稳定的 JSON tag，直接复用为会话持久化与工具输出形状（草案 §5 的 `goal` 字段），避免上层再定义一套 DTO。
- `Contract` 在 Start 时填入默认 stopping rules（完成需验证、blocked 阈值、预算上限）；Phase 1 不做 `/goal draft`。
- `usage_limited` 只保留枚举值：Phase 1 没有 token 记账，任何迁移都不产生它。
- 包内不得 import `internal/agent` 或 `internal/tui`；错误信息要说明被拒绝的迁移和当前状态。

## 验收标准

- 表驱动测试覆盖 start/replace/pause/resume/clear、stale goal id、终态再迁移、blocked 阈值（1 次不生效、2 次同签名生效、不同 reason 不累积）、预算耗尽、Resume 重置计数。
- `go test -race ./internal/goal/...` 通过，并发 Snapshot 与迁移无数据竞争。
- 公开 API 足以实现 `/goal status` 的全部展示字段，上层无需读取内部字段。
