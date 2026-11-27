---
id: 134
title: Measure a tool-result budget for compaction summary requests
status: open
priority: low
labels:
    - performance
created: "2026-11-27"
updated: "2026-11-27"
---

## 背景

Maki 的 `maki-agent/src/agent/compaction.rs` 在构建独立摘要请求时，给待摘要历史中的原样工具结果设预算，超预算的结果在副本中换成占位符。Neo 的 `internal/compact/summarize.go` 已按上下文窗口触发压缩、安全切分并原样保留近期消息，但待摘要旧段目前未经这层减量；大日志会被原样再发一次。与 #42 的压缩触发条件不同，本实验只处理摘要请求。

## 实验范围

在摘要请求的副本上试验有界工具结果预览，优先保留能支持续作的诊断、退出状态与验证结论。不要改正常请求的历史、源会话、最近保留段、触发阈值、后端路由或公开配置。目标是减少摘要这一请求的输入，并降低超窗风险，而非声称所有请求都省 token。

## 验收标准

- 使用含大日志、并列工具结果以及近期未答输入的 fixture，验证摘要请求变小、tool_use/tool_result 配对及顺序有效，原始历史和近期保留段保持不变。stub 测试不访问网络。
- 在同模型同 fixture 下比较未裁剪和预算版，记录摘要请求输入、输出、失败和用摘要继续任务的结果；检查摘要保留用户约束、关键错误、未完成事项和验证结论。
- 只有在输入有明显下降且续作质量无回归时才建议启用；若需补查导致总消耗上升，记录为不引入。
