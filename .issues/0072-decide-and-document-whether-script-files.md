---
id: 72
title: Decide and document whether script filesystem access uses node:fs or Neo tools only
status: wontfix
priority: medium
labels:
    - security
    - docs
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #60 的第 3 步。moejs 自带 `node` 包，可在 `os.Root` 内沙箱化文件访问，并有 `FSOptions.Allow` 回调、`Write` 开关、路径符号链接解析。

这带来一个真实的两难：

- **只用 Neo 工具**（当前状态）：审批、截断、路径语义与顶层调用完全一致，但脚本无法流式处理大文件（只能整块读入字符串）。
- **同时开放 `node:fs`**：脚本处理大文件更自然，但同一份数据有两条访问路径，审批规则可能不一致（一条看 JS 路径，一条看解析后路径），是典型的策略分裂。

这一步不改功能，只做**决策并把它写进文档**，避免以后有人顺手打开 `node:fs` 造成门禁旁路。

## 目标

在 `docs/design/js-batch-tool.md` 中明确结论，并在代码里加上阻止误开的护栏。

## 实现要点

- 评估两个选项，至少覆盖：审批一致性、路径语义、大文件处理、未来的 `fs/promises` 与事件循环成本。
- 若结论是“不开放”：在 `internal/tools/script.go` 的运行时构造处加注释，并在测试中断言 `node:fs` 不可用（import 失败或 `require` 不存在）。
- 若结论是“开放”：必须要求 `FSOptions.Allow` 与 `tools.WithApproval` 走**同一判定函数**，并写测试证明两边对同一路径给出一致结论。
- 无论哪种结论，都要写明“`bash` 永不进入脚本白名单”及其理由（Neo 不解释 shell 语义，`internal/tools/bash.go` 也不做只读判定）。

## 验收标准

- 设计文档有明确结论段落，不用“视情况而定”。
- 若选择不开放：测试证明脚本内无法访问 `node:fs`。
- 若选择开放：测试证明 `Allow` 与审批对同一路径判定一致。
- 结论与 #71 的最终白名单一致（文档与实际能力不脱节）。

## Closed
ponytail 评审：#71 的白名单已事实性定死结论（仅 Neo 工具、无 node:fs）。降级为 #71 的一条验收标准加一行注释，不单开决策任务
