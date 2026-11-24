---
id: 37
title: Add usage totals to the headless JSON result
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 41
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 目标

让 headless 运行可被脚本消费地报告 token 用量，作为 `docs/design/token-budget.md` 原则 2 的测量底座。

## 范围

**只做汇总，不新增事件机制。** 逐请求记录流（`--json-stream`）与 `agent.EventUsage` 已按评审结论砍掉：`--json` 的总量足以回答「这次贵不贵」，逐轮明细在需要时用 `Usage()` 前后差分即可。

## 实现要点

1. `headlessResult` 增加 `usage llm.Usage` 与 `requests int`。`llm.Usage` 已有 `cache_creation_tokens` / `cache_read_tokens` 的 JSON tag，直接复用，不要另造 DTO。
2. 运行结束时读 `ag.Usage()`，**不修改 `internal/agent`**。
3. `requests` 由 `headlessStats` 现有事件计数扩展：`EventDone` 或每轮响应各计一次，取能稳定对应 provider 调用的那个，并在注释里写明选择。
4. stdout 只放 JSON，诊断走 stderr，沿用现有 `stdio` 约定。
5. 无 `--json` 时的输出不得改变。

## 验收标准

- 用 `llmtest` 桩 provider 跑一次两轮工具调用：`usage` 等于两轮之和，`requests == 2`。
- 带压缩的路径：`usage` 包含压缩调用的用量（与 `agent.Usage()` 的累加口径一致）。
- 既有 headless 测试（非 `--json` 输出）不改动即通过。
- `go test ./internal/agent/ ./cmd/neo/ -race`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/cli.md` 的 `neo run` 一节补充新字段。
