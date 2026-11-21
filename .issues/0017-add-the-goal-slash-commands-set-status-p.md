---
id: 17
title: 'Add the /goal slash commands: set, status, pause, resume, and clear'
status: open
priority: high
labels:
    - feature
    - tui
relations:
    blocks:
        - 18
        - 19
    depends-on:
        - 16
        - 14
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

草案 §4：Phase 1 的唯一用户界面是 `/goal <objective>`、`/goal status`、`/goal pause`、`/goal resume`、`/goal clear`。`/goal <objective>` 要「设置或替换」会话唯一的 goal，并立刻把 objective 当作第一个普通用户 turn 发送；其余子命令只操作控制状态。现在 `handleSlashCommand` 只有 `/help /model /provider /clear /quit` 和各 skill。

## 目标

把五个子命令接进 TUI，全部走 `goal.Manager`，并把状态展示成可读的 transcript 块。

## 实现要点

- `baseSlashCommands` 加 `{"/goal", "set or manage a standing goal"}`，`builtinSlashCommand` 与 slash picker 自动获得它（务必确认 `/goal` 不会被同名 skill 覆盖）。
- 解析：`/goal` 无参数或 `/goal status` 都展示状态（无参数时附一行用法提示）；`/goal <objective>` 把余下文本作为 objective（保留内部换行与顺序，仅 TrimSpace）。
- `/goal <objective>`：`mgr.Start(objective, limits)` → 同步保存（携带首轮前的 goal）→ 保存失败则回滚（`mgr.Clear()`）并显示错误，不发送 turn；保存成功才 `submitUserTurn`。已定：可见文本（displayText）就是 objective 原文，不拼 `/goal` 前缀——会话标题与 transcript 应读起来像用户的任务，而 goal 的设置由紧随其后的 notice（含 goal ID 与 attempts 上限）说明。
- 替换已存在 goal 时先发一条 notice 说明旧 goal 被替换（含旧 ID 与旧状态）；替换后 attempts 归零，旧 goal 的 blocked 审计不继承。
- `/goal status`：新 `goalBlock` 渲染 goal 的 ID、objective（多行缩进）、状态、`attempts used/max`、blocked 审计签名与次数、last reason、created/updated 时间。无 goal 时输出「no active goal」提示与用法。
- `/goal pause`、`/goal resume`、`/goal clear`：迁移后立即保存，保存失败显示错误并说明内存状态已变更但未落盘（不能假装成功）；无 goal 时给出可读拒绝。
- 空闲约束：沿用 `slashCommandRequiresIdle`——`/goal <objective>` 是「开始新工作」，与 `/clear` 同级必须空闲；`status/pause/resume/clear` 属于控制状态，草案 §9 允许在 turn 进行中执行，因此不加 idle 限制。`slashCommandRequiresIdle` 需要按子命令判断，不能用整条 `/goal`。
- 不修改 `internal/agent`；不在状态行/页脚添加常驻装饰，`/goal status` 是唯一展示面。
- 测试：五个子命令各自路径、替换提示、无 goal 拒绝、保存失败回滚、busy 时 `/goal x` 被拒而 `/goal status` 可用、`/help` 与 picker 含 `/goal`、`goalBlock` 渲染包含关键字段。

## 验收标准

- `/goal 修复 internal/session 的失败测试` 立即开始一轮对话，会话 JSON 在任何 provider 请求之前就含 active goal。
- 五个子命令在无 goal、有 goal、终态三种情况下都给出可理解的输出，不 panic、不静默失败。
- `go test ./internal/tui/... ./internal/goal/...` 通过；新增测试用 `llmtest.FakeProvider`，不联网。
