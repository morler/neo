---
id: 84
title: Stop the script prompt from naming the task shape a script loses, and fix the two-turn rule
status: closed
priority: high
labels:
    - llm
relations:
    blocks:
        - 86
    depends-on:
        - 83
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`cmd/neo/main.go:109-124` 的 `scriptCapability` 有两个问题，方向相反但都压低选用率：

1. **首条正例是脚本必输的形状。** 现文：

   > Write a script when a later step needs an earlier result, so the intermediate output stays out of the conversation: **count or test matches rather than reading the list and judging it; run the same check across many files**; read, branch, then write inside one call.

   “run the same check across many files” 恰好是 `bash -c 'for f in …'` 或一条 `grep -r` 一次进程就能做完的事。脚本里的每个 `tools.*` 都是同步宿主调用（`internal/tools/script.go:248` 的 `hostTool`），循环 N 个文件 = N 次串行 ripgrep。模型在这类任务上选 `bash` 在性能上是对的，写进提示词等于教它选一个更慢的路径。

2. **唯一那条负向判据把该省往返的场景也否掉了。** 现文末：

   > Prefer a script over two turns, **not over one read**.

   #80 的实测里，“读一个文件 → 按内容分支 → 改一行”是 `0 / 3` 且 `requests 4 → 4`。这句话最可能被读成“单个文件的事别用脚本”，而它正是 `docs/design/js-batch-tool.md` §1 说的那个缺口：读、条件编辑、检查三步只需要一次往返。

## 目标

让提示段的正例指向“后一步的参数取决于前一步的结果”，而不是“文件数量多”；把负向判据从“几次读”改成“是否存在依赖”。

## 实现要点

- 正例改为按**依赖**描述，至少保留两条可识别形状：
  - 下一步要用的值来自上一步的读出内容（匹配到几处、某行是否存在、某行是什么）。
  - 读后按内容分支再写，读-判断-写在同一脚本内完成。
- 删掉 “run the same check across many files” 这个以文件数量为判据的说法。
- 负向判据改为：“当下一步不需要上一步的结果时，直接调对应工具，不要包一层脚本”。删掉 “not over one read”。
- 保持陈述语气，不写“必须优先使用”（依据 `docs/performance.md:95-96` 与 #79 的结论）。
- 保留现有可跑示例与 `grep` 返回 `"no matches"` 的提醒；示例若改动必须仍能通过 `TestScriptExampleRuns`。
- 不复述工具白名单，只留指向 schema 的一句（#79 已确立，避免两处漂移）。

## 验收标准

- `scriptCapability` 中不再出现以文件数量为收益判据的措辞，也不再出现 “not over one read”。
- 正例至少两条，且都能对应到 #75/#76 任务集里的具体任务（在 issue 里注明对应关系）。
- `go test ./cmd/neo` 通过，含 `TestScriptExampleRuns`、`TestScriptExampleAvoidsAnEmptyStringTrap`、`TestScriptFloorBudget`。
- `scriptFloorByteBudget`（10,000，实测 9,499）不超；若超按 `docs/performance.md:178` 的规则同步预算与文档，不静默放宽。
- 改动后重跑 #83 的任务集，数据写回 #86，不在本任务里下“有效”的结论。

## 实现结果（2026-11-25）

改动落在 `cmd/neo/main.go` 的 `scriptCapability`：

- 判据从文件数量改为依赖：“Write a script when the next call depends on what this one returns: whether a pattern matched, how many times, which line, or what that line says. The saving is the round trip, not the number of files, so read, branch, and write inside one script whenever the branch decides the write.”
- 删除 “run the same check across many files”。
- 负向判据由 “Prefer a script over two turns, not over one read.” 改为 “When the next call does not need this result, call the tool instead of wrapping it.”
- 保留 `grep` 的 `"no matches"` 提醒与可跑示例，未复述白名单。

**理由已按 #83 的证据修正。** 原 issue 把“文件多”当作“脚本必输但被写成正例”的判据；#83 实测发现真正的原因是模型根本不 `read_file`，而是用 `sed -n` / `grep -n` 收窄窗口，所以脚本竞争的是一条已经收窄的 bash 命令。因此本任务的收益不再是“提高选用率”，而是**删掉一条已被证明对脚本不利、且把模型推向更慢路径的判据**。

测试：

- 新增 `TestScriptCapabilityNamesDependencyNotVolume`，锁住三件事：不含 “across many files”、不含 “not over one read”、含依赖判据。
- `TestScriptExampleRuns`、`TestScriptExampleAvoidsAnEmptyStringTrap`、`TestScriptCapabilityDoesNotRestateTheWhitelist`、`TestChatSystemPromptSizeBudgets`、`TestFixedPromptFloorBudget`、`TestScriptFloorBudget` 全部通过。
- floor 由 9,499 增至 9,555（prompt 段 604 → 682），仍在 `scriptFloorByteBudget` 10,000 内；默认 floor 8,084 未变。

文档同步：`docs/performance.md`（预算数字 + 新增“脚本竞争的是收窄后的命令”一节，含 #83 的数据与噪声底）、`docs/design/token-budget.md`、`docs/developer/config.md`、`CHANGELOG.md`、`cmd/neo/performance_test.go` 的字节注释。1,455/604 已不再被引用。

**未做**：本任务不下“改动有效”的结论。#86 的判据需按 #83 记录的噪声底修正——n=3 的 requests/input 差异不可用于判定。
