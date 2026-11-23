---
id: 27
title: Deduplicate the model and provider browser key handling
status: closed
priority: low
labels:
    - refactor
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`internal/tui/models.go:97-129` 的 `handleModelBrowserKey` 与 `internal/tui/providers.go:74-106` 的 `handleProviderBrowserKey` 是同一段按键处理。把类型名做机械重命名（models→ent、Model→Entity、providers→ent、Provider→Entity）后，两份代码逐字节相同：

    $ diff <(sed -n 97,129p internal/tui/models.go | sed -E 's/models/ent/g; s/Model/Entity/g') \
           <(sed -n 74,106p internal/tui/providers.go | sed -E 's/providers/ent/g; s/Provider/Entity/g')
    (无输出)

三段共 33 行 × 2：`handle*BrowserKey`、`move*Selection`、`ensure*Selection`。

## 只是部分重复

`filtered*`、`selectCurrent*`、`*BrowserView`、`*ChoiceMatches` 等后续函数**有实质差异**，不能一并合并：

- `selectCurrentProvider` 额外返回 `ProviderState` 并重建 `m.modelChoices` / `m.modelChoicesLoader`（`internal/tui/providers.go:34-45`）。
- `selectCurrentModel` 走 `m.modelSwitcher` 且失败时回退到 `m.ag.SetModel`（`internal/tui/models.go`）。
- `ProviderState` 携带 `Choices` 与 `Loader`，`ModelChoice` 没有对应字段。

因此本次只处理三段真正同构的代码。

## 目标

把三段重复逻辑收敛成一份，同时保持两套 picker 的既有差异行为。

## 实现要点

- 抽出与具体浏览器无关的键处理：ctrl+c/ctrl+d 退出、`quitPending`、esc/up/down/backspace/delete/enter/单字符输入七个分支。
- 通过参数传入差异点（打开/关闭、选区移动、查询增减、选中确认、查询状态读写），不要引入接口或反射。
- `res` 的选取顺序与现有一致：`models_test.go` 与 `providers_test.go` 覆盖两个 picker，行为必须全部保持不变。

## 验收标准

- `go test ./internal/tui/` 通过，`models_test.go`、`providers_test.go` 无需放宽断言。
- `golangci-lint run --config <临时配置启用 dupl> ./internal/tui/...` 不再报告 `models.go:97-129` 与 `providers.go:74-106` 重复。
- 两个 picker 的按键行为（含 esc/enter/退格/可打印字符输入）与改动前一致。
