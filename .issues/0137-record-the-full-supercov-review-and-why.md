---
id: 137
title: Record the full supercov review and why most findings are not defects
status: open
priority: low
labels:
    - docs
created: "2026-11-28"
updated: "2026-11-28"
---

## 背景

两份全量评审（2026-11-28，quality snapshot `q_3b628ae17288c36e`、security snapshot `q_4099766b715ed815`）逐条核实后，**大部分判断不成立或已在历史 issue 中处理**。本 issue 记录结论与核对方法，避免下次评审重复调查。

## 不立项的条目（逐条核实）

| 判断 | 核实结论 |
| --- | --- |
| 安全面 9/83 标记 | 与 #34 一致，全部设计使然或误报（`script` 的 eval 是功能本体；`auth/openai.go` 只有 OAuth 公开客户端 ID）。#34 已 wontfix |
| `long_method`（`tui/model.go`、`agent/agent.go`） | #33 上半已完成：`Update` 206 行 → 19 行分派器。剩余最长 `runPreparedTool` 85 行，是线性串联、各分支早返回，拆分收益不抵风险 |
| `god_class`（`tui/model.go` 1820 行） | #29 已处理触发原因；剩余是 Bubble Tea 单一 model 的固有形态，按视图拆文件需大改 |
| `temporary_field`（`modelChoicesLoaded/Loading`） | 生命周期封闭在 `internal/tui/models.go:69-75`、`internal/tui/messages.go:159-169`；`providers.go:108-110` 切换时重置 flag 与 loader。非缺陷 |
| 20 个测试有断言无覆盖证据 | 逐条看过：文档/CLI 文本断言、子进程测试（`TestAgent_ParallelToolPanicChild`）、常量表断言。全部正当 |
| `internal/tools` 整包缺席覆盖率 | 已知 Go 插桩限制（`docs/developer/code-quality.md`），单独测 86.4% |
| 全量 7.3 > 默认 7.0 | 范围变宽假象。`quality diff`：共用 82 文件 0 改善 0 退步 |
| `primitive_obsession` 占 15+ 文件 | 多数是测试文件（45/80 有发现的是测试）。生产侧只保留 `approval/matcher.go` 一处立项 |

## 方法学教训（可复用）

1. **不要用 awk 数函数行数**。按源码行切分会把函数之间的 `const`/`var` 块算进去：`programOptions` 实测 13 行，awk 报 167 行。用 `go/ast` 的 `Pos()`/`End()`，本 issue 的复核用了一个 60 行的 `/tmp/fnlen` 工具。
2. **`supercov quality patch --base HEAD~N` 不可用于本仓**。它按"分支离开默认分支的合并基点"计算，在 `main` 上分支尚未离开，永远回答 `No changed source files`。要看某段提交的增量只能用快照 diff。
3. **`quality diff` 是判断"分数变化是否真实"的唯一手段**。全量把 123 个新文件加进分母，测试文件平均分高于生产代码，总分从 7.0 抬到 7.3 纯属范围变化。
4. **`--all` 会把 `docs/measurements/` 纳入评估**（标为 documentation）。想评产品用默认范围，想看测试质量用 `--all`。

## 目标

不改代码。归档核实结论与核对方法。若日后 supercov 再次报出相同的安全标记，直接引用 #34 与本 issue，不重复调查。
