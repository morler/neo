---
id: 62
title: Verify the script tool end to end and record the token baseline change
status: wontfix
priority: medium
labels:
    - test
    - docs
relations:
    depends-on:
        - 61
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

前五个任务把机制、可观测性、审批门禁和提示词都做完了，但这个特性的全部价值主张是“减少 provider 往返与重复上下文”。没有前后对比，就无法判断复杂度是否配得上收益——`MISSION.md` 明确要求“不保留复杂度超过已证明价值的功能”，`docs/design/token-budget.md` 规则 2 也要求“Measure before cutting”。

## 目标

用现有工具做一次可信的前后对比，并把结论写进文档；结论可以是“收益成立，保留”或“收益不足，回退”，两者都是合法结果。

## 实现要点

- **基线**：在引入 `script` 之前的提交上，用 `neo run --json` 跑一组真实任务（建议 3–5 个，覆盖：多文件读取后汇总；读取后条件性改写；大文件里定位一处并报告）。记录 `requests`、`usage.input`、`usage.cache_read`、`output`。
- **对比**：同一组任务在 `script` 可用后重跑，记录同样字段。样本量与命令原样写进文档，便于复现。
- **不要只看总量**：分别报告“往返次数”和“是否把大文件内容留在上下文里”。后者是脚本最该改善的部分。
- **端到端测试**：`neo run` 路径一个用例，证明无审批环境下脚本内写操作被拒绝；交互路径由 #60 的集成测试覆盖。
- **文档**：在 `docs/performance.md` 的 Token Baseline 一节追加对比表与结论；`CHANGELOG.md` 的 Unreleased 段记录该特性。
- **回退判据**：若往返次数没有下降，或 token 节省低于 10%，在 issue 里写明并建议回退或收窄范围，不要为了让特性“看起来有用”而保留它。

## 验收标准

- 对比表含：任务描述、命令、`requests` 前后值、input token 前后值、结论。
- 结论明确指向“保留”或“回退”，不含模糊表述。
- `CHANGELOG.md` 与 `docs/performance.md` 已更新。
- `go test ./...`、`go test -race ./internal/...`、`just performance`、`qlty check --all` 全部通过。
- 若判定回退，另开 issue 说明拆除范围，不在本任务里静默删除。

## Closed
已拆分为 #75（基线）+ #76（对比与结论）+ #77（收尾检查）
