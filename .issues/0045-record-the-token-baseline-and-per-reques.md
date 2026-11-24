---
id: 45
title: Record the token baseline and per-request measurement protocol
status: closed
priority: medium
labels:
    - docs
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`docs/performance.md` 现在只覆盖二进制大小、提示词构造耗时与工具目录构造耗时，完全没有任何 token 维度的基线，也没有说明怎么测。`docs/design/token-budget.md` 第 7 节要求每个阶段都有前后对比，但没有可执行的测量步骤。

同类项目把这件事做成了可复现的流程（killi `docs/eval.md` + `scripts/eval.py`：每个任务报告通过率、新鲜 token、缓存 token、请求数、耗时）。Neo 不需要立刻做完整评测集，但必须先有「怎么量」的书面协议。

## 目标

在 `docs/performance.md` 增加 token 基线一节，说明测量命令、记录格式与当前基线值，使后续每个优化任务都能引用同一口径。

## 实现要点

- 记录当前固定开销基线（来自 issue #38 的实测）：9 个工具定义 5,022 B（≈1,255 tokens），系统提示词含技能目录 3,062 B（≈765 tokens），合计 8,084 B（≈2,020 tokens）。
- 说明估算口径：字符数 / 4，与 `internal/compact.EstimateTokens` 一致；真实值以 provider 返回的 `llm.Usage` 为准。
- 给出测量步骤：先 `go build ./...`，再用 `neo run --json-stream`（依赖 issue #37）跑一个固定提示，记录 `usage` 各分项，最后与基线对照。
- 明确「估算」与「计费」的区别，避免把字节数当账单。
- 表格只放稳定可复现的数字；耗时类同现有约定，仅作参考、不作为 CI 门禁。

## 验收标准

- `docs/performance.md` 新增小节包含：基线表格、测量命令、口径说明、如何更新基线。
- 文档中的字节数与 `cmd/neo/performance_test.go` 的预算常量互不矛盾，并与 issue #38 的实测一致。
- 从 `docs/developer/index.md` 出发两跳内可到达该小节。
- `qlty check --all` 通过（文档改动也要过 markdown lint）。
