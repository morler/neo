---
id: 133
title: Measure Go source outlines before adding an index capability
status: open
priority: low
labels:
    - performance
    - tools
created: "2026-11-27"
updated: "2026-11-27"
---

## 背景

Maki 的 `plugins/index/` 用 tree-sitter 返回源码骨架和精确行号，让模型先定位再窄读正文。Maki README 自报的净节省 165 token/轮不是 Neo 的基线。Neo 的 `grep` 加 `read_file` 已能高效处理已知符号；只有大型 Go 文件的未知落点导航可能有增益。

## 实验范围

用 Go 标准库 `go/parser` / `go/token` 做一个只读实验，输出函数、方法和类型的签名或名称及起止行号，不返回实现正文。通过现有 script 路径或离线测试工具验证，先不注册常驻顶层工具，不引入多语言 tree-sitter 或新运行时，保持 CGO_ENABLED=0 的构建约束。可复用 #132 的度量方法。

## 验收标准

- 用可复现 fixture 验证声明的起止行号能用于 `read_file(offset, limit)`，包括多行签名和方法；解析失败时明确报错或回退，不把不完整结果当完整索引。
- 并列比较骨架加窄读与现有 grep 加窄读：至少覆盖未知落点和已知符号两种任务。报告正确率、工具结果体积、请求总数和模型总输入；计入获取骨架的一轮及额外提示开销。
- 只有在目标任务保持正确率且端到端用量下降时才建议引入；没有明显增益则记录结论，不新增默认能力。
