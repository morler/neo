---
id: 21
title: Pause an active goal when the user interrupts a turn
status: open
priority: high
labels:
    - feature
    - tui
relations:
    blocks:
        - 23
    depends-on:
        - 19
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

草案 §9：Esc / Ctrl-C 取消进行中的 turn 时应当 pause goal，而不是取消后立刻再排一轮。Hermes 的经验也是「Ctrl-C 暂停 goal，不要重新排队工作」。当前 `handleEscape` 只调用 `m.sendCancel()`；`requestQuit` 与 `/quit` 走退出路径；`sendResultMsg` 在 `context.Canceled` 时只显示 "turn canceled"。

## 目标

用户中断一轮后 goal 变为 paused、不再自动续跑，并给出「如何恢复」的可见提示。

## 实现要点

- `handleEscape` 取消 turn 时，若存在 active goal：调用 `mgr.Pause(reason)`，标记本轮为「用户中断」以免 `sendResultMsg` 再触发续跑；迁移后立即保存并显示 notice（含 `/goal resume` 提示）。
- `sendResultMsg` 的取消分支：若本轮是因用户中断导致的取消且 goal 已被 pause，不再重复 pause；若取消来自其他原因（如 provider 关闭、上下文取消）也按 pause 处理，保持「非正常结束不自动续跑」的一致规则。
- Ctrl-C 退出路径（`requestQuit` → 取消 → `sendResultMsg` → 退出）同样 pause，但不要在退出流程里因保存失败而阻塞（沿用现有 quit 的保存失败处理）。
- 直接 `!` 命令（`m.turn.direct`）不是 agent turn，取消它不改变 goal 状态。
- 已完成、已 blocked、已 cleared、budget_limited 的 goal 在中断时保持原状态。
- 测试：Esc 中断后状态 paused、`AfterSend` 被调用、notice 出现、随后 turn 结束不自动续跑；paused 状态下 `/goal resume` 后调度器恢复；中断一个 direct `!` 命令不改变 goal；无 goal 时中断行为与现在完全一致。

## 验收标准

- 中断一次自动续跑后，transcript 里能看到「goal paused」与恢复方式。
- 不会出现「取消后立刻又开一轮」的循环。
- `go test ./internal/tui/... ./internal/goal/...` 通过。
