---
id: 29
title: Split the oversized Update and newModel in internal/tui/model.go
status: closed
priority: medium
labels:
    - tui
    - refactor
relations:
    related-to:
        - 30
        - 31
        - 33
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`supercov quality` 全仓 75 文件评分 7.89/10，最低分是 `internal/tui/model.go` 的 **5.18**，命中 `long_method 0.89`、`temporary_field 0.77`、`complex_conditional 0.67`、`god_class 0.65`。这些判断有可直接核对的依据。

## 实测数据

文件 1568 行。两个超长函数：

| 函数 | 起止 | 行数 |
| --- | --- | --- |
| `func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd)` | `internal/tui/model.go:410-615` | **206 行** |
| `func newModel(ctx context.Context, ag *agent.Agent, modelTag, version, workingDir string, sk []skills.Skill, opts Options) *model` | `internal/tui/model.go:301-401` | **101 行** |

`Update` 内含 14 个 `case`：`tea.BackgroundColorMsg`(:415)、`tea.WindowSizeMsg`(:423)、`tea.KeyMsg`(:429)、`tea.PasteMsg`(:441)、`tea.MouseWheelMsg`(:451)、`tea.MouseClickMsg/MouseReleaseMsg/MouseMotionMsg`(:465)、`modelChoicesMsg`(:468)、`agentEventMsg`(:487)、`approvalRequestMsg`(:497)、`sendResultMsg`(:503)、`persistenceRetryResultMsg`(:573)、`spinner.TickMsg`(:584)、`workflowEventMsg`(:593)、`branchMsg`(:596)、`default`(:601)。

`temporary_field` 的可核对来源是 `modelChoicesLoaded`(`internal/tui/model.go:293`) 与 `modelChoicesLoading`(`:294`)：它们的有效生命周期横跨 `internal/tui/models.go:71-77`、`internal/tui/providers.go:111-112`、`internal/tui/model.go:469-479`。

## 目标

把 `Update` 拆成每个消息族一个方法，把 `newModel` 拆成"构造 textarea/viewport/样式"与"装配 model 字段"两段，使任一函数不超过约 60 行，行为完全不变。

## 实现要点

- `Update` 保留类型 switch 作为分派表，每个 case 体抽成 `handleKeyMsg`、`handleAgentEvent`、`handleSendResult`、`handleMouse`、`handleModelChoices` 等方法；`wasBusy` 与 `cmds` 的收集逻辑要在抽取后保持不变（`internal/tui/model.go:411-413` 与末尾 `:605-613` 的收尾代码是唯一能看出"忙/闲状态切换"的地方）。
- `sendResultMsg` 分支最长（`:503-572`，70 行），优先抽出。
- `newModel` 中 textarea 与 viewport 的初始化为独立的 `newTextarea()`/`newViewport()`；`glamour.NewTermRenderer` 的失败降级（`md = nil`）保持不变。
- 不要顺手改字段名：`m.models.visible`/`m.providers.visible` 被 `internal/tui/goal_scheduler.go:64` 直接读取，`models_test.go`、`providers_test.go`、`reset_test.go` 也直接访问这些字段。
- 拆分后补测此前 0 覆盖的 `With*` 选项构造（见关联 issue）。

## 验收

- `go test ./...` 全部通过，`go test -race ./internal/tui/` 通过。
- 无函数超过 60 行（`awk` 或 gocyclo 可核对）。
- `internal/tui/model.go` 的 `golangci-lint` 与 `qlty check --all` 无新增问题。
