---
id: 20
title: Enforce the attempt budget and show budget_limited handoff
status: open
priority: high
labels:
    - feature
relations:
    blocks:
        - 22
        - 23
    depends-on:
        - 15
        - 19
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

草案 §8：attempt 是「一次自动续跑 turn」，首次 `/goal <objective>` 那一轮不计入。耗尽时 goal 进入 `budget_limited` 并给出明确 handoff，而不是悄悄停止。当前 `internal/agent` 只有 `MaxTurns`（单轮内工具循环的安全阀，默认 500），与 goal 预算含义不同，不能复用。

## 目标

把 `goals.max_attempts` 真正接到 runtime：manager 的 `Attempts.Max` 来自配置，耗尽后状态为 `budget_limited`，TUI 输出草案 §8 的 handoff 文案。

## 实现要点

- `cmd/neo` 创建 manager 时传入 `cfg.GoalMaxAttempts()`（#15 提供；本 issue 之前先用默认 20，接入后删掉临时常量）。
- `goal.Manager.ConsumeAttempt` 在 Used == Max 时返回「已耗尽」并置 `budget_limited`（本 issue 之前可能只返回计数）；调用点（#19 的调度器）据此不发送续跑。
- TUI：预算耗尽的迁移后立刻保存，并输出 handoff 块：
  ```
  Goal budget reached: 20/20 attempts used.
  Last reason: <last reason or "none recorded">
  Use /goal resume to continue or /goal clear to stop.
  ```
- `/goal status` 显示 `20/20 attempts (budget limited)`；`/goal resume` 重置 Used=0 并回到 active（manager 已在 #12 实现）。
- 首次 `/goal <objective>` turn 不调用 `ConsumeAttempt`，用测试锁住这一点。
- token 与 wall-clock 预算明确不做：在 `internal/goal` 的类型注释里写清 Phase 1 只按 attempt 计，`usage_limited` 暂无生产路径。
- 文档：`docs/developer/config.md` 的 `goals` 小节补充耗尽行为与 resume 语义；`CHANGELOG.md` Unreleased 追加 `/goal` 条目在最后统一处理（见文档 issue），本 issue 只需保证代码注释与字段注释准确。
- 测试：默认 20 与配置值都生效；耗尽后状态为 budget_limited、无自动续跑；resume 后再次获得完整预算；首次 turn 不计数。

## 验收标准

- 把 `max_attempts` 设为 1 时，一次自动续跑后 goal 变为 budget_limited 并显示 handoff。
- `/goal resume` 后自动续跑恢复，Used 从 0 开始。
- `go test ./internal/goal/... ./internal/tui/... ./internal/config/... ./cmd/neo/...` 通过。
