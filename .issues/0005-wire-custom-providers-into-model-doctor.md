---
id: 5
title: Wire custom providers into /model, doctor, sessions, and log redaction
status: open
priority: medium
labels:
    - feature
    - tui
relations:
    blocks:
        - 6
    depends-on:
        - 4
created: "2026-11-20"
updated: "2026-11-20"
---

## 背景

自定义 provider 必须在内置 provider 出现过的每一处保持一致，否则会出现以下问题：/model 里看不到自定义模型、医生检查误判为未知 provider、恢复会话时被当作"provider 未配置"回退、日志泄露自定义网关的 API key。

## 目标

把 cmd/neo/provider.go 中硬编码的 anthropic/openai/openrouter/google 分支改为"内置表 + 自定义表"统一解析。

## 实现要点

- providerModelChoices：自定义 provider 使用 providers.<name>.models 静态列表；模型目录加载器返回 nil（不联网）。normalizeModelChoices 已能补齐当前 model，无需额外处理。
- providerCredentialPresent 与 doctor 的凭据检查：自定义 provider 检查 api_key_env 对应的环境变量；未设置 api_key_env 时凭据检查为 pass，并明确标注"无需 API key"。
- doctorProviderCheck：自定义 provider 存在即 pass，未知名称仍 fail 并列出可用名称。
- sessionBackend：session 中保存的自定义 provider 名称若仍存在于 providers 配置中则正常恢复，否则沿用现有 warning + 回退逻辑。
- internal/logx：自定义 provider 的 api_key_env 也要纳入脱敏集合（当前只读四个固定环境变量），并为自定义 provider 请求中的 Authorization 头保持 Bearer 脱敏。
- headless neo run 与 subagents 走同一条解析路径。

## 验收标准

- /model 在自定义 provider 下展示配置的模型列表与当前 model。
- 自定义 provider 的会话能恢复；配置被删除后恢复会警告并回退到 cfg.Provider。
- neo doctor 对自定义 provider 输出 pass，缺失 key 时指出具体环境变量名。
- logx 测试覆盖自定义 api_key_env 的脱敏。
- go test ./... 通过。
