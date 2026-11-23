---
id: 34
title: Record the supercov security audit and why its seven flags are not defects
status: wontfix
priority: low
labels:
    - security
    - docs
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`supercov security` 在 76 个文件里标记 7 个（`destination_from_input` 2、`injection_sink` 2、`path_from_input` 2、`secret_in_source` 1、`unsafe_code_execution` 1），其中 1 处确认到行。逐条核实后**全部是设计使然或误报**。本 issue 记录结论，避免下次评审重复调查。

## 逐条核实

| 文件 | 标记 | 结论 |
| --- | --- | --- |
| `internal/tools/bash.go` | `injection_sink 0.92`、`unsafe_code_execution 0.86` | 该工具的定义就是执行 shell 命令（`internal/tools/bash.go:140` 的 `exec.Command("/bin/bash", "-c", cmd)`）。命令由 `internal/approval/matcher.go` 的门控决定是否放行，属产品设计。 |
| `internal/tools/fs.go` | `path_from_input 0.96`（唯一到行：`:121`、`:138`） | `ReadFile.Run` 接受任意路径且无沙箱，是本地 CLI 的预期能力。 |
| `internal/atomicfile/atomicfile.go` | `path_from_input 0.80` | 路径来自 4 个内部调用方（`internal/auth/store.go:95`、`internal/config/write.go:75`、`internal/session/session.go:367`、`internal/tools/fs.go:345`），不来自模型输入。 |
| `internal/auth/openai.go` | `secret_in_source 0.73` | 命中的是 `internal/auth/openai.go:24` 的 `openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"`。这是 **OAuth 公开客户端 ID**，注释已写明与 Codex CLI 的公开流程一致，不是密钥。 |
| `internal/llm/custom/models.go` | `destination_from_input 0.88` | URL 由用户配置的 `endpoint` 推导（`ModelsEndpoint`），是自定义 provider 特性的本体。 |
| `internal/llm/chatcompletions/client.go` | `destination_from_input 0.72` | 同上。 |
| `internal/tools/search.go` | `injection_sink 0.86` | ripgrep 子进程，参数来自工具调用。 |

## 独立复核

    $ grep -rnE "(sk-[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY)" \
        --include=*.go --include=*.md --include=*.toml --include=*.yml .
    (无输出)
    $ qlty check --all --filter trufflehog
    ✔ No issues

`supercov security patch --base origin/main`：6 个变更文件、2 个被评审、nothing introduced。

## 目标

不改代码。本 issue 作为审计记录归档（`wontfix`），若要防回归可在 `.qlty/qlty.toml` 或 CI 中固定该结论，但当前 `qlty` 与 `gosec` 均已无告警，无需额外基线文件。
