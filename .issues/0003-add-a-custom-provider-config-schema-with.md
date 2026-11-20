---
id: 3
title: Add a custom provider config schema with validation
status: closed
priority: high
labels:
    - feature
    - config
relations:
    blocks:
        - 4
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

## 背景

Neo 目前只支持四个内置后端：anthropic、openai、openrouter、google。选择逻辑分散在 cmd/neo/provider.go 的 newProvider、internal/config/config.go 的 knownProvider、cmd/neo/doctor.go 的 doctorProviderCheck 与 internal/logx 的密钥列表里。用户自建的 OpenAI 兼容网关（vLLM、Ollama、LiteLLM、Azure 兼容端点、公司内网网关）无法接入。

## 目标

在 neo.yaml 增加可选的顶层 providers 映射，让用户声明自定义后端；内置四个 provider 的行为与优先级保持不变。

```yaml
provider: local
model: qwen3-coder

providers:
  local:
    endpoint: http://127.0.0.1:8000/v1/chat/completions
    api_key_env: LOCAL_LLM_API_KEY   # 可选；为空表示不发送 Authorization
    default_model: qwen3-coder       # 可选；省略 model 时使用
    models:                          # 可选的静态模型列表，供 /model 使用
      - id: qwen3-coder
        name: Qwen3 Coder
        description: Local coder model
```

## 实现要点

- internal/config：新增 Providers map[string]CustomProvider 结构与 API 协议字段（首批支持 openai_compatible，走既有 internal/llm/chatcompletions）。
- 校验：names must be trimmed、非空、唯一且不与 anthropic/openai/openrouter/google 重名（防覆盖内置）；endpoint 必须是带 scheme 与 host 的 http(s) URL；api_key_env 若存在必须是合法且不与其他 provider 冲突的环境变量名；models 条目 id 非空去重。
- 新增 Config.ProviderConfig(name) 查找自定义 provider；已知 provider 判定从 knownProvider 的硬编码 switch 改为"内置集合 + 自定义集合"的单一函数，供 config、cmd、doctor 共用。
- provider 未声明且非内置时，错误信息要列出可用的内置与自定义名称，并提示去 neo.yaml 的 providers 段声明。
- subagents.provider 同样接受自定义名称。

## 验收标准

- 合法 providers 配置可被解析，Config.ProviderConfig 返回 endpoint/api_key_env/default_model。
- 非法配置（重名内置、空 endpoint、非 URL、非法环境变量名、重复模型 id）各自返回带指名的错误。
- 未定义 provider 的报错包含全部可用名称。
- 既有 internal/config 测试全部通过，新增用例覆盖上述场景。
