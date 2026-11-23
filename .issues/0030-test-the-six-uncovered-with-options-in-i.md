---
id: 30
title: Test the six uncovered With* options in internal/tui
status: closed
priority: low
labels:
    - tui
    - test
relations:
    related-to:
        - 29
        - 32
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

supercov 覆盖测量（`supercov -- go test ./...`，run `run_4f9932578d890ff4`）报告 `internal/tui/model.go` 有 116 行、17 个函数未执行。用 Go 原生工具定位，缺口集中在 6 个选项构造函数上，且全是 0.0%：

    $ go test ./internal/tui/ -coverprofile=/tmp/tui.out
    $ go tool cover -func=/tmp/tui.out | grep -E "model.go:(72|83|93|97|105|111):"

| 行 | 函数 | 覆盖率 |
| --- | --- | --- |
| `internal/tui/model.go:72` | `WithModelSwitcher(provider string, choices []ModelChoice, fn func(string) error) Option` | 0.0% |
| `internal/tui/model.go:83` | `WithProviderSwitcher(choices []ProviderChoice, fn ProviderSwitcher) Option` | 0.0% |
| `internal/tui/model.go:93` | `WithModelChoicesLoader(loader func(context.Context) ([]ModelChoice, error)) Option` | 0.0% |
| `internal/tui/model.go:97` | `WithWorkflowEvents(ch <-chan workflow.Event) Option` | 0.0% |
| `internal/tui/model.go:105` | `WithVerbose(verbose bool) Option` | 0.0% |
| `internal/tui/model.go:111` | `WithIO(input io.Reader, output io.Writer) Option` | 0.0% |

对照：同文件的 `WithAfterSend`(:50)、`WithGoalManager`(:57)、`WithGoalPersist`(:66) 都是 100%。这 6 个从未被任何测试调用（`grep -rn "WithModelSwitcher(" --include=*_test.go .` 返回 0），生产代码各调用 2 次。

## 目标

补测这 6 个选项，使它们各自在 `go tool cover -func` 下达到 100%。

## 实现要点

- 测试只需断言选项被应用后的字段值，不需要启动 Bubble Tea 程序：构造 `opts := Options{}`，逐个 `With*` 应用于 `opts`，再检查对应字段。
- 也可从 `newModel` 侧验证：给 `newModel(...)` 传入这些选项，检查 `m.modelSwitcher`、`m.providerSwitcher`、`m.modelChoices`/`m.modelChoicesLoader`、`m.workflowEvents`、`m.verbose`、`m.input`/`m.output` 是否就位。后者能顺带覆盖 `internal/tui/model.go:385-397` 的装配段。
- 注意 `WithModelSwitcher` 会走 `normalizeModelChoices(modelTag, choices)`（`internal/tui/models.go:34`），断言时要考虑它可能插入 "Current configured model" 条目。
- `WithWorkflowEvents` 传一个关闭的 channel 即可，不要让它启动 goroutine。

## 验收

- `go test ./internal/tui/` 通过。
- 6 个函数覆盖率均为 100.0%。
- `gofmt -l`、`go vet ./...`、`golangci-lint run ./...` 干净。
