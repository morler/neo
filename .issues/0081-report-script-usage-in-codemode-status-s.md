---
id: 81
title: Report script usage in /codemode status so the user can judge the opt-in cost
status: closed
priority: low
labels:
    - tui
    - feature
relations:
    related-to:
        - 78
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`/codemode status` 目前只报告状态与成本（`internal/tui/model.go:1641-1648`）：

    codemode on: the script tool is available (+~1.1 KB prompt per request)

它告诉了用户“每轮多付多少”，但没有任何信号说明“这钱花出去有没有回报”。用户无法判断该开着还是关掉。

这是给人看的信号，不应写进 prompt——把成本-收益框架塞进提示词只会增加每轮的固定字节。

## 目标

让 `/codemode status` 顺带显示本会话 `script` 的调用次数，使 opt-in 决策有数据依据。

## 实现要点

- 计数放在会话运行时（`chatRuntime`，`cmd/neo/main.go`），在 `switchCodeMode` 同层维护，不要在 `Script` 工具内部做全局状态——`Script` 每次调用无状态是刻意设计（`docs/design/js-batch-tool.md` §4）。
- 通过已有的 `tui.WithScriptSwitcher` 通道旁再传一个计数 getter，或在 `tui` 侧用一个可注入的 `func() int`；避免让 `internal/tui` 依赖 `cmd/neo`。
- 显示形如：`codemode on: script would be available ...; used N times this session`。
- 关闭状态下不显示计数（N 无意义）。
- 不改任何 prompt 或 schema 字节。

## 验收标准

- 单测（`internal/tui/codemode_test.go` 风格）：计数为 0 与大于 0 两种状态的 status 文案。
- 单测：`/codemode off` 后 status 不再显示计数。
- `TestScriptFloorBudget` 与 `TestFixedPromptFloorBudget` 字节数不变。
- `go test ./internal/tui ./cmd/neo` 通过。
