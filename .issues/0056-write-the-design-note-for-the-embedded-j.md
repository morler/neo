---
id: 56
title: Write the design note for the embedded JS batch tool
status: closed
priority: high
labels:
    - docs
relations:
    blocks:
        - 63
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

在引入可嵌入 JS 运行时（moejs）做批处理工具之前，先把决策、边界和失败模式固定下来，避免实现过程中反复改口径。这份文档同时是后续 (编号见 related) 各项任务的共同依据。

## 目标

新增 `docs/design/js-batch-tool.md`，作为该特性的唯一事实来源，并在 `docs/developer/index.md` 的入口列表中挂上链接。

## 必须写清的内容

文档只写**不会随意翻动的部分**：为什么做、边界在哪。会随实现变化的具体契约不抄进文档，指向对应 issue。

1. **问题**：模型必须看到工具结果才能写下一步参数，导致“读 → 改写 → 验证”跨多次 provider 往返，且大文件内容要重复进入上下文。
2. **方案边界**：一个 `script` 工具，输入是 JS 源码，最后一条表达式的值即输出；只暴露白名单工具；不暴露 `bash`；不允许任意 `import`。
3. **为什么选 moejs**：纯 Go、Apache-2.0、`context` 可中断；同时记录体积 +5.83 MiB 这一成本。
4. **不做的事**（与 `MISSION.md` 对齐）：不是运行时插件生态；不引入第二套任务状态；不改变 `internal/agent` 的 policy-free 边界；不支持长驻 JS 状态跨调用共享；不支持 `async`/顶层 `await`。
5. **许可证**：Apache-2.0 与 MIT 兼容，需保留 NOTICE。与 AGPL 项目（如 `../tura`）不同，本方案不移入任何 AGPL 代码；设计只借鉴公开行为，不复制代码、提示词、schema 文本或测试用例。

具体契约（可观测性、审批、失败形状、值转换细节）在各自任务里定义，文档只留一行链接，避免两份规格同时维护。

> ponytail 评审：原计划要求文档复述 9 项，其中可观测性与审批契约已在 #68/#70 写得更精确，重复会制造同步负担。

## 验收标准

- 文档覆盖上面 5 点，且不包含未落地的承诺（“将来支持 X”必须标注为 non-goal 或后续 issue）。
- `docs/developer/index.md` 有指向该文档的条目。
- `go test ./cmd/neo -run TestDocsLinks` 通过（仓库已有链接检查）。
- 文档里的体积数字与 `docs/performance.md` 一致。
