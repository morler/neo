---
id: 101
title: Measure the merge rate on the single-tool surface where no top-level tool is available
status: open
priority: medium
labels:
    - performance
relations:
    related-to:
        - 91
        - 97
        - 100
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

`docs/performance.md` 的解释很克制：脚本的**选用率**在多轮提示词改动后已经能起来（计数任务 3/3），但**合并率**——把读、判断、写放进同一次调用——至今为 0（#83 的 2/6、#91 的 1/6、以及 2026-11-25 默认面的"合并仍罕见"）。当前所有已登记的任务都只修**单点失败**（#98 路径、#99 名字空间、#100 重试循环），没有任何任务回答"合并率为什么是零"。

而本轮验证提供了一个此前所有测量都没有的快照：默认 code mode 下的**真实工具面**只有一个工具（`script`），没有 `bash`/`read_file` 可回退，`codeModeCapability` 明确要求"read, decide, and write inside the same call"。在这个面上，模型没有"退回 top-level 工具"的退路，合并与否只能由它自己决定。这是观察合并行为最干净的对照条件，而至今没有数据。

## 目标

在不改任何生产文本的前提下，先把"合并率"变成可用 `NEO_LOG` 从单次运行里判定的事实，再据此决定是否要动提示词。

## 实现要点

- 判定信号（从日志读，不靠人看最终答案）：
  - 一次 `script` 调用内，`SubCall` 序列（`Script.ReportSubCall`，`script.go:334-338`）**同时**含读取类（`read_file`/`grep`/`glob`）与写入类（`write_file`/`edit_file`）事件，记为该次调用"合并"。
  - 检查日志里是否已经有这三类事件的稳定字段；若没有，本任务**只加日志字段**，不改行为（这类元数据属于可观测性，符合 MISSION 的"clear visibility"）。
- 在 #97 的任务集上跑（#97 是为"单条 bash 完不成"设计的，正好也是必须合并的形状）；若 #97 尚未完成，则先用 `docs/performance.md` 里那套"读 2,001 行文件、改一行"的既有 fixture 记录基线，并在 issue 中注明 fixture 的局限（`sed -i` 能一步完成）。
- 报告要包含三件事：合并率、每次运行的 `requests`、以及**未合并那种运行的具体形状**（例如：先单独 `grep`，再单独 `read_file`，再单独 `edit_file`），因为它决定了下一步该修哪一条。
- n ≥ 6，同一 fixture、同一模型、只翻转 `features.codemode` 或 `features.script`。

## 验收标准

- `docs/performance.md` 新增一小节，记录：日期、provider、模型、fixture、n、合并率、requests 中位数与范围。
- 若合并率仍为 0，必须给出**至少一种**实测到的未合并形状，并据此提出**至多一条**后续任务；不要一次开出多条推测性改进。
- 若日志字段不足以判定，本任务交付该字段与对应单测（`SubCall` 事件的可区分性），并在 issue 里说明测量延后。
- 不改 `scriptCapability`/`codeModeCapability` 的任何字节；若测量结论指向提示词，另开 issue 引用本结果。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 备注

本任务是**测量任务**，不是功能任务，因此不设 `depends-on` 代码依赖，但应与 #99 的复测分开归因：先量，再改。这也回应了 #97 提到的"在拿到真正需要合并的任务之前，无法区分'模型不会合并'和'任务不需要合并'"：本轮已经有一个默认面只剩 `script` 的已知条件，值得先用它量一次。
