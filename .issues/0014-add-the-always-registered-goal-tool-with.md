---
id: 14
title: Add the always-registered goal tool with get, complete, and blocked
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 19
        - 17
        - 16
    depends-on:
        - 12
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## 背景

草案 §6：Phase 1 只给模型一个窄工具 `goal`，由 `internal/goal.Manager` 强制执行迁移。工具必须常驻注册，不能随 goal 启停动态改工具表；没有 active goal 时返回明确的 "no active goal"。模型只能标记 complete 或 blocked，pause/resume/clear/预算由用户与系统控制。

## 目标

在 `internal/goal` 实现 `Tool`（满足 `tools.Tool` 接口），并在 chat 启动路径注册它，同时在系统提示里加入对应能力说明。

## 实现要点

- `Tool.Manager *goal.Manager`；`Name() == "goal"`；`Spec()` 里 `action` 枚举 `get|complete|blocked`，`goal_id`、`reason` 为可选字符串字段，`required` 只有 `action`。
- `action: "get"`：返回当前 goal 的 id、objective、status、attempts（used/max）、stopping rules（Contract 的 StopWhen/Verification/Constraints/Boundaries）与 last_reason；无 goal 时返回 `{"ok": false, "error": "no active goal"}` 之类的结构化结果而不是 Go error，让模型能读懂而不是反复重试。
- `complete`/`blocked`：必须带 `goal_id` 与 `reason`；交由 `Manager.RequestComplete/RequestBlocked` 判定，stale id、终态、阈值未满足都返回结构化失败结果（含当前状态与下一步该做什么），不要返回裸 Go error，避免模型误以为工具坏了。
- 未知 action 或缺失字段按 `workflow.Tool.Run` 的现状返回明确 error（与项目既有风格一致）。
- 成功时 JSON 含 `ok: true` 与新 status，便于 transcript 留证。
- 注册位置：`cmd/neo/main.go` 的 `runChatSession` 的 `extra` 列表，与 `workflow.Tool` 同级；`neo run` 头less 路径不注册（不接受交互控制）。
- 系统提示：新增 `goalCapability` 常量并在 `capabilitySections(reg)` 里按 `reg.Get("goal")` 追加，正文要写明：完成需先验证、不要缩小目标、blocked 需同一阻塞条件连续多次、只有用户能暂停/恢复/清除。
- `docs/developer/tools.md` 表格加 `goal` 行（Interactive chat；get/complete/blocked）。

## 验收标准

- 单测：无 goal 时 get 返回结构化 "no active goal"；有 goal 时 get 字段完整。
- 单测：complete 缺 reason、blocked 首次调用、stale goal_id 均不改变状态，且返回可读原因。
- 单测：阈值满足后 blocked 生效；complete 成功后状态为 complete，再次 complete 被拒。
- 单测：`Spec()` 的 JSON schema 与实现接受的输入一致（action 枚举、缺失 action 报错）。
- `cmd/neo` 测试断言 chat 注册表含 `goal`，headless 注册表不含。
- `go test ./internal/goal/... ./cmd/neo/...` 通过。
