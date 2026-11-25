---
id: 105
title: Remove stale solo-mode references from user-facing docs and CHANGELOG
status: closed
priority: medium
labels:
    - docs
relations:
    related-to:
        - 103
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---


## 背景

ad63f36 已移除 solo：提交信息写明 "Solo is gone as a name and as a separate implementation"，合法值只剩 codemode 与 classic。但仓库里仍有 4 处文档把 solo 当作**当前可用**的配置值（不含 #97 测量记录那条，那已由 #103 覆盖）：

| 位置 | 内容 |
|---|---|
| docs/developer/guides/permissions.md:19 | "Single-tool mode (features.codemode: solo) is a capability choice..." |
| docs/design/js-batch-tool.md:108 | "The opt-in single-tool mode (features.codemode: solo, --codemode=solo) keeps one interpreter per session..." |
| CHANGELOG.md:32-37 | 整条 "Added an opt-in single-tool code mode: features.codemode: solo..." 记为当前新增特性 |
| CHANGELOG.md 同文件 Changed 段 | 又写着 "Solo is gone as a name and as a separate implementation" |

CHANGELOG 内部自相矛盾最严重：同一版本的 Added 段描述了一个不存在的模式，还给出地板字节数 2,970。

另需注意：docs/design/js-batch-tool.md 描述的行为（"keeps one interpreter per session so globalThis state survives"）其实**仍然成立**，只是归属变了——现在 codemode 默认面就有持久解释器（见 cmd/neo/main.go 的 registryFor 与 tools.ScriptSession）。所以这段不是删掉，而是把模式名替换为实际存在的机制。

## 目标

让所有面向用户的文档只描述当前存在的配置值，同时保留正确的历史记录。

## 实现要点

- permissions.md：把 "Single-tool mode (features.codemode: solo)" 改为对当前默认 code mode 的描述，保留"run() 无法被 tool_approvals 匹配"这一实质结论。
- js-batch-tool.md：把 (features.codemode: solo, --codemode=solo) 改为 code mode 的实际表述，保留"一次调用内状态不持久、globalThis 跨调用保留、/clear 后清空"的机制说明。
- CHANGELOG.md：Added 段那条改写为如实的历史条目（说明它曾作为 solo 引入、后被合入 codemode），或移至合适的历史位置并删除已失效的字节数与配置名。不得让同一份 CHANGELOG 同时声称"solo 是新增特性"和"solo 已不存在"。
- 不要修改 docs/performance.md（#103 负责）。
- 不要修改任何产品代码。

## 验收标准

- 全仓库（排除 .supercov/ 与 .issues/）执行 grep -rn solo --include=*.md --include=*.go 后，剩余命中必须是明确标注为已移除的历史叙述，或测试/internal 标识符（如 profile 测试里的 solo.md 是文件名，需单独确认是否为有意保留）。
- grep -rn "codemode: solo" 与 grep -rn "codemode=solo" 没有任何命中。
- CHANGELOG.md 内部不再自相矛盾。
- 文档链接仍然有效：go test ./cmd/neo -run TestDocs 通过。

## 修复记录（2026-11-25）

按 issue 范围为 4 处生效中的文档修正（docs/performance.md 归 #103，未在此改动）：

| 文件 | 改动 |
| --- | --- |
| docs/developer/guides/permissions.md | "Single-tool mode (features.codemode: solo)" 改为 "Code mode"，保留"run() 无法被 tool_approvals 匹配"这一实质结论 |
| docs/design/js-batch-tool.md | 模式名改为 code mode；保留 globalThis 跨调用保留、局部声明每次重置、/clear 后清空等机制说明 |
| CHANGELOG.md Added 段 | 改写为如实的历史条目：说明它曾以 solo 之名引入，该名字已不存在，行为现由 codemode 承担；删去失效的配置名与 2,970 字节地板 |
| cmd/neo/main.go 注释 | codeModeOverride 的注释原写 --codemode=solo，改为 --codemode=<mode> |
| internal/tools/script.go 注释 | bindGlobals / takeConsole 里 "solo" 改为 "code mode" 与 "script" |

验收核对：

- grep -rn "codemode: solo" 与 "codemode=solo" 在全仓库（排除 .supercov/ 与 .issues/）无命中。
- CHANGELOG 剩余两处 solo 均为明确标注"该名字已不存在/已被移除"的历史叙述，不再自相矛盾。
- go test ./cmd/neo -run TestDocs 通过。
