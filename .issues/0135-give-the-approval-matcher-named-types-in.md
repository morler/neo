---
id: 135
title: Give the approval matcher named types instead of bare strings
status: open
priority: medium
labels:
    - security
created: "2026-11-28"
updated: "2026-11-28"
---

## 背景

`supercov quality --all`（2026-11-28，snapshot `q_3b628ae17288c36e`）在 205 个文件上给出 7.3/10，80 个文件有发现。逐条核实后，**只有一项是真实、可修、且未被历史 issue 覆盖的**：

`internal/approval/matcher.go` 命中 `primitive_obsession 0.82`，而该文件承载的是**安全门控**逻辑：它决定哪些 bash 前缀、工具名需要用户确认。这是全仓最不该用裸字符串表达的概念。

## 核实过程

    $ /tmp/fnlen-bin internal cmd      # go/ast 精确行数，非 awk
    functions > 60 lines: 10 of 922

    $ supercov quality file internal/tui/model.go
      yes  0.71  god_class     yes  0.65  long_method
    （supercov 不提供行号，需自行定位）

已核实**不立项**的条目，避免重复调查：

| 判断 | 结论 |
| --- | --- |
| 安全面 9/83 标记（`injection_sink`、`path_from_input`、`secret_in_source` 等） | 与 #34 的审计逐条一致，全部设计使然或误报。#34 已 wontfix 归档 |
| `long_method`（`tui/model.go`、`agent/agent.go`） | #33 的上半部分已完成（`Update` 206 行 → 19 行分派器，`run` 143 行已拆）。剩余最长 85 行的 `runPreparedTool` 是"装配 context → 守卫 → 执行"的线性串联，各分支已是早返回，拆分收益不抵风险 |
| `god_class`（`tui/model.go` 1820 行） | #29 已处理其触发原因；剩余是 Bubble Tea 单一 model 的固有形态，按视图拆文件需大改且收益未证实 |
| `temporary_field`（`modelChoicesLoaded/Loading`） | 生命周期封闭在 `models.go:69-75` 与 `messages.go:159-169`；`providers.go:108-110` 切换时正确重置两个 flag 与 loader。不是缺陷 |
| 覆盖率 "20 tests made assertions 但无覆盖证据" | 逐条看过：文档/CLI 文本断言（`cmd/neo/*_test.go`）、子进程测试（`TestAgent_ParallelToolPanicChild`）、纯常量表断言（`llm/*/effort_test.go`）。全部正当 |
| `internal/tools` 整包缺席覆盖率 | 已知插桩限制，见 `docs/developer/code-quality.md`。单独测 86.4% |
| 全量 7.3 > 默认 7.0 | 范围变宽的假象。`quality diff` 证明共用的 82 个文件 0 改善 0 退步 |

## 问题

`approval/matcher.go` 用裸字符串表达"工具名"与"bash 前缀"两类语义不同的概念。一个拼写错误不会在编译期暴露，而这里漏判意味着命令未经确认就执行。

## 范围

只改 `internal/approval`：为工具名与命令前缀引入具名类型（或各自的匹配函数），使调用点不能把两者互换。不改批准流程、不改配置格式、不改 TUI。

## 验收标准

- `internal/approval` 的既有测试不改动即通过（`go test ./internal/approval/`）。
- `supercov quality file internal/approval/matcher.go` 不再命中 `primitive_obsession`。
- 新增一条测试：把工具名当作命令前缀传入（或反之）无法编译，或运行时被明确拒绝。
- `go test ./...`、`qlty check --all` 通过。
