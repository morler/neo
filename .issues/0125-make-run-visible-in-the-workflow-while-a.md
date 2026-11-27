---
id: 125
title: Make run() visible in the workflow while a script runs
status: open
priority: high
labels:
    - bug
created: "2026-11-27"
updated: "2026-11-27"
---

## 问题

code mode 是默认工具面，子代理实际工作主要经由 `script` 里的 `run([...])` 完成。但 `run()` 在事件流里是隐形的。

`internal/tools/script_run.go:124-133` 的 `report` 只在结束时调用，且固定 `Done: true`，从不发 `Done: false` 的开始事件。于是两条消费路径都丢掉它：

- 协调者侧：`internal/tui/model.go:1411` 的 `handleToolCallEvent` 只在 `EventToolCall` 上调用 `startScriptSubCall`。`run()` 没有开始事件，结束时 `finishScriptSubCall` 在 `m.subCalls` 里找不到这个 ID，不追加任何行。探针（`script` 内先 `tools.read_file` 再 `run(["echo","hi"])`）在 TUI 里只产生 `read_file` 一行。
- 子代理侧：`internal/subagent/runner.go:221-225` 的 `translate` 对 `EventToolResult` 仅在 `IsError` 时产出 `AgentEvent`，成功的子调用完成事件被整条丢弃。端到端探针（`Supervisor.RunAgentPrompt` → `sup.Events`）的事件序列是 `tool body="script"`、`tool body="read ..."`、`tool body="script"`、`text`、`usage`、`done`；`run(["git","status"])` 没有产生任何事件，工作流条目停在 `Subagent · script`，直到 `Subagent finished`。

附带的第二个信息缺口：`internal/subagent/runner.go:237` 的 `summarize` 没有 `script` 分支，顶层 `script` 调用的 detail 就是裸工具名，子代理里显示为 `Subagent · script`。

## 影响

`MISSION.md` 把"agent、工具和子代理正在做什么的可见性"列为第 2 优先级，而 `workflow` 清单是唯一声称提供该可见性的界面。默认面下最常见的动作恰好不可见：跑测试、git、构建。委派回合里用户看到一行 `Subagent · script` 冻结数分钟，无法判断子代理是在推进还是卡死，也看不到失败的检查。

## 验收标准

- `run()` 在启动程序前上报一次 `Done: false`，结束时上报 `Done: true`（失败时 `IsError: true`）。
- `translate` 对带 `GroupID` 的子调用完成事件不再整条丢弃；顶层同名工具保持现有的省略行为。
- `summarize` 认识 `script` 和 `run`，让 detail 说清在跑什么（例如来源首行、`$ prog arg`）。
- `internal/tui/script_rows_test.go` 覆盖 run 行的出现与原地结算；`internal/subagent/runner_test.go` 覆盖 run 事件到达事件流。
- 不改动 `script` 的返回形态，不增加 prompt/固定预算字节数。

## Notes

排查"子代理里运行 script 的操作没有进入可见工作流"时发现。顶层接线（#124）已完成，缺的是 `run()` 这一类的开始事件与子代理流的过滤规则。
