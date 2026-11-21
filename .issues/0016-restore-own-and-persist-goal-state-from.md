---
id: 16
title: Restore, own, and persist goal state from the chat composition root
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 17
    depends-on:
        - 12
        - 13
        - 14
    related-to:
        - 15
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## 背景

草案 §5/§12/§14 第 2-3 步：goal 状态属于会话，但只有 `cmd/neo` 知道会话文件；`internal/goal.Manager` 负责迁移，`internal/tui` 负责交互。缺了这层组装，`/goal` 无法在 resume 时还原，也无法在迁移后立刻落盘。当前 `saveChatSession` 只写 messages/usage/metadata，TUI 通过 `WithAfterSend` 触发保存。

## 目标

在 `runChatSession` 里创建唯一的 `*goal.Manager`：从 `sess.Goal` 还原、把快照写回会话、提供「立即保存」回调，并把 manager 传给 TUI 与 `goal` 工具（工具注册由 #14 完成）。

## 实现要点

- `runChatSession`：`mgr, err := goal.NewManager(sess.Goal, goal.Limits{MaxAttempts: ...})`；恢复失败时打印 warning 并以无 goal 继续，不让旧会话因新字段损坏而无法打开。预算值先用固定默认（`goals.max_attempts` 由 #15 接入，避免本 issue 依赖未完成的配置字段）。
- `saveChatSession`：保存前 `sess.Goal = mgr.Snapshot()`，保证 messages 与 goal 同时落盘；`sess.Goal` 为 nil 时字段不出现。
- 新增 `tui.WithGoalManager(mgr *goal.Manager)` 选项与 `model.goal *goal.Manager` 字段；此 issue 只完成接线，不添加任何 slash 命令和 UI 文案。
- 新增 `tui.WithGoalPersist(func() error)`（或复用 `AfterSend`）：供后续 `/goal` 命令在**发送首轮之前**同步落盘。回调失败必须返回错误给 TUI 展示，不能静默。
- 迁移发生在 TUI 内时（后续 issue），保存通过同一个回调，保证「创建 goal 后立即保存 → 发送首轮 → 每轮后再保存」的次序。
- 不修改 `internal/agent`；goal 相关状态不进入系统提示（`goal` 工具的提示由 #14 负责）。
- `neo run`（headless）不创建 manager、不读 goal 字段，行为不变。
- 单元测试：恢复保存过的会话后 manager 快照与原文一致；`sess.Goal == nil` 时保存出的 JSON 无 `goal` 键；`saveChatSession` 会带上 manager 的当前状态；恢复非法状态时只 warning 不失败。

## 验收标准

- `neo resume <id>` 打开含 active goal 的会话后，`/goal status`（由 #17 提供）能显示同一 goal；本 issue 的测试用 manager 快照断言。
- goal 迁移后立即调用保存回调，会话文件中的 goal 与内存一致。
- `go test ./cmd/neo/... ./internal/tui/... ./internal/goal/...` 通过。
- 不新增对 `internal/goal` 之外包的循环依赖。
