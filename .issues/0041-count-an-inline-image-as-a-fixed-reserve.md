---
id: 41
title: Count an inline image as a fixed reserve when estimating context size
status: open
priority: high
labels:
    - bug
relations:
    blocks:
        - 42
    depends-on:
        - 37
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

`internal/compact/summarize.go:153-175` 的 `EstimateTokens` 按 4 字符/token 估算，其中对图片的处理是：

```go
if b.Source != nil {
    chars += len(b.Source.Data)
}
```

`b.Source.Data` 是 base64 编码的图片字节（`internal/agent/image.go:50`）。一张 300 KB 的 JPEG 编码后约 400 KB 字符，被当成 10 万 token；而实际计费约 1–1.5k token。

后果与 killi decision 42/43 记录的故障完全一致：只要读一张截图，估算立刻超过 `TriggerTokens`（默认 200k 窗口 × 0.70 = 140k），`Compact` 在下一轮就触发总结；总结请求本身也可能因为相同的高估而再次超限，形成连续压缩风暴。

## 目标

让估算对图片给出与实际计费同数量级的值，并让「估算」与「真实上报」两条路径使用同一口径。

## 实现要点

- 引入具名常量 `ImageTokenReserve`（建议 1,500，取主流视觉模型单图上限的保守值），在 `EstimateTokens` 中：`b.Source != nil` 时加 `ImageTokenReserve` 而不是 `len(b.Source.Data)`。
- `len(b.Source.Data)` 若还要用于字符统计（例如 `Content` 文本），保持不动，只改图片分支。
- 加注释说明这是估算而非契约，并指出真实值来自 provider 的 `Usage`；一旦 `promptTokens > 0`，`Compact` 就改用真实值（`summarize.go:86-90` 已有此逻辑，不要重复）。
- 与 issue #42 的触发阈值改动解耦：本任务只改估算，不改 `triggerRatio`。

## 验收标准

- 新增表驱动测试：纯文本消息、单张 300 KB 图片消息、图文混合消息，断言估算值落在预期区间，且图片消息的估算不再随 base64 长度线性增长。
- 构造一个「一张大图 + 少量文本」的转录，断言估算低于默认 140k 触发线。
- 现有 `internal/compact` 测试全部通过（覆盖率为 88.8%，改动后不得下降）。
- `go test ./internal/compact/`、`go vet ./...`、`qlty check --all` 通过。
