---
id: 58
title: Build the moejs runtime pool and tool bridge in internal/tools
status: wontfix
priority: high
labels:
    - feature
    - security
relations:
    blocks:
        - 59
    depends-on:
        - 57
    related-to:
        - 60
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

#56 定下设计，#57 引入依赖并抬高体积预算。现在需要真正把 JS 运行时装进 Neo，并把白名单工具暴露给脚本。这是整个特性的核心机制层，后面三个任务（可观测性、审批门禁、提示词与文档）都建立在它的接口之上。

## 目标

新增 `internal/tools/script.go`（或 `internal/tools/script/` 子包），提供 `Script` 工具：模型给一段 JS 源码，Neo 在受限运行时里执行，返回结构化结果。

## 实现要点

- **Runtime 池**：`moejs.Runtime` 明确要求“一次只有一个 goroutine 使用”，但 Neo 的并行调度默认 8 并发。必须池化（参考 moejs README 的 `Plugin` 模式：`idle chan *Runtime`），取用前 reset，`Interrupt` 返回后才能回池。
- **编译缓存**：相同源码可编译一次复用；但脚本通常一次一写，第一版可直接编译、不缓存，并在注释里说明取舍。
- **白名单**：只注入只读或明确的工具集，第一版建议 `read_file`、`grep`、`glob`。**不得**注入 `bash`、`write_file`、`edit_file`、`agent`、`goal`、`workflow`——写操作留到审批任务(#59)落地后再评估。
- **工具函数签名**：`tools.read_file({...})` 形式，同步返回字符串；出错时抛 JS 异常（`moejs.NativeFunc` 返回 error 即 throw）。
- **值转换**：入参用 `rt.ToGo`/`ToGoStrict`，出参用 `JSON.stringify` 或直接字符串。禁止把 `*Runtime` 的值跨 runtime 传递（`ErrForeign`）。
- **输出上限**：脚本返回值在宿主侧按 `tools.MaxOutputBytes` 截断，复用现有 marker 语义，不要另造一套。
- **资源限制**：`Options.MemoryLimit` 必须有非零默认值；执行超时用 `context` + `rt.Interrupt`。
- **并发分类**：`Script` 实现 `tools.ParallelTool` 并**默认返回 false**（脚本可写文件、可跑任意逻辑，fail closed 到串行）；只有明确只读的脚本才允许并发，第一版可以直接恒为 false 并在注释说明。
- **不引入事件循环**：第一版只支持同步 API；`node:fs/promises` 与 `await` 不在范围内，脚本里出现时给出清晰错误。

## 验收标准

- 单测覆盖：脚本读取文件并返回摘要；脚本内抛出错误时工具返回 `isError` 且消息可读；语法错误被区分出来；内存超限与超时各自返回可读错误而不是 panic。
- 单测覆盖：`tools` 全局里**不存在** `bash`/`write_file`/`edit_file`（白名单是封闭的，不是“没注册就报 unknown”的偶然结果）。
- 并发测试：`go test -race` 下 8 个并行 `Script` 调用不共享 Runtime、不出现 `ErrForeign`。
- `tools.MaxOutputBytes` 截断有测试；超限输出仍带截断标记。
- `docs/developer/tools.md` 的工具表新增一行，说明 `script` 的用途与限制（写操作与异步脚本不支持）。
- 该任务**不**改提示词预算、不接审批、不发子事件——那是后续任务。

## Closed
已拆分为 #65（骨架与白名单）+ #66（运行时池）+ #67（内存/超时/输出限制）
