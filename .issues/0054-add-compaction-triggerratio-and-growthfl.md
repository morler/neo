---
id: 54
title: Add compaction trigger_ratio and growth_floor to config
status: closed
priority: medium
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #42 拆出的第一半：**只做配置管道与校验**，不改变任何触发行为。

现有配置侧只有一个覆盖项 `compaction.context_window_tokens`（`internal/config/config.go:109-112`），在 `cmd/neo/main.go:1014-1016` 转成 `TriggerTokensForContextWindow`。新增的两个旋钮需要走同一条路径，先把它建好，后续改动才有地方接。

## 前序

#42 已完成压缩行为本身的改动；本任务只把两个数字暴露为配置。

## 实现要点

1. `config.Compaction` 增加 `TriggerRatio float64`（默认 0.70）与 `GrowthFloor float64`（默认 1.2）。
2. 校验：`TriggerRatio` 必须在 `(0, 1)` 开区间；`GrowthFloor` 必须 `>= 1.0`（1.0 表示不设地板）。非法值在加载时返回错误，错误信息带字段名与实际值。
3. 零值语义要明确：字段为 0 表示「未设置」，回落到默认值；不要因 0 而报错，否则现有配置文件全部失效。这一点必须有测试。
4. 在 `cmd/neo` 的构造点把配置传入 `compact.Summarizer`，不要在 `internal/compact` 里读全局配置。
5. 保持 `context_window_tokens` 现有行为不变。

## 验收标准

- 测试：缺省配置得到 0.70 / 1.2；显式设 0 也得到默认值。
- 测试：`trigger_ratio: 1.5`、`trigger_ratio: 0`、`growth_floor: 0.9` 分别加载失败并指出字段。
- 现有配置文件（不含新字段）加载行为不变。
- `go test ./internal/config/ ./cmd/neo/`、`go vet ./...`、`qlty check --all` 通过。

## Closed
ponytail review: 已有 context_window_tokens 覆盖口；行为改动不应同时暴露成两个旋钮。常量先写死在 summarize.go。

## Closed
ponytail review: 配置项砍掉，行为并入 #42，阈值改为包内常量。
