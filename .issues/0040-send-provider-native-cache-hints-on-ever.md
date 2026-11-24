---
id: 40
title: Send prompt_cache_key so providers keep a conversation on one cache shard
status: wontfix
priority: high
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

缓存断点目前只有 Anthropic 适配器在放：`internal/llm/anthropic/client.go:126-140` 给 `SystemBlocks` 里带 `Cache` 的块加 `cache_control`，`:177` 给最后一条消息加滚动断点。其余后端完全没有：

- `internal/llm/openai` 会读取 `cached_tokens`（`responses.go:196`、`:422-423`），却从不发送任何缓存提示；
- `internal/llm/chatcompletions` 与 `internal/llm/custom` 同样没有缓存字段；
- `internal/llm/google` 会读 `cachedContentTokenCount`（`client.go:239`），也没有对应处理。

结果：这些后端上的命中率完全取决于服务端默认行为，无法改进。killi 实测（decision 48）表明，同一网关不标断点命中率为 0。

## 范围（已拆分）

本任务只做**安全、无副作用**的那部分：

1. OpenAI Responses 与 Chat Completions 发送 `prompt_cache_key`（值取会话 ID）。
2. 自定义 provider 的 `providers.<name>.prompt_cache_key` 开关（默认关闭）。
3. `internal/llm` 中立请求类型新增 provider 无关字段（例如 `CacheKey string`），并在注释里写明哪些后端会用。

**`cache_control` 透传已拆到 #50**，因为它会改变请求字节、有成本权衡，需要独立验证。

先确认会话 ID 的稳定来源（`internal/session`）；若某后端需要 header 而非 body 字段，也要在本任务里定下来。

## 实现要点

- `prompt_cache_key` 是幂等提示：不支持的实现会忽略，不会报错，因此可以安全发送。
- 不要在 `internal/agent` 加任何 provider 判断；提示的构造全部落在各适配器的请求构造里，符合 `MISSION.md` 的 provider-neutral 约束。
- 开关关闭时必须完全不发送该字段，而不是发送空值。

## 验收标准

- 每个适配器一个单测：会话 ID 存在时请求体含 `prompt_cache_key`；不存在时字段缺失。
- 开关关闭时序列化结果不含该字段（字符串断言）。
- 现有 provider 测试全部保持通过。
- `go test -race ./internal/llm/... ./internal/config/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/config.md` 记录开关、默认值与适用后端。

## Closed
ponytail review: OpenAI 的自动前缀缓存不依赖 prompt_cache_key，它只是路由提示，收益未测。并入观察阶段，有数据再发。
