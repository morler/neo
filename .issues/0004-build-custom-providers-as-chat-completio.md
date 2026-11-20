---
id: 4
title: Build custom providers as chat-completions clients
status: closed
priority: high
labels:
    - feature
    - llm
relations:
    blocks:
        - 5
    depends-on:
        - 3
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

## 背景

internal/llm/chatcompletions 已经是可复用的 OpenAI 兼容适配器，internal/llm/openrouter 只是它的薄包装。自定义 provider 应当复用它，而不是为每个网关写新适配器。

## 目标

cmd/neo/provider.go 的 newProvider 在名称既非内置四家、又存在于 cfg.Providers 时，用配置构造 chatcompletions.Client。

## 实现要点

- 新增 internal/llm/custom（或 cmd 内构造器）：ProviderName 使用配置里的 provider 名称，Endpoint 使用 providers.<name>.endpoint，DefaultModel 使用 default_model（可为空，此时必须显式配置 model）。
- api_key_env 为空时不发送 Authorization 头；非空时用 llm.APIKeyFromEnv 读取，缺失时给出"设置 <ENV>"的可执行提示（复用现有引导文案风格）。为此给 chatcompletions.Client 增加"无密钥"模式，避免写出 "Bearer "。
- 重试与超时沿用 retry.NewHTTPClient、MaxRetries=4、BaseDelay=500ms，与 openrouter 一致。
- 已知的自定义 provider 缺失配置时返回清晰错误，不能静默回退到 anthropic。
- 保持 internal/agent 完全不感知 provider 细节。

## 验收标准

- provider: local 能构造出名为 local 的 client，请求打向配置的 endpoint（httptest 断言 path、Authorization 头有无）。
- 未配置 api_key_env 时不带 Authorization；配置了但环境变量缺失时启动即失败并提示变量名。
- 自定义 provider 的 HTTP 错误仍以 llm.HTTPError 单行呈现，且提示指向正确的 key 环境变量。
- go test ./cmd/neo/... ./internal/llm/... 通过。
