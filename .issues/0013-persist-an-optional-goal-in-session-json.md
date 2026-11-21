---
id: 13
title: Persist an optional goal in session JSON and restore it on resume
status: open
priority: high
labels:
    - feature
relations:
    blocks:
        - 16
    depends-on:
        - 12
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

草案 §5 要求 goal 属于会话而不是整个 Neo 进程：新会话没有 goal，恢复会话还原它，切换会话就切换 goal 上下文。现在 `internal/session.Session` 只有 `metadata/messages/usage`，`/goal` 无处持久化，`neo resume` 也会丢状态。

## 目标

在会话文件里增加可选的顶层 `goal` 字段，保存/加载/索引行为与草案一致，旧会话继续可用。

## 实现要点

- `internal/session/session.go`：`Session` 增加 `Goal *goal.State \`json:"goal,omitempty"\``，import `internal/goal`（goal 不反向 import session，保持无环）。
- `Save` 原样序列化 `Goal`；nil 时 JSON 中不出现该键。
- `Load` 对缺字段的旧文件返回 `Goal == nil`，不得报错，也不得把空对象当成有 goal。
- `Session.Goal` 不写入 `index.json`，除非后续 UI 需要列表展示（Phase 1 不需要）；`Metadata` 结构保持不变。
- `TitleFromMessages`、`Store.Search`、`List` 对含 goal 的会话行为不变：goal 文本不进入标题或搜索正文，避免用户目标被当成会话第一句。
- 增加 `Store.Save` 的调用时机说明注释：goal 创建后、每个 turn 后、每次用户可见迁移后各保存一次，由调用方（`cmd/neo` 的 `saveChatSession`）负责，不在 session 包内定时保存。
- `internal/session` 不 import `internal/tui`、`internal/agent`。
- `docs/developer/sessions.md` 加 `goal` 字段小节（字段表 + 「旧会话没有该字段」），草案实施后与代码一致。

## 验收标准

- 新增测试：带 goal 的会话保存后重新加载，goal 各字段（ID/Objective/Status/Attempts/Blocked/理由/时间戳）逐一相等。
- 新增测试：手写一份不含 `goal` 键的旧会话 JSON 能加载，`Goal == nil`。
- 新增测试：含 goal 的会话标题仍取自第一条用户消息，`Search` 不因 goal 文本产生额外命中。
- 新增测试：`Goal` 为 nil 时序列化结果里没有 `"goal"` 键。
- `go test ./internal/session/... ./cmd/neo/...` 通过。
