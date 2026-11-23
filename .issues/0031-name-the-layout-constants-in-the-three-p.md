---
id: 31
title: Name the layout constants in the three picker views
status: closed
priority: low
labels:
    - tui
    - refactor
relations:
    related-to:
        - 29
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`supercov quality` 给 `internal/tui/models.go` 打 6.62、`internal/tui/providers.go` 打 6.78，两个文件唯一或主要命中项都是 `magic_values`。`internal/tui/command_picker.go` 也被同一项命中（6.76）。

## 命中的字面量

模型浏览器（`internal/tui/models.go`）：

- `:198` `padRight(choice.ID, 30)` —— ID 列宽 30
- `:202` `style.Width(max(1, m.width-2))` 与 `truncate(line, max(1, m.width-2))` —— 左右边距 2
- `:242` `for used < m.height-3` —— 底部留 3 行
- `:246` `strings.Repeat("─", max(1, m.width-1))` —— 分隔线宽度
- `:250` `m.width-lipgloss.Width(footer)-lipgloss.Width(status)-2` —— 状态行右对齐留白 2

供应商浏览器（`internal/tui/providers.go`）结构相同但列宽不同：

- `:176` `padRight(choice.ID, 18)` —— ID 列宽 18（模型浏览器是 30）
- `:180` `m.width-2`
- `:208` `for used < m.height-3`
- `:212` `max(1, m.width-1)`
- `:216` `...-2`

命令选择器（`internal/tui/command_picker.go:184-188`）：

    184	if width < 10 {
    185		return 10
    186	}
    187	if width > 24 {
    188		return 24
    189	}

## 目标

把上列字面量提成有名字的常量，使"为什么是 30 / 18 / 3 / 2"可在声明处读到，行为完全不变。

## 实现要点

- 列宽差异是真实的产品意图（`providers` 的 ID 较短），不要强行统一成一个值：分别声明 `modelIDColumn` = 30、`providerIDColumn` = 18。
- `m.height-3`、`m.width-2`、`m.width-1` 属于两个视图共用的布局规则，可在 `internal/tui` 内声明 `pickerFooterRows`、`pickerHorizontalMargin` 之类常量，放在 `internal/tui/picker.go` 与键处理相邻。
- `command_picker.go:184-188` 的 10/24 是 slash 选择器的最小/最大宽度，命名后放回该文件。
- 只用常量替换，不改表达式结构，避免影响 `modelBrowserView`/`providerBrowserView` 的既有快照测试。

## 验收

- `go test ./...` 通过（尤其 `internal/tui` 的视图测试）。
- `golangci-lint` 的 `magic_values` 不再命中上述三条文件（可用 `supercov quality file internal/tui/models.go` 复核评分上升）。
- 无行为变化：`views` 的黄金输出保持不变。
