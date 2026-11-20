---
id: 10
title: Persist the model chosen with /model to the config file
status: closed
priority: high
labels:
    - feature
created: "2026-11-20"
updated: "2026-11-20"
closed: "2026-11-20"
---

「/model 切换模型后要持久化到配置里」

现状：/model 只对当前会话切换模型（cmd/neo/main.go 的 switchModel 只改 agent 与 providerModels），不写配置文件；/provider 已经通过 config.WriteBackend 写回 provider: 和 model:。结果：重启后会退回配置文件里的旧 model。

期望：/model 选中模型后（模型切换成功、persistSession 成功）把 model: 写回当前加载的配置文件；provider: 保持不变（与当前运行的后端一致）。嵌入默认值使运行时应创建 ~/.neo/config.yaml。写文件失败时要报错并提示，不能假装已保存。

范围：
- internal/config：新增只写 model 的持久化入口，复用现有行级重写逻辑。
- cmd/neo：switchModel 成功后调用持久化；失败时返回错误让 TUI 展示。
- 测试：config 单测 + cmd 层针对 switchModel 的测试。
- 文档：docs/developer/config.md、cmd/neo/main.go usage 里only
