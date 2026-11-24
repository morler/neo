---
id: 50
title: Pass cache_control through the chat-completions adapter behind a per-model switch
status: wontfix
priority: medium
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #40 拆出的第二半，也是三者里唯一会改变请求字节的部分，所以单独成任务。

killi 实测（decision 48）：同一 LiteLLM-on-Bedrock 网关，不标 `cache_control` 时两次相同请求都全额计费；标了之后第一次写入 8,898 tokens（贵约 25%），第二次全部读回。结论是这必须是**per-model 开关**而不是默认行为——短会话是净亏。

## 前序

#40 已完成，必须提供：
- 自定义 provider 配置解析（`providers.<name>`）；
- `internal/llm` 中立请求类型的扩展位（例如 `CacheKey string`）与既有开关的加载/校验路径。

本任务只加 `cache_control` 透传。

## 实现要点

1. 配置项 `cache_control: true`，可出现在 `providers.<name>` 与 per-model 条目上；默认 `false`。
2. 开启时，`internal/llm/chatcompletions` 在**发送副本**上打标：
   - 系统消息（或第一条 user 消息，取决于该后端形态）一个断点；
   - 最新一条消息的最后一个内容块一个断点。
3. **历史消息逐字节不变**：只修改构造出的请求体副本，绝不修改 `llm.Request.Messages` 指向的切片。这一点要有测试直接断言（深比较改动前后的 `Messages`）。
4. 关闭时不产生任何 `cache_control` 字段——不是空值，而是字段完全缺失。
5. 网关若不认识该字段，可能报错或忽略；在配置文档里写明这是「按需开启」并列出已验证的网关类型，不要做成自动探测。

## 验收标准

- 开关关闭：序列化后的请求体不含 `cache_control`（字符串断言即可）。
- 开关打开：`cache_control` 恰好出现在两个位置，且原始 `Messages` 深比较不变。
- 流式与非流式两条路径都覆盖。
- `go test -race ./internal/llm/... ./internal/config/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/config.md` 记录默认值与「何时该开」。

## Closed
ponytail review: cache_control 只在 Bedrock 类网关后需要，neo 无该场景证据。先看 #40 发 prompt_cache_key 后的命中率。
