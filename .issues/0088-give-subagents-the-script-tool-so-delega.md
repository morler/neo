---
id: 88
title: Give subagents the script tool so delegated file work can batch dependent steps
status: closed
priority: medium
labels:
    - feature
relations:
    depends-on:
        - 86
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`internal/subagent/runner.go:94-107` 构造子代理注册表，`internal/subagent/supervisor.go:42-43` 决定两种模式的工具名：

```go
dynamicAgentTools = []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob"}
inspectAgentTools = []string{"read_file", "grep", "glob"}
```

两处都没有 `script`，即使 `features.script` 为 true。于是多文件、依赖前一步结果的批处理——恰恰是委派出去最常发生的工作——在子代理里无法脚本化。这是一个能力缺口，不是提示词问题。

## 目标

当会话启用 `script` 时，work 模式的子代理也能用 `script`，行为与顶层一致。

## 实现要点

- `registryWithOptions` 里用与 `registryFor`（`cmd/neo/main.go:424-442`）相同的方式构造 `tools.NewScript(files)`，并复用**同一批 file tools 值**，这样脚本内 `read_file` 与顶层 `edit_file` 共享 `fileState` 的陈旧读保护（见 `internal/tools/script.go` 的 `NewScript` 注释）。
- `script` 是否启用必须由配置决定，不要无条件注册；子代理不接收父会话的 `/codemode` 切换，明确以 `cfg.ScriptEnabled()` 为准。
- inspect 模式：额外注册只读脚本是有价值的（比逐文件读快），但需要先确认 `Script` 在只读白名单下不会暴露写工具。若不满足，inspect 保持不含 `script`，并在本条 issue 里记录理由。
- 子代理的系统提示（`runner.go:16` 的 `dynamicAgentSystemPrompt`）不加脚本教程：`script` 的 schema 已自述，且子代理的提示要保持短。
- 事件：脚本子调用经 `ReportSubCall` 已经能冒泡（#68/#69），确认在子代理流里表现为嵌套而非新增顶层行。`translate`（`runner.go:108`）目前对 `GroupSize == 0 && GroupID != ""` 有专门分支，检查它对脚本父块是否够用。

## 验收标准

- 单测：`cfg.ScriptEnabled()` 为 true 时，work 模式子代理的注册表含 `script`；为 false 时不含。
- 单测：子代理脚本内的 `read_file` 与随后顶层 `edit_file` 共享陈旧读保护（构造一个文件在脚本读后被外部改动的场景，断言 `edit_file` 拒绝）。
- 单测：inspect 模式的工具集符合本 issue 记录的结论。
- `TestFixedPromptFloorBudget`、`TestScriptFloorBudget` 字节数不变（子代理注册表不计入 chat floor）。
- `go test ./internal/subagent ./cmd/neo` 与 `go test -race ./internal/tools` 通过。

## 实现结果（2026-11-25）

- `internal/subagent/runner.go`：`AgentRunner` 新增 `Script bool` 与 `ScriptEnabled()`；`registryWithOptions` 把 file tools 构造一次并同时交给 `tools.NewScript(files)`，因此脚本内的 `read_file` 与随后的 `edit_file` 共享 `fileState`。
- `internal/subagent/supervisor.go`：新增 `toolsFor(mode)`，成为子代理工具清单的**唯一决定处**。work 模式在 `ScriptEnabled()` 为真时追加 `script`；inspect 模式恒不追加。`ScriptEnabled()` 通过可选接口查询 runner，runner 不实现时返回 false（fail closed）。
- `cmd/neo/subagent.go`：`chatAgentTool` 用 `cfg.ScriptEnabled()` 初始化 `Script`，与 `registryFor` 同一决定。
- `cmd/neo/main.go`：`switchCodeMode` 调用新增的 `setRunnerScript`，让 `/codemode on|off` 同时作用于子代理；写盘失败时一并回滚。

实现中发现并修正了一个设计错误：最初在 `registryWithOptions` 里按 `r.Script` 注册 `script`，同时 `dynamicAgentTools` 不含它，而 `Registry.Filter` 只保留清单内的名字——于是两个决定处互相抵消，工具永远不会出现。改为清单权威、`Script` 只回答问题后一致。

验证：

- `TestScriptToolFollowsTheRunnerSetting`：关闭时不注册，开启时注册（走 supervisor 的真实清单）。
- `TestInspectModeNeverGetsScript`：即使开启，inspect 仍只有 `glob/grep/read_file`。
- `TestScriptSharesReadStateWithTheChildsFileTools`：脚本内 `read_file` 后外部改动文件，顶层 `edit_file` 报 “changed since you read it”。
- `go test ./...` 通过；`go test -race ./internal/subagent ./internal/tools` 通过。
- `TestFixedPromptFloorBudget`（8,084）与 `TestScriptFloorBudget`（9,555）字节数不变。

文档：`docs/developer/tools.md` 记录 work 模式子代理可获 `script`、inspect 恒不可得及理由、以及共享 file tools 的保证。

**未做**：子代理系统提示未加脚本教程，`script` 的 schema 已自述，且子代理提示要保持短。
