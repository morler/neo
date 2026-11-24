---
id: 55
title: Add a /sessions command to switch chat sessions inside the TUI
status: open
priority: medium
labels:
    - feature
    - tui
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

恢复已保存会话目前只有 shell 路径：`neo sessions` 列出，`neo resume <id>` 恢复（`cmd/neo/main.go:689`）。TUI 内没有任何入口，用户想换线程必须先退出、查 ID、再启动，中断了「一个终端里连续工作」的用法。

现状证据：

- TUI 斜杠命令只有 `/clear`、`/goal`、`/help`、`/model`、`/provider`、`/quit`(`/exit`)、`/usage`（`internal/tui/info.go:20-28`）。
- 恢复链路全在 CLI 层：`resumeSession`（`cmd/neo/main.go:689`）→ `prepareChat`（`cmd/neo/main.go:788`）→ `runChatSession`（`cmd/neo/main.go:719`）。
- `internal/tui` 不导入 `internal/session`，TUI 被刻意做成不感知会话存储。
- `docs/developer/sessions.md` 的 "Resume Surfaces" 只记录了 shell 入口。

## 为什么这不只是加一个 case

TUI 持有的运行时句柄全部在 `Run()` 时通过 `Option` 一次性注入，并被闭包捕获，之后没有替换路径：

| 句柄 | 来源 | 捕获内容 |
| --- | --- | --- |
| `ag *agent.Agent` | `Run` 参数 | transcript、usage、事件处理器、approver、subagent runner |
| `afterSend` / `goalPersist` | `WithAfterSend` / `WithGoalPersist` | `saveSession` 闭包里的 `setup.sess`（`cmd/neo/main.go:723-725`） |
| `goal *goal.Manager` | `WithGoalManager` | 当前会话的 goal 状态 |
| `modelSwitcher` / `providerSwitcher` / `modelChoicesLoader` | 对应 `With*` | `ctx` 与 `rt`（`cmd/neo/main.go:740-744`） |
| `sk`、`cwd`、`workflowEvents` | `Run` 参数 / `WithWorkflowEvents` | 技能集、工作目录、workflow 事件通道 |

换会话等于这些全部重建：transcript、usage、goal 管理器、`os.Chdir` 到目标 cwd、provider 后端、模型目录、技能集、workflow 通道，以及把 agent 的事件处理器与 approver 重新绑定到同一个 `tea.Program`（`internal/tui/model.go:133-149`）。因此不能只替换其中一部分——半切换状态正是本任务要避免的东西。

## 目标

在 TUI 内列出已保存会话并切换过去；切换前保存当前会话，任何失败都保持当前会话原样。

## 范围

- 只做 TUI 内的浏览与切换入口，复用 `internal/tui/picker.go` 的共享浏览器布局（`pickerView`，`picker.go:31-46`）。
- 打开浏览器时用 `store.List(ctx)` 列出会话，不做新的检索语义：正文搜索仍是 `neo sessions search`。
- 命令名为 `/sessions`（先选再切），与 goal 的 `/goal resume` 区分开。
- 不改 `internal/agent` 的策略，不改 `internal/session` 的存储格式。

## 待定的设计决策（实现前必须先定一项并写进说明）

- **A. 进程内重建**：新增 `tui.WithSessionSwitcher(choices, fn)`，并在切换后重新绑定 `ag`/`afterSend`/`goal`/各 switcher。代价是模型字段全部变成可变，易留半切换状态。
- **B. 交接退出**：`/sessions` 只记录目标 ID 并正常退出（persist + quit），由 CLI 层读到该 ID 后重新进入 TUI，等价于自动执行 `neo resume <id>`。代价是一次屏幕重建，但没有半切换状态，且完全复用 `resumeSession`。
- **C. 不做**：维持 shell 路径。按 `MISSION.md` 的「Add extensibility only when it supports a demonstrated workflow」，若没有实测需求可选此项。

倾向 B：符合 MISSION 的「小核心 + 复用既有组合」，不引入新的可变状态机；A 的成本远超一个斜杠命令的收益。

## 实现要点

1. 命令分发在 `internal/tui/model.go:596-620`；`/help` 的列表来自 `internal/tui/info.go:20-28`（渲染见 `info.go:60-80`），两处都要加。
2. 必须 idle-only：把 `/sessions` 加进 `slashCommandRequiresIdle`（`internal/tui/model.go:659-670`），与 `/clear`、`/model`、`/provider`、`/usage` 同档，理由同 `/clear`——它会替换对话状态。
3. 浏览器复用 `pickerView` 与现有 open/filter/close 模式（`models.go:15-21`、`providers.go`），列表列与 `neo sessions` 同源：id、更新时间、模型、标题。
4. 若采用方案 B：`runChatSession` 需要返回「退出后要恢复的会话 ID」并在外层循环；TUI 侧仍不得 import `session`，ID 只作为字符串携带。
5. 切换或退出前先 `persistSession`（`model.go:1386`）；保存失败要报告，并且不继续切换。
6. cwd 变更沿用 `restoreSessionCWD`（`main.go:706`）的既有语义：目标 cwd 不可用时警告并留在当前目录。
7. 文档：`docs/developer/sessions.md` 的 "Resume Surfaces" 补 TUI 入口，`docs/developer/cli.md` 的运行时说明补 `/sessions`，`CHANGELOG.md` 记一条面向用户的变化。

## 验收标准

- `/help` 列出 `/sessions`；turn 运行中执行 `/sessions` 被拒绝，消息与其它 idle-only 命令一致。
- 列表字段与 `neo sessions` 一致，来自同一个 `store.List`。
- 选中当前会话是无操作并给出提示，而不是重载。
- 切换前调用保存；保存失败时不切换并显示错误。
- 目标会话文件已被删除时报错，当前会话保持不变。
- 至少一个 TUI 测试覆盖：列表渲染与过滤、选中当前会话、保存失败中止；若采用方案 B，再加一个覆盖「退出时携带目标 ID」的测试。
- 文档已更新，`go test ./...`、`go vet ./...`、`qlty check --all` 通过。
