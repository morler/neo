---
id: 68
title: Emit sub-call events from script tools through a context sink
status: in-progress
priority: high
labels:
    - feature
relations:
    blocks:
        - 69
    depends-on:
        - 67
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

原 #59 的第 1 步。`MISSION.md` 优先级 2 要求用户看得见工具在做什么。脚本内一次跑多个工具，若不透出，就是黑盒。这一步只做**事件产生与归属**，不做 TUI 渲染（下一步单独做，便于分别验证）。

已有先例：`internal/subagent/supervisor.go:151` 的 `attribute()` 用 `tools.CallMetadataFrom(ctx)` 把事件归属到父调用。

## 目标

脚本内每次工具调用产生一对开始/结束事件，携带父调用身份与自身稳定 ID，且由 agent 转成既有事件流。

## 实现要点

- **复用现有身份字段**：`agent.Event` 已有 `GroupID`/`GroupSize`/`GroupPos`，并行组用的就是它。子调用把 `GroupID` 设为父 `ToolUseID`、`GroupPos` 递增即可，**不新增 `ParentID` 字段**，也不新增 `SubCall` 类型。
- **不可反向依赖**：`internal/tools` 不能 import `internal/agent`。复用已有的 `tools.WithCallMetadata`（`internal/tools/execution.go:18`）携带父调用身份；事件转发用一个极小的回调注入即可，不要新开第二个 `ctx` 键。
- 子调用 ID 用 `<parent ToolUseID>#<n>`，`n` 在单次脚本执行内递增；必须有测试证明同一脚本内两次 `read_file` 的 ID 不同。
- **不阻塞执行**：回调调用用 `select` + `default` 丢弃，参考 `supervisor.attribute()`（`internal/subagent/supervisor.go:151`）；丢弃不影响脚本结果（要有测试）。
- 父事件保持现状（一次 `EventToolCall` + 一次 `EventToolResult`），子事件是**附加**，不改变既有事件顺序契约。
- `internal/subagent/runner.go` 的 `translate()` 把子事件转成 `AgentEvent{Kind:"tool"}`。

> ponytail 评审：原计划新增 `SubCall` 结构、`WithSubCallSink` 上下文键和 `Event.ParentID` 字段，共三处自造抽象。其中 `GroupID` 已是同一语义，`WithCallMetadata` 已是同一通道。复用后本任务的改动面从“新增类型 + 新键 + 新字段”缩到“填两个已有字段 + 一个回调”。

## 验收标准

- 单测：脚本内 3 次工具调用产生 3 对子事件，`GroupID` 等于父 `ToolUseID`，`GroupPos` 为 0/1/2，ID 唯一。
- 单测：脚本内工具失败时，对应子事件 `IsError=true`，父事件仍正常结束。
- 单测：sink 丢弃（模拟慢消费者）时脚本返回值不变。
- 单测：`internal/tools` 没有 import `internal/agent`（用 `go list -deps` 或用例外的编译断言）。
- `go test -race ./internal/agent ./internal/tools ./internal/subagent` 通过。
