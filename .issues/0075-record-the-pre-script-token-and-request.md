---
id: 75
title: Record the pre-script token and request baseline on a fixed task set
status: closed
priority: medium
labels:
    - performance
relations:
    blocks:
        - 76
    depends-on:
        - 73
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #62 的第 1 步。这个特性的全部价值是“减少 provider 往返与重复上下文”。没有可比基线，就无法判断复杂度是否配得上收益（`MISSION.md`：不保留复杂度超过已证明价值的功能）。

实测显示基线必须在**脚本可用之前**取得，事后补做不可信。

## 目标

在 `script` 尚未生效的配置下（可用配置开关或临时禁用注册），跑固定任务集，记录 `neo run --json` 的关键指标。

## 实现要点

- 任务集固定 3 个，覆盖三种模式：
  1. **多文件读取后汇总**（例：统计某目录下某模式出现次数）。
  2. **读取后条件性改写**（例：仅当某条件成立时改一处）。
  3. **大文件定位一处并报告**（例：在 2,000 行文件里找唯一命中）。
- 每个任务用同一模型、同一提示、同一工作目录；记录完整命令原文。
- 记录字段：`requests`、`usage.input`、`usage.cache_read`、`usage.output`、是否把大段文件内容留在上下文。
- **每个任务跑 1 次**。`requests` 与 `usage` 是确定性计数的产物；重复运行测到的主要是模型自身的随机性，不是特性效果。若某个任务的结果明显离群，再补跑一次并说明。
- 结果写进 issue，**不要**在此时就下结论。
- 若 `neo run --json` 缺某个字段，先记录缺失，不临时改 CLI。

> ponytail 评审：原计划“3 任务 × 3 次取中位数”共 9 次完整会话，成本高且测的是噪声。改为 1 次，离群再补。

## 验收标准

- issue 中可复现：命令、cwd、模型、3 个任务的输入与各自一组关键数字。
- 基线在 `script` 未注册的构建上取得，并注明构建方式。
- 不修改产品代码（只可能改本地配置/临时分支）。
- 任务集在下一步能原样重跑（命令逐字保存）。


## 实测环境

- 二进制：基线 `/tmp/neo-base`（提交 `3987ed0`，`script` 未注册），对照 `/tmp/neo-with`（提交 `9a78a22`，`script` 已注册）
- 构建：`CGO_ENABLED=0 go build -o <path> ./cmd/neo`
- provider/model：`commandcode` / `deepseek/deepseek-v4.1-flash`
- cwd：`/tmp/measure/ws`（`src/{a,b,c}.txt` 各含 1 行 TODO；`big.txt` 2001 行，第 1501 行含唯一 needle）
- 每次运行前重置 `src/b.txt` 为原始内容
- 每个任务每个版本跑 1 次；任务 3 因首轮明显离群，额外补跑

## 任务原文

1. `Count how many TODO lines exist under src/ across all files, and report the per-file counts. Use the tools available.`
2. `If src/b.txt contains the line TODO two, change it to DONE two. Only make the change when that exact line is present; otherwise change nothing and say why.`
3. `Find the line containing the unique needle marker in big.txt and report its line number and surrounding context. Do not modify the file.`

## 结果

| 任务 | 版本 | requests | input | output | tool_calls |
| --- | --- | ---: | ---: | ---: | ---: |
| 1 | 基线 | 3 | 6,802 | 675 | 3 |
| 1 | 含 script | 3 | 7,026 | 549 | 7 |
| 2 | 基线 | 4 | 8,096 | 325 | 3 |
| 2 | 含 script | 4 | 9,071 | 263 | 3 |
| 3 | 基线 | 3 | 6,425 | 611 | 2 |
| 3 | 含 script（首轮） | 5 | 12,348 | 1,025 | 4 |
| 3 | 含 script（补跑 1） | 3 | 7,283 | — | 3 |
| 3 | 含 script（补跑 2） | 4 | 10,483 | — | 4 |
| 3 | 基线（补跑 1） | 4 | 9,142 | — | — |
| 3 | 基线（补跑 2） | 3 | 6,560 | — | — |

## 观察

- **往返次数没有下降。** 三个任务的 `requests` 在两版之间相同，任务 3 双方都在 3–4 之间波动。
- **input token 没有下降，反而略升。** 任务 1：6,802 → 7,026（+3.3%）；任务 2：8,096 → 9,071（+12%）。这部分是 script schema 与提示段的固定成本。
- **模型几乎没写脚本。** 任务 1 的 `tool_calls` 从 3 升到 7，说明它多调了几次工具，但最终回答里提到的是 “grep 与脚本读取全文”，未显示一次 `script` 调用改变了解法结构。
- **任务 3 的方差大于版本差异。** 同一版本内 3–5 requests 的跨度，比两个版本之间的差距更大。

## 结论

按 #76 预先写死的判据（往返未下降，或 token 节省 < 10% 即建议回退或收窄），本组数据的判定是 **收益未证实**。

需要说明三条限制，避免把结论读得过强：

1. 单次采样，且已验证同一版本内的方差大于版本差异。要得出确定结论，需要每个组合至少 3 次并取中位数。
2. 模型是 `deepseek-v4.1-flash`，一个小模型，可能不会主动使用新工具；换更强的模型结论可能不同。
3. 任务本身偏小（3 个文件、2001 行），没有构造“不写脚本就无法完成”的场景。

因此这不是“特性无用”的证明，而是“在当前条件下收益未观测到”。下一步 #76 应据此给出保留或收窄的建议。
