---
id: 25
title: Delete the unused apiResponse type in the anthropic client
status: closed
priority: low
labels:
    - refactor
    - deadcode
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`internal/llm/anthropic/client.go:125` 定义了 `type apiResponse struct`，但该包内再无任何引用。

核实结果：

    $ grep -rn "apiResponse" internal/llm/anthropic/
    internal/llm/anthropic/client.go:125:type apiResponse struct {

只有定义这一处，引用数为 1。`git log -S 'type apiResponse'` 显示它自 `9a9fd19 Rewrite in Go` 起存在，流式响应改造后成为孤儿。

## 为什么容易看走眼

同名的 `apiResponse` 在 `internal/llm/openai/` 里是活跃类型（15 处引用），全文搜索时极易误判为「还在用」。

## 目标

删除这段死代码，不改变任何行为。

## 实现要点

- 删除 `internal/llm/anthropic/client.go:125-133` 的 `apiResponse` 结构体定义。
- 确认 `internal/llm/anthropic/client.go:109` 的 `apiUsage` 仍被使用（`apiUsage` 在响应解析里活跃），只删 `apiResponse`。
- 不动 `internal/llm/openai` 中的同名类型。

## 验收标准

- `go build ./...` 与 `go test ./internal/llm/anthropic/` 通过。
- `golangci-lint run --no-config --enable unused ./internal/llm/anthropic/...` 不再报告 `type apiResponse is unused`。
- 无其他行为改动。
