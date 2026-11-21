---
id: 18
title: Clear the active goal and pending continuation on /clear
status: closed
priority: high
labels:
    - bug
    - tui
relations:
    blocks:
        - 22
    depends-on:
        - 17
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## 背景

草案 §9：`/clear` 开始一段没有上下文的新对话，如果 goal 还活着，Neo 会继续在一个已清空的 transcript 上自动续跑，等于让模型在没有证据的情况下继续推进。Claude 的实现也在 `/clear` 时清除 active goal。

## 目标

`/clear` 清除当前会话的 goal，并取消任何已排队但尚未开始的自动续跑；两者都随会话立即保存。

## 实现要点

- `resetConversation()`（`internal/tui/reset.go`）统一负责会话级清理，因此 goal 清理放在这里：调用 `m.goal.Clear()`（manager 由 #16 注入，为 nil 时跳过），并清掉调度器里待执行的续跑标记。
- 清理顺序：先取消可能的待续跑，再清 goal，再清 transcript，最后沿用现有 `persistSession()`；保存失败必须照旧显示错误块（不能吞掉）。
- 不在 `/clear` 时自动 `pause`：草案要求「清除」而不是「保留但暂停」，用户想保留应使用 `/goal pause`，这一点写进 notice 文案。
- 若 `/clear` 时存在 goal（任意状态），追加一条 notice 说明 goal 已清除（含 goal ID 与 objective 摘要）并提示 `/goal pause` 是「保留但不续跑」的正确命令，让 transcript 之外的行为不会无声消失。
- `resetConversation` 的既有测试（`reset_test.go`）要补充 goal 断言；新增测试覆盖：有 goal 时 `/clear` 后 `Snapshot() == nil`、保存被调用一次、notice 出现；无 goal 时 `/clear` 不产生多余 notice。
- 不修改 `internal/agent`。

## 验收标准

- `/clear` 之后不会再出现自动续跑 turn，`/goal status` 显示 no active goal。
- 会话文件中的 `goal` 键随之消失（或为 null 时按 #13 的规则处理）。
- `go test ./internal/tui/...` 通过。
