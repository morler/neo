---
id: 36
title: Show session token usage with a /usage command
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 38
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`llm.Usage` 已经把 prompt 拆成 `InputTokens` / `CacheCreationTokens` / `CacheReadTokens`（`internal/llm/provider.go:82-96`），并随会话持久化（`internal/session/session.go:49`）。但 TUI 只显示 cwd、分支和后端（`internal/tui/model.go:991-1007`），用户看不到一次会话花了多少 token、多少命中缓存。

这是 `docs/design/token-budget.md` 原则 2「先测量再优化」的第一个落地点：无法观测就无法判断优化是否生效。

## 范围（已拆分）

本任务只做**按需明细**：`/usage` 命令。footer 常驻显示已拆到 #48，两者共用同一个格式化函数与同一份分母口径。

## 实现要点

1. 新增 `/usage` 命令，接入方式与现有命令一致：`internal/tui/model.go:597-615` 的命令分发，加上 `internal/tui/info.go:20-27` 的 `baseSlashCommands` 说明。
2. 输出分项：`InputTokens`、`CacheCreationTokens`、`CacheReadTokens`、`OutputTokens`，再加 `Usage.PromptTokens()` 与命中率（`CacheReadTokens / PromptTokens()`）。
3. 分母必须用 `PromptTokens()`，与 `docs/developer/prompt-caching.md` 的 Token Accounting 一节保持一致；分项互不重叠。
4. 提供一个可复用的格式化函数（`k`/`M` 缩写、一位小数、0 值显示 `0`），供 #48 直接调用，不要写两份。
5. `/clear` 与新会话归零：与 `internal/tui/reset.go:22` 的 `resetConversation` 放在一起。
6. 不改 `internal/agent`：通过既有 `Usage()` 读取，保持 agent loop 无策略。

## 验收标准

- `/usage` 输出包含四个分项与命中率，数字与 `Usage()` 返回值一一对应。
- `PromptTokens()==0` 时不出现 `NaN`、不除零，输出形如「本会话还没有响应」。
- 格式化函数有表驱动测试：`0`、`999`、`1_000`、`12_345`、`1_234_567` 五个输入。
- `go test ./internal/tui/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/cli.md` 补上 `/usage`。
