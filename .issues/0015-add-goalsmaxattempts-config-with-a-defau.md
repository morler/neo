---
id: 15
title: Add goals.max_attempts config with a default attempt budget
status: closed
priority: medium
labels:
    - feature
    - config
relations:
    blocks:
        - 20
        - 22
    related-to:
        - 16
created: "2026-11-21"
updated: "2026-11-21"
closed: "2026-11-21"
---

## 背景

草案 §8：Phase 1 用 attempt 预算而不是 token 预算，因为 `internal/agent` 还没有 Codex 式的 token 记账。配置入口是 `goals.max_attempts`，默认 20；预算是「自动续跑 turn 数」，`/goal <objective>` 的首次 turn 不计入。

## 目标

`internal/config` 支持可选的 `goals.max_attempts`，非法值在加载阶段报错，默认值集中定义一次，供 CLI、TUI、goal manager 共用。

## 实现要点

- `internal/config/config.go`：新增 `Goals Goals \`yaml:"goals"\``，`Goals{MaxAttempts int \`yaml:"max_attempts"\`}`。
- 常量 `DefaultGoalMaxAttempts = 20`；`Config.GoalMaxAttempts()` 在字段为 0（缺省）时返回默认值，显式负数返回配置错误（在 `parseConfig` 里校验，错误信息指明 `goals.max_attempts` 与文件来源）。
- 不把上限写死成「必须小于某值」；只拒绝负数与非整数解析错误（YAML 解析已有报错）。
- `internal/config/defaults/neo.yaml`：新增带注释的 `goals:` 段，说明 attempt 计数口径（只有自动续跑计数）与耗尽后进入 `budget_limited`。
- `neo.yaml.example` 同步同样的注释示例。
- 不新增 feature flag：Phase 1 的 `/goal` 始终可用，预算只是上限。
- `docs/developer/config.md` 增加 `goals` 小节与字段表；此时文档可以描述「行为随 §14 的 issue 逐步落地」，但字段语义必须与实现一致。

## 验收标准

- 测试：缺省 → 20；显式 `max_attempts: 5` → 5；`max_attempts: -1` → 带 `goals.max_attempts` 的错误。
- 测试：内嵌默认配置可被解析且仍通过既有全部断言。
- 测试：自定义 providers、subagents 等其他字段的解析行为不变。
- `go test ./internal/config/... ./cmd/neo/...` 通过。
