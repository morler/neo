---
id: 76
title: Rerun the same task set with the script tool and decide keep or revert
status: in-progress
priority: medium
labels:
    - performance
relations:
    blocks:
        - 77
    depends-on:
        - 75
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

原 #62 的第 2 步。基线已在 #75 取得。现在用完全相同的任务集重跑，得出可比较的差异。

结论允许是“保留”或“回退”，两者都是合法结果——`MISSION.md` 明确要求不保留价值未证实的复杂度。

## 目标

产出前后对比表与明确结论。

## 实现要点

- 逐字复用 #75 的命令、模型、cwd、提示；只改变 `script` 是否可用。
- 同样每个任务跑 1 次；结果离群时补跑一次并说明。
- 分别报告：
  - **往返次数**（`requests`）的绝对与相对变化；
  - **input token** 变化，并区分是“少发了几轮”还是“每轮更小”；
  - **大文件内容是否留在上下文**（定性 + 若可测则定量）。
- 判据（在 issue 里预先写死，避免事后调整）：
  - 往返次数未下降，或 token 节省 < 10% → 建议回退或收窄范围。
  - 往返下降且 token 节省 ≥ 10% → 保留。
- 把结论写进 `docs/performance.md` 的 Token Baseline 一节。
- 若判定回退：**另开** issue 说明拆除范围，不在本任务里静默删除。

## 验收标准

- 对比表含：任务、命令、`requests` 前/后、input 前/后、相对变化、结论。
- 结论明确写“保留”或“回退/收窄”，不含模糊措辞。
- 若数字与预期相反，如实记录，不为特性辩护。
- `docs/performance.md` 已更新。
