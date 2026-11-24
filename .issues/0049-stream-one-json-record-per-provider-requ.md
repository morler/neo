---
id: 49
title: Stream one JSON record per provider request from headless runs
status: wontfix
priority: medium
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #37 拆出的第二半。`--json` 只给汇总，能回答「这次花了多少」，但回答不了「哪一轮最贵」。`docs/design/token-budget.md` 第 7 节需要一个逐请求的记录流，才能定位是首轮写入贵，还是后续未命中。

## 前序

#37 已完成，必须提供：
- `headlessResult` 上的 `usage` 与 `requests` 字段；
- 一个可观察每次 provider 调用用量的机制（新增 `agent.EventUsage` 或等价物）。

本任务只做流式输出，不重复实现事件。

## 实现要点

1. 新增 `--json-stream` 开关，与 `--json` 互斥；同时出现时退出码 2，并在 `printUsage` 的用法文本里补一行。
2. stdout 每行一个 JSON 对象，字段固定：
   - 请求记录：`{"kind":"request","turn":N,"provider":"...","model":"...","usage":{...}}`
   - 结束记录：`{"kind":"result", ...headlessResult 的全部字段}`
3. 请求记录在每次 provider 返回后立即写出并 `Flush`，长任务要能中途观察；不要让缓冲把记录攒到最后。
4. 诊断、警告仍走 stderr，stdout 严格保持每行一个完整 JSON（沿用现有 `stdio` 约定）。
5. 非 JSON 模式（既无 `--json` 也无 `--json-stream`）的输出不得改变，现有用户脚本不受影响。

## 验收标准

- `llmtest` 桩 provider 跑两轮工具调用：恰好 2 条 `request` + 1 条 `result`，且 `request` 的 `usage` 之和等于 `result.usage`。
- 每行都能被 `json.Unmarshal` 成功；没有任何非 JSON 行混入 stdout。
- `--json --json-stream` 同时出现时退出码为 2。
- 不带新开关时输出与改动前逐字节一致（用既有测试锁定）。
- `go test ./cmd/neo/`、`go vet ./...`、`qlty check --all` 通过。

## Closed
ponytail review: 汇总用量已足够定位成本，#json-stream 逐请求流无证据支撑。真要逐轮明细时用 --json 加 Usage() 差分即可。deferred，等 #36 有真实数据再评估。
