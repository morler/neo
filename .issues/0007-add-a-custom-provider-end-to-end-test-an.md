---
id: 7
title: Add a custom provider end-to-end test and doctor guidance
status: closed
priority: low
labels:
    - feature
    - llm
relations:
    depends-on:
        - 6
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

## 背景

前四个 issue 完成后，需要一个端到端用例证明"改一次 neo.yaml 就能用自建网关"这条路径在真实启动流程里成立，而不是只在单元测试的构造函数里成立。

## 目标

用 httptest 起一个假的 OpenAI 兼容服务，通过完整配置加载 + provider 构造 + 一次 agent 轮次，验证自定义 provider 通路。

## 实现要点

- 新增 cmd/neo 下的集成测试：写入临时 neo.yaml（provider + providers.endpoint 指向 httptest server），调用与 runChatSession/runHeadless 相同的解析函数，断言请求命中假服务、响应被正确解析为 assistant 文本、usage 被记录。
- 覆盖工具调用往返：假服务返回一次 tool_calls，Neo 执行后再次请求并拿到最终文本。
- 覆盖失败诊断：假服务返回 401 时，错误信息包含自定义 provider 名与 api_key_env 名称。
- 若某个解析函数目前是包内私有且难以独立测试，允许为其抽出可测的小函数，但不改变现有调用行为。

## 验收标准

- 新增测试在离线环境可运行（不访问外网）。
- go test ./... 与 just check（若存在）通过。
- 测试失败时能指出是请求形状、鉴权头还是响应解析的问题。
