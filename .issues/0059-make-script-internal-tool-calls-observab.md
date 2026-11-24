---
id: 59
title: Make script-internal tool calls observable in the TUI and subagent streams
status: wontfix
priority: high
labels:
    - feature
    - tui
relations:
    blocks:
        - 60
    depends-on:
        - 58
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

> **已作废（ponytail 评审）**：本任务拆分为 #68（事件产生）与 #69（TUI 渲染）。
> 正文中的 `ParentID`/`SubCall` 设计已被推翻——改用既有的 `GroupID`/`GroupPos`
> 与 `tools.WithCallMetadata`。请以 #68/#69 为准，不要按本文实现。

## 背景

`MISSION.md` 优先级 2 是“清楚看到 agent、工具和子代理在做什么”。#58 的 `script` 工具一次调用内部可能跑 5–10 个工具，但在 Neo 的事件模型里只会出现一次 `EventToolCall`/`EventToolResult`。若不透出子动作，用户看到的是“黑盒跑了一段 JS”，直接违背使命，且失败时无法定位是哪一步出错。

已有先例可复用：`internal/subagent/supervisor.go:151` 的 `attribute()` 通过 `tools.CallMetadataFrom(ctx)` 把子事件归属到父调用（`CallID`/`GroupID`/`GroupPos`），TUI 用 `inflight` 渲染嵌套行。

## 目标

脚本内每次工具调用都产生可见事件，并归属到发起它的 `script` 调用。

## 实现要点

- 在 `internal/tools/execution.go` 的 `CallMetadata` 同层新增一个**子动作事件**承载方式，避免 `internal/tools` 反向依赖 `internal/agent`。建议：`tools.WithEventSink(ctx, func(SubCall))`，由 agent 在派发工具前注入。
- `internal/agent` 在 `runPreparedTool` 里注入 sink，把 `SubCall` 转成既有事件：开始用 `EventToolCall` 的变体（带父 `ToolUseID` 与新字段 `ParentID`），结束用 `EventToolResult`。
- 子调用必须有**自己的稳定 ID**（`<parent ToolUseID>#<n>`），否则 TUI 无法区分同一脚本内两次 `read_file`。
- TUI：`internal/tui/model.go` 的 `handleToolCallEvent`/`handleToolResultEvent` 识别 `ParentID`，把子行挂在父行之下；参考 `docs/parallel-execution-ui/design.md` 的稳定高度与顺序要求——子行必须**预先占位**，不能逐个插入导致布局跳动。
- 简洁模式下的呈现规则：父行保持一行摘要，子行默认折叠；父行失败时展开失败的子行。不要新增面板或边框。
- 子代理路径：`internal/subagent/runner.go` 的 `translate()` 需要把 `ParentID` 事件转成 `AgentEvent{Kind:"tool"}`，保持父子关系可见。
- 背压：sink 调用不得阻塞脚本执行（子代理的做法是 `select` + `default` 丢弃）；被丢弃的事件不能影响正确性，父调用的最终结果必须仍然完整。

## 验收标准

- 单测：一个脚本内调用 3 次 `read_file`，产生 1 个父调用事件、3 对子调用事件，ID 稳定且可区分。
- 单测：子调用失败时，错误归属到正确的子行，父行显示失败态。
- TUI 测试（含 40/80/120 列快照，去色）：子行在组开始时一次性占位，完成时只更新字形与耗时，行高与顺序不变。
- 子代理测试：脚本子事件出现在正确的父调用下，且不串到其他子代理。
- `go test -race ./internal/agent ./internal/tui ./internal/subagent` 通过。
- 慢 UI 导致 sink 丢弃事件时，脚本仍返回正确结果（有测试）。

## Closed
已拆分为 #68（子调用事件契约）+ #69（TUI 渲染）
