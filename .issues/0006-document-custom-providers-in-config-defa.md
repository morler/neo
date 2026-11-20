---
id: 6
title: Document custom providers in config defaults, CLI help, and developer docs
status: open
priority: medium
labels:
    - docs
    - feature
relations:
    blocks:
        - 7
    depends-on:
        - 5
created: "2026-11-20"
updated: "2026-11-20"
---

## 背景

docs/developer 与内嵌 default 配置都把 provider 描述成"四个固定值"。加入自定义 provider 后这些说明会误导用户，且 AGENTS.md 要求行为变更时同步文档。

## 目标

让用户只看 neo.yaml 注释、neo help 和 docs/developer/config.md 就能配置一个自建 OpenAI 兼容后端。

## 实现要点

- internal/config/defaults/neo.yaml：新增注释版 providers 示例（含 api_key_env 与 models），说明 provider 名称解析顺序：内置四家优先、其次 providers 映射。
- cmd/neo/main.go 的 usageText：补充自定义 provider 与环境变量说明，避免写着"只能四选一"。
- docs/developer/config.md：新增 "Custom Providers" 小节，含完整字段表（endpoint、api_key_env、default_model、models）、校验规则与安全注意（endpoint 为明文 http 时的提醒）。
- docs/developer/guides/providers.md：在 "Current Providers" 后补充自定义 provider 的构建方式与适用范围（仅 OpenAI 兼容 Chat Completions）。
- docs/developer/architecture.md 的模块表与启动流程第 2 步补充自定义 provider。
- CHANGELOG.md Unreleased 增加 Added 条目。

## 验收标准

- 文档中的 YAML 示例可直接粘贴运行并通过 config 校验。
- 明确写出当前只支持 OpenAI 兼容 Chat Completions 协议，其他协议（如 Anthropic 兼容网关）暂不支持。
- 文案与实现一致，无残留"只能四选一"的表述。
