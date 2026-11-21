---
id: 19
title: Add the goal continuation prompt and idle-only TUI scheduler
status: closed
priority: high
labels:
    - feature
    - tui
relations:
    blocks:
        - 20
        - 21
        - 23
    depends-on:
        - 17
        - 14
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## 背景

草案 §7/§9：goal runner 位于 agent loop 之上；`internal/agent` 不理解 goal。一轮结束后，只要 goal 仍 active、TUI 空闲、输入为空、没有 picker/modal、没有待处理的中断，就追加一个「可见的续跑提示」作为普通 user turn。现有 TUI 在 `sendResultMsg` 里已经知道一轮结束（含 queued follow-up 与 quitPending 分支），这是唯一的续跑入口点。

## 目标

实现续跑提示生成与「只在空闲时续跑」的调度规则，并把自动续跑轮标记为 attempt。

## 实现要点

- 新文件 `internal/goal/continuation.go`：`ContinuationPrompt(state State) string`。正文必须包含：
  - 完整 objective 原文（声明为用户提供的任务上下文，不是更高优先级指令）；
  - 要求先收集证据再调用 `goal complete`，说明用什么验证方式（取自 Contract.Verification）；
  - 明确禁止把目标缩小成更小的任务或宣称「基本完成」；
  - 只有在同一阻塞条件反复出现达到阈值后才调用 `goal blocked`；
  - 当前 attempt 计数与上限。
- 续跑 turn 以 `SendWithDisplay(prompt, displayText, nil)` 发送：displayText 使用简短可读文案（如 `↳ goal continuation 3/20`），避免整段提示污染 transcript；agentText 为上面的完整提示。skill 展开对续跑 turn 关闭。
- 调度触发点：`sendResultMsg` 处理完当轮后，若 goal active 且满足全部前置条件（`!m.busy`、`strings.TrimSpace(m.input.Value()) == ""`、`!m.picker.visible`、`!m.files.visible`、`m.approval == nil`、`!m.queued`、`!m.quitPending`、`!m.quitting`），调用 `m.maybeContinueGoal()`；不引入后台 ticker，只在 turn 结束与用户命令结束时检查。
- `ConsumeAttempt` 在**入队前**调用：达到上限则不发送，转而由 #20 输出 handoff（本 issue 只需保证不再续跑并返回「预算耗尽」结果）。
- 续跑 turn 同样走 `submitUserTurn` 的块/统计逻辑，但要避免触发 `m.turn.label` 与 skill 相关分支；`m.workflow = nil` 与 `conversationGeneration++` 的既有行为保留。
- 取消与错误：续跑 turn 失败按普通 turn 处理（errorBlock），不自动重试；`context.Canceled` 不重排续跑。首轮或续跑轮保存失败时同样不续跑：goal 不应在会话未能落盘的状态上前进。
- `/goal resume` 是有意的「现在继续」，成功后立即触发一次调度检查（仍需空闲条件），否则用户要等到下一次 turn 结束才会看到续跑。
- manager 为 nil（headless 或未接线）时 `maybeContinueGoal` 直接返回，不发生任何行为变化。
- 测试（`internal/goal/continuation_test.go` + `internal/tui/goal_scheduler_test.go`）：提示包含 objective 全文与验证/阈值要求；turn 结束后自动发出且 attempts 递增；输入非空、picker 可见、approval 挂起、busy、queued 时都不自动发送；预算耗尽后不发送；`/goal pause` 后不发送。

## 验收标准

- 一个 active goal 在无用户输入时会连续推进，transcript 中每轮都有可识别的续跑标记。
- 用户一开始输入（哪怕只有一个字符）就不再自动续跑。
- `go test ./internal/goal/... ./internal/tui/...` 通过，且不依赖真实时钟或网络。
