---
id: 87
title: Put one script hint where the model actually chooses a tool, not only in the system prompt
status: in-progress
priority: low
labels:
    - llm
relations:
    blocks:
        - 91
    depends-on:
        - 86
    related-to:
        - 89
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

缓存前缀在会话中不能变（`docs/design/token-budget.md` 规则 1），所以 `scriptCapability` 那 564 字节只在会话开头说了一次。模型真正做选择时读到的是相邻工具的描述：

- `bash`：`Use for git, tests, builds, file inspection beyond Read.`（`internal/tools/bash.go:45`）
- `grep`、`read_file`、`glob`：各自只描述自己。

两者都不提脚本，于是“要不要包一层脚本”这个判断在决策发生的那一刻没有输入。这是 #80 里模型全程选 `bash` 的最朴素解释。

## 目标

在决策点加一句最短的判据，代价可控且可测。

## 实现要点

- 首选方案：在 `bash` 的 schema 描述末尾加一句，指向“下一步依赖这次输出”的场景并提及 `script`。`bash` 是模型最常选的工具，也是脚本最该替代的路径。
  - 必须只在 `script` 已注册时出现——用 `registryFor` 现有的组合方式决定描述文本，不要让默认 floor 支付这笔字节。
  - 实现上注意 `tools.Bash` 的 `Spec()` 目前是常量文本，加可配置字段时要保证 `registryFor` 是唯一决定处，和它注释里“single decision about the script tool”的说法一致。
- 备选方案（若首选超出预算）：在 `grep` 描述上加同样一句，理由是计数/过滤是最常见的依赖场景。
- 这句话只说“何时”，不复述白名单、不给示例——细节留在 `scriptCapability` 与 `script` 的 schema 里，避免三处漂移。
- **默认 floor 不得增长。** `TestFixedPromptFloorBudget`（8,084 / 8,500）必须不变；`TestScriptFloorBudget`（9,499 / 10,000）允许小幅增长，超出则按 `docs/performance.md:178` 同步预算与文档。

## 验收标准

- 单测：`script` 关闭时，`bash`（或所选工具）的描述**逐字节**与改动前相同。
- 单测：`script` 开启时，描述包含那句指向脚本的判据。
- `TestFixedPromptFloorBudget` 字节数不变；`TestScriptFloorBudget` 通过。
- `go test ./internal/tools ./cmd/neo` 通过。
- 合入后用 #83 的任务集复测一次，数据写进本 issue；不改 `scriptCapability` 本身（那是 #84 的范围），以便归因。

## 实施记录（2026-11-25）

已实现，`script` 之外无任何工具描述变动。

**改动**：
- `internal/tools/bash.go`：抽出 `bashDescription` 常量（原有文本，逐字节不变）与 `bashScriptHint`（98 B）；`Bash` 新增 `ScriptAvailable bool`，仅在为真时追加提示。
- `cmd/neo/main.go` 的 `registryFor`：把 `cfg.ScriptEnabled()` 提取为 `scripted`，同时喂给 `tools.Bash{ScriptAvailable: scripted}` 与 `script` 的注册。仍是唯一决定处。
- `internal/subagent/runner.go` 的 `registryWithOptions`：同样用 `r.Script` 决定，否则"子代理能写脚本却读到朴素 bash 描述"会复现同一个盲点。

**提示文案**（98 B，只说"何时"，不复述白名单）：

> When the next step depends on this output, do it inside one script call instead of a second turn.

**实测字节**（`go test ./cmd/neo -run 'TestFixedPromptFloorBudget|TestScriptFloorBudget' -v`）：

| 项 | 改前 | 改后 |
| --- | ---: | ---: |
| 默认固定地板 | 8,084 | **8,084（不变）** |
| script 地板 | 9,555 | 9,653 |
| 其中 tools | 5,873 | 5,971 |
| 其中 prompt | 3,682 | 3,682（不变） |

增量 98 B 全部落在 `script` 开启的那一臂，默认会话零成本，符合本任务"默认 floor 不得增长"的硬约束。

**测试**：
- `TestBashDescriptionIsUnchangedWithoutScript`：关闭时描述与字面量逐字节相同（字面量写死，逼迫常量改动必须是一次显式决定）。
- `TestBashDescriptionNamesTheDecisionPoint`：开启时以 `bashDescription` 为前缀，含 "next step depends" 与 "script"，且不含 `tools.`/白名单工具名。
- `TestBashHintFollowsTheScriptTool`（cmd/neo）：`registryFor` 在两臂分别选出朴素/带提示的描述。
- `TestBashHintFollowsTheChildsScriptTool`（subagent）：子代理两臂同样分离。

**文档同步**：`docs/performance.md`、`docs/design/token-budget.md`、`docs/developer/config.md`、`docs/developer/tools.md`、`CHANGELOG.md`、CLI `--script` 帮助、`/codemode status` 文案，统一为 1,569 B（949 schema + 620 prompt），并注明其中 98 B 只在 script 注册时出现。顺带把 issue 正文里过时的 564 B / 9,499 换成 620 B / 9,653。

**未做**：任务集复测。它需要真实 provider 与多次运行（#86 已证明 n=3 无法分辨 token 差异），代价高于改动本身，留作下一步；#91 的启动条件依赖这次复测的结果，不宜由本任务顺带假装完成。
