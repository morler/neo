---
id: 28
title: Cover the untested branches in the shared picker handler
status: closed
priority: medium
labels:
    - tui
    - test
relations:
    related-to:
        - 27
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`internal/tui/picker.go`（supercov 评审新增文件的本地实现）质量评分 7.88 且无命中项，但 supercov 覆盖率显示 **5 行 NOT COVERED、7 个分支只走了一半**。用 Go 原生工具独立复核，结论一致：

    $ go test ./internal/tui/ -coverprofile=/tmp/tui.out
    $ go tool cover -func=/tmp/tui.out | grep picker
    internal/tui/picker.go:26  handlePickerKey    90.0%
    internal/tui/picker.go:61  moveSelection      50.0%
    internal/tui/picker.go:71  clampSelection     85.7%

## 未覆盖路径

| 位置 | 条件 | 说明 |
| --- | --- | --- |
| `internal/tui/picker.go:28` | `key == "ctrl+c" \|\| key == "ctrl+d"` | 只有 ctrl+c 被测试，`ctrl+d` 从不为真 |
| `internal/tui/picker.go:31-32` | `if m.quitPending { return m, nil }` | 提前返回分支从未执行 |
| `internal/tui/picker.go:37-38` | `case "up": t.move(-1)` | 全仓无任何 `KeyUp` 打到 picker |
| `internal/tui/picker.go:42` | `if *t.query != ""` | 假分支未走：无"空查询时退格"的测试 |
| `internal/tui/picker.go:62-64` | `moveSelection` 的 `items == 0` | 空列表回绕未测试 |
| `internal/tui/picker.go:80` | `clampSelection` 的 `items == 0` | 同上 |

验证依据：

    $ grep -rn "KeyUp" internal/tui/*_test.go | grep -c picker
    0
    $ grep -rn "ctrl+d" internal/tui/*_test.go
    (无输出)

## 目标

`internal/tui/picker.go` 三个函数在 `go tool cover -func` 下达到 100%，且不改变任何生产行为。

## 实现要点

- 在既有 `internal/tui/picker_test.go` 中追加用例，沿用现有 `makeTestModel()`、`keyPress()`、`testProviderChoices()` 辅助函数。
- `up` 分支：打开模型浏览器后先 `keyPress(tea.KeyDown)` 两次再 `KeyUp` 一次，断言 `m.models.selected`。
- `ctrl+d`：用 `tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl})`（bubbletea v2 无 `tea.KeyCtrlD`），断言 `m.quitPending` 为真——同文件已有 `ctrl+c` 的等价写法可参照。
- `quitPending` 提前返回：先发 ctrl+c，再发 `esc`，断言浏览器仍然可见。
- 空查询退格、空列表回绕：直接调用 `moveSelection(0, &sel, 1)` 与 `clampSelection(0, &sel)` 断言归零；用真实浏览器路径覆盖 `:42` 的假分支。
- 纯函数可加表驱动测试，无需 TUI 程序。

## 验收

- `go test ./internal/tui/` 通过。
- `go tool cover -func` 对 `picker.go` 三个函数均为 100.0%。
- `gofmt -l`、`go vet ./...`、`golangci-lint run ./...`、`qlty check --all` 均干净。
