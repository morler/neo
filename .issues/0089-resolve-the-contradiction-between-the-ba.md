---
id: 89
title: Resolve the contradiction between the base prompt's batch-one-response rule and script being a serial barrier
status: in-progress
priority: low
labels:
    - llm
relations:
    blocks:
        - 91
    related-to:
        - 87
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

基础提示词要求把独立的调用合并到一个回答里：

> Issue independent reads, searches, or inspections together in one response.
> （`cmd/neo/main.go:57`）

而 `Script.ParallelSafe` 恒为 false（`internal/tools/script.go:95`），`internal/agent/agent.go:699` 一遇到它就冲刷并行批次，把它前后可并行的读/搜调用拆成串行段。

**范围要说清**：`bash` 本身也恒为串行（`internal/tools/bash.go` 没有实现 `ParallelSafe`，注册表对未分类工具 fail closed 到串行，见 `internal/tools/tool.go:56-64`）。所以对 `bash` 与 `script` 之间的取舍，并行度不是差异——两者都得独占。真正被这条规则挤压的是“多个独立 `read_file`/`grep` 一趟并发送出”与“一个脚本串行做完”。

也就是说：模型同时遵守两条指令时，最省事的路径是“多工具并发”，而这恰好把依赖型任务也拆成两轮。这不是 `script` 该被改成并发的理由（#66 已按证据判为 wontfix：`Runtime` 需要池，且没有第二个消费者），而是提示词与调度分类之间的表述冲突。

## 目标

让基础提示词与脚本的串行分类不再互相抵消，且不引入并发执行脚本。

## 实现要点

- 首选：在基础提示词的这一句里补一个限定——独立调用可以并行；当后一步依赖前一步的输出时，那是**一个** `script`，不是两次调用。用一句话，不加新章节。
- 该限定只在 `script` 注册时追加（走 `capabilitySections` 同样的机制），默认 floor 的字节不得变化。
- **不要**改 `Script.ParallelSafe`，**不要**恢复 #66 的运行时池。若本任务结论是“确实需要并发脚本”，先写测量证明，再另开 issue。
- 与 #87 的区别：#87 在 `bash`/`grep` 的 schema 里说“何时用脚本”；本任务修的是基础提示词内部的指令冲突。两者可同时存在，但若预算紧张，优先 #87。

## 验收标准

- 单测：`script` 关闭时基础提示词与改动前逐字节相同（用现有 prompt 预算测试的渲染路径）。
- 单测：`script` 开启时，`corePromptByteBudget`（1,400，现 1,282）仍通过。
- 明确记录：本任务不改变任何工具的 `ParallelSafe` 结果（可用断言固定 `tools.Script{}.ParallelSafe(nil) == false`，若尚无此测试则补上）。
- `go test ./cmd/neo ./internal/agent ./internal/tools` 通过。

## 实施记录（2026-11-25）

已实现，`Script.ParallelSafe` 与运行时池均未改动。

**改动**：
- `cmd/neo/main.go`：`chatSystemPrompt` 的收尾句拆为 `chatPromptTail`（原文本，逐字节不变）与 `chatPromptTailScripted`（追加限定）。新增 `hasScript(reg)`，能力段与尾段选择共用它，两者不可能对"脚本是否可用"给出不同答案。
- 限定语（+143 B，只在 `script` 注册时出现）：

  > That batching is for calls that do not depend on each other; when one depends on another's output, that is one script call, not two responses.

**实测字节**（`go test ./cmd/neo -run 'TestFixedPromptFloorBudget|TestScriptFloorBudget| -v`）：

| 项 | 改前 | 改后 |
| --- | ---: | ---: |
| 默认固定地板 | 8,084 | **8,084（不变）** |
| 默认提示词 | 3,062 | 3,062（不变） |
| script 地板 | 9,653 | 9,796 |
| 其中 prompt | 3,682 | 3,825 |

**过程中发现并修正的一处**：首版把尾段拼成 `` ` + chatPromptTail + `\n` ``，多出 1 字节，默认提示词变成 3,063。`TestDefaultPromptKeepsThePlainBatchingRule` 现在以字面量钉死整段默认提示词，这类"重组常量时多一个换行"的偏移会立刻失败。

**测试**：
- `TestDefaultPromptKeepsThePlainBatchingRule`：默认提示词与字面量逐字节相同。
- `TestScriptTieBreakReachesThePromptOnlyWithScript`：`chatSystemPrompt` 以 `chatPromptTail` 收尾（否则替换不成立）；默认提示词不含 "one script call"；带脚本时含限定；两臂只有尾段不同。
- `TestScriptStaysASerialBarrier`（`internal/tools`）：`Script.ParallelSafe` 在 nil 与带 source 两种输入下都为 false，把"本任务不改并行分类"钉成断言。

**文档同步**：`docs/performance.md`、`docs/design/token-budget.md`、`docs/developer/config.md`、`docs/developer/tools.md`、`CHANGELOG.md`、CLI `--script` 帮助、`/codemode status` 统一为 1,712 B（949 schema + 763 prompt）。

**未做**：任务集复测，理由同 #87——它需要真实 provider 与多次运行，且其结果是 #91 的输入。
