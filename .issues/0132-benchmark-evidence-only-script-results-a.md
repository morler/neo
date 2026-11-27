---
id: 132
title: Benchmark evidence-only script results against full output
status: open
priority: medium
labels:
    - performance
    - tools
created: "2026-11-27"
updated: "2026-11-27"
---

## 背景

Maki 的 `code_execution` 将中间结果留在解释器中，只把脚本明确返回或打印的内容送入模型上下文。Neo 默认 codemode 已支持这条路径，但仍可能把多个文件窗口或整段日志拼进最终结果。旧实验（#76、#97、#103）主要比较工具面或合并调用，没有隔离最终回传体积。

## 实验与预期收益

使用现有 `script`，比较直接回传输出与在脚本内筛选后仅回传结论和必要证据（计数、文件位置、少量命中或错误片段）。目标是减少后续请求重带的历史，不把工具结果占比当作可节省比例。先不改常驻 prompt、工具 schema、全局输出上限或执行器。

## 验收标准

- 固定可重复的跨文件查找和长日志定位 fixture，另设已知行号窄读负对照；写明提示、判分证据和两组唯一变更，不拿全文件读取当唯一基线。
- 固定 provider、model、effort 和运行条件，多次运行，报告成功率、请求数、最终工具结果字节及 provider 的 input/cache creation/cache read/output；后端未提供缓存分项则标明。非模型测试使用 stub，不依赖网络。
- 对结果字节变少但补读或失败变多的情况单独报告；只有在任务成功率不降且端到端用量下降时才建议后续产品改动。允许结论为不引入。
