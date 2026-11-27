---
id: 126
title: Stop code-mode script from reusing sub-call IDs across run() and tools.*
status: open
priority: medium
labels:
    - bug
created: "2026-11-27"
updated: "2026-11-27"
---

## 问题

`internal/tools/script.go:274` 在 `globals(ctx)` 里为一次执行新建 `calls := &subCallCounter{}`。`bindGlobals`（:605）先 `SetGlobal("tools", s.globals(ctx))`，code mode 下再经 `codeModeExtras(ctx)`（:310-313）第二次调用 `s.globals(ctx)` 取 `run`，于是 `tools.*` 与 `run()` 各持一个计数器，都从 `#1` 开始。

实测（`NewScript`、`CodeMode: true`、`source = tools.read_file({...}); var r = run([...])`，调用 ID `call_p`）：

```text
sub-call id="call_p#1" name="read_file" done=false
sub-call id="call_p#1" name="read_file" done=true
sub-call id="call_p#1" name="run"       done=true
```

TUI 用 `ToolUseID` 作 `m.subCalls` 的键（`internal/tui/model.go:1673`），所以 `run` 的完成事件结算到 `read_file` 那一行。探针里 `read_file` 行被标成 `isErr=true`：一个成功的读操作显示为失败。

## 影响

错配的失败态让用户以为文件读取失败，而真正失败的是 `run()`。`script_rows_test.go` 的孤儿行分支说明这个键必须唯一；`.issues/0068` 的验收也要求"同一脚本内两次同名子调用 ID 不同"。审计承诺（#59/#60：用户能回溯脚本到底尝试了什么）因此失真。

## 验收标准

- 一次脚本执行只有一个 `subCallCounter`，`tools.*` 与 `run()` 共享。
- 单测：同一脚本内先 `tools.read_file` 再 `run`，两者子调用 ID 不同，且 `run` 的失败不结算到 `read_file` 行。
- `TestScriptSubCallsCarryParentGroupIdentity`、`internal/tui/script_rows_test.go` 现有断言仍通过。

## Notes

排查"子代理里运行 script 的操作没有进入可见工作流"时顺带发现；它与 run() 缺开始事件（#125）是两个独立缺陷，可分别修。
