---
id: 129
title: Contain a panicking tool call so one bad call cannot end the process
status: closed
priority: high
labels:
    - bug
    - tools
created: "2026-11-27"
updated: "2026-11-27"
closed: "2026-11-27"
---

## 问题

`internal/agent/agent.go:1093` 直接调用 `call.tool.Run(ctx, input)`，没有任何 `recover()`。`cmd/` 与 `internal/` 的非测试代码里 `recover()` 出现 0 处（唯一几处在测试与第三方依赖中），所以一个工具实现里的 panic 不是"这次工具调用失败"，而是结束进程。

两条路径都不可接受，但表现不同：

- **串行**：panic 冒泡到 agent 的调用方（TUI 的 event loop 或 headless 主循环）。bubbletea 在 event loop 里有 recover，但那只是把崩溃报出来；未落盘的会话不保证保留。
- **并行**：`runGroup`（`internal/agent/agent.go:865-889`）把并行安全的调用放进 `go func`。goroutine 里的 panic 没有任何上层能 recover，进程直接退出。未捕获的 panic 只展开该 goroutine 的 defer，所以其他 goroutine 的清理（终端恢复等）也不会执行。

## Evidence

临时探针（已删除，未提交）：

串行路径，`Agent.RunTool` 调一个必然 panic 的工具：

```text
=== RUN   TestPanicProbe
    zz_panic_probe_test.go:29: PANIC ESCAPED: assignment to entry in nil map
--- PASS
```

并行路径，同一工具经 `runGroup` 派发（子进程运行，因为 goroutine panic 无法在同进程内断言）：

```text
child exit err=exit status 2
child output (tail): panic: assignment to entry in nil map

goroutine 10 [running]:
github.com/owainlewis/neo/internal/agent.panicTool.Run(...)
github.com/owainlewis/neo/internal/agent.(*Agent).runPreparedTool(...)
```

## 这不是 moejs 的问题

一个容易写错的前提需要先排除：`script` 工具内部**已经**有兜底。moejs 每个可能执行 JS 的方法都 `defer rt.guard(...)`，把穿过引擎的 Go panic 转成 `*moejs.InternalError` 并恢复调用簿记：

```text
promise.go:52   defer rt.guard(&err, r.CallState(), len(rt.argStack))
runtime.go:105  SetGlobal
runtime.go:289,312,654,684,743
script.go:135   RunScript
value.go:22     ToGo
```

实测：host 函数 panic 后 `RunScript` 返回 `InternalError`，同一 runtime 紧接着的 `1+41` 仍返回 42。因此 neo 的 `hostTool`（内部调 `t.Run`）发生 panic 会被引擎兜住，不是本 issue 的范围。

缺口在**工具边界**，且对所有工具成立，不限于 `script`。剩下的暴露面有两个：

1. `Agent.runPreparedTool` → `tool.Run`：本 issue 的主体。
2. `moejs.CompileScript` / `CompileScriptWith` 是包级函数，没有 Runtime 也就没有 guard；`syntax` / `compiler` 的 panic 会直接冒泡。这是引擎内部 bug 的路径，不需要单独处理，但要知道它不在 guard 覆盖内。

## Impact

默认 `MaxParallelTools = 8`。一条模型响应里有两个并行安全的调用，就足以让一个工具实现的 bug 变成进程退出：未落盘的会话丢失，还在跑的子代理一起没。

这与 `MISSION.md` 的 "Make state transitions and failures visible rather than silently recovering or discarding work" 同向——崩溃既不可见，也不保留工作。

## 建议

在工具边界兜底，而不是让每个工具各写一份 recover：

- 把 `call.tool.Run` 包进一个有 `recover()` 的函数，panic 转成 `toolOutcome{isError: true}`，文本包含 panic 值与一句"这是 Neo 的内部错误，原样重试通常无用"。
- 完整 stack 写进 `logx`，**不要**放进工具结果：stack 进 transcript 会污染上下文并泄露本机路径。
- 不要静默吞掉。panic 必须仍在一个看得见的地方留痕（日志 + 事件），否则就变成"悄悄恢复"。

不要做的：在 `main` 里加一个全局 recover 当作答案——进程活下来了，但状态已不可信。

## 验收标准

- 并行组里一个工具 panic 时：进程存活；该位置的结果是 `isError: true` 且文本点名内部错误；同组其它调用正常完成。
- 串行调用同理，TUI 不退出，会话可继续。
- panic 的 stack 出现在日志中，不出现在工具结果里。
- 单测：一个固定 panic 的工具，分别在串行路径与 `runGroup` 并行路径上断言"进程未退出 + isError"。并行那条必须用子进程跑。
- 现有工具行为不变；`go test ./internal/agent` 与 `qlty check` 通过。

## Notes

与"用 wago/wazy 替换 moejs"的评估无关。那次评估的结论是保留 moejs；本 issue 是它顺带暴露出来的独立缺陷。

## 完成记录（2026-11-27）

- 新增 `internal/agent/tool_panic.go`：`runToolSafely` 是 `tool.Run` 的唯一调用点，用 `recover()` 把 panic 转成 `isError: true` 的工具结果（文本含工具名、panic 值、"internal Neo error" 说明），完整 stack 只写 `NEO_LOG`（`"msg":"tool panic"`），不进 transcript。用 `panicked` 标志而非 `recover()` 的返回值判断，兼容 `GODEBUG=panicnil=1` 下的 `panic(nil)`。
- `agent.go:1114` 的 `call.tool.Run` 改为 `runToolSafely`，串行与并行两条路径共用同一边界。
- 测试 `internal/agent/tool_panic_test.go`：串行路径断言 turn 继续、结果为 isError、文本含 panic 值且不含 stack、日志含 stack；并行路径用子进程（`NEO_PANIC_CHILD=1` 重跑自身）断言进程存活、同组健康调用正常返回 `ok`。
- 反证：把 `runToolSafely` 换回 `call.tool.Run` 后两条测试都失败（串行 panic 冒泡、并行子进程 `exit status 2`），证明测试确实覆盖原缺陷。
- 文档：`docs/developer/tools.md` 新增 "A panicking tool call" 一节。
- 验证：`go test ./... -count=1`、`go vet ./...`、`qlty check` 均通过。`qlty check --all` 剩 1 条 `cmd/neo/main.go:150` 的 ripgrep TODO 提示，属既有示例文本，与本改动无关。
