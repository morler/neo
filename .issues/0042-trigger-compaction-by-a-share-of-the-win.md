---
id: 42
title: Trigger compaction by a share of the window with a growth floor
status: open
priority: high
labels:
    - feature
relations:
    blocks:
        - 43
    depends-on:
        - 41
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

压缩触发目前是固定比例：`internal/compact/summarize.go:17` 的 `triggerRatio = 0.70`，配合 `DefaultContextWindowTokens = 200_000`，得到固定 140k 触发线。配置侧只有 `compaction.context_window_tokens` 一个覆盖项（`internal/config/config.go:109-112`），在 `cmd/neo/main.go:1014-1016` 转成 `TriggerTokensForContextWindow`。

两个具体问题：

1. **窗口越大越吃亏**：1M 窗口的模型仍然在 140k 就压缩，等于只用了七分之一，而且此时前缀几乎全在缓存里（killi 实测 96% 缓存时压缩是纯亏）。
2. **压缩后立刻再压缩**：压缩把转录削到 `KeepRecent`（20 条）附近，但下一轮只要再涨一点就可能越过同一个固定阈值，形成连续压缩。killi decision 50 引入「20% 增长地板」正是为此。

## 目标

让触发点既尊重模型窗口，又不会在刚压缩完之后立刻再次触发。

## 实现要点

- `TriggerTokensForContextWindow` 保持按窗口比例计算（当前 0.70 合理，保留），但为「已知窗口」与「未知窗口」给出明确不同的默认值；未知窗口仍用 200k。
- 在 `Summarizer` 上记录上一次压缩后的估算规模，`Compact` 在真实/估算规模未超过「上次压缩后规模 × `growthFloor`」时直接返回原转录，跳过压缩。
- 地板的必要性依赖 #41：#41 修掉图片估算假象后，压缩风暴的根因就消失了，地板是对剩余场景的兜底。若实现后发现 #41 已足够，允许只做比例不做地板，并在 issue 里记录这个判断。
- 不要把地板做成隐藏的全局状态：作为 `Summarizer` 的字段并在注释里说明生命周期，或者由调用方显式传入。压缩器当前是值类型，注意方法接收者与状态更新的一致性。
- 阈值以具名常量表达（`triggerRatio = 0.70`、`growthFloor = 1.2`），不新增配置项。
- 保持 `KeepRecent` 与 `SafeSplitPoint` 不变，本次不改「保留多少条」的策略。

## 验收标准

- 测试：同一份转录在压缩一次后，规模仅增长 10% 时不触发第二次压缩；增长 30% 时触发。
- 测试：1M 窗口的 `TriggerTokensForContextWindow` 结果显著高于 200k 窗口的结果，且都等于窗口 × 配置比例。
- 常量有注释写明取值理由与测量来源。
- `internal/compact` 覆盖率不下降；`go test ./internal/compact/ ./internal/config/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/config.md` 只记录既有 `context_window_tokens` 与新的默认触发线数值，不写不存在的开关。
