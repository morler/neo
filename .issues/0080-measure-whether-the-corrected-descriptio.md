---
id: 80
title: Measure whether the corrected description and prompt raise script use without raising cost
status: open
priority: high
labels:
    - performance
relations:
    depends-on:
        - 79
    related-to:
        - 76
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

`docs/performance.md:80-100` 与 #76 已测过一轮：三个固定任务下 `requests` 变化 0%，input 上升 6.7%–10.6%，模型全程未调用 `script`。当时的结论是“收窄 + 改进提示词后再测”。

#78 修正了描述中的错误（描述声称没有 write，实际有），#79 改写了提示段。文案已变，必须重新测量，否则无法判断改动是否有效，也无法区分“措辞变好”与“仍有别的障碍”。

## 目标

用同一任务集取得改动前后的可比值，给出明确“保留/回退”判定。

## 实现要点

- 逐字复用 #75/#76 的任务原文、模型、cwd、提示，只改变 `script` 是否可用（关闭基线 vs 开启对照）。
- 记录三项：`script` 调用次数、`requests`、`usage.service.input_tokens`（`neo run --json` 已提供）。
- 每个任务至少跑 3 次，报告中位数与散度；离群时补跑并说明。
- 判据事先写死，避免事后调整：
  - 脚本调用率上升 **且** `requests` 不上升 **且** input 增量不超过固定成本 + ε（固定成本以 `TestScriptFloorBudget` 实测为准）→ 保留。
  - 调用率上升但 input 反而明显上升（说明在被诱导用于无收益任务）→ 回收 #79 的部分措辞，不收窄能力。
  - 调用率不上升 → 说明障碍不在文案，转为排查调用约定可发现性（如是否需要在结果错误里给出正确写法）。
- 结论与数字写进 `docs/performance.md` 对应小节，替换或补充现有表格。

## 验收标准

- 对比表含：任务、命令、`script` 调用次数、`requests` 前/后、input 前/后、相对变化、结论。
- 结论明确写“保留”或“回退/收窄”，不含模糊措辞。
- 数字与预期相反时如实记录，不为特性辩护。
- `docs/performance.md` 已更新，且不与 `docs/design/js-batch-tool.md` §2 的结论矛盾。
