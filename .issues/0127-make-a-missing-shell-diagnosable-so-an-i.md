---
id: 127
title: Make a missing shell diagnosable so an inspect child does not burn its budget
status: closed
priority: high
labels:
    - bug
created: "2026-11-27"
updated: "2026-11-27"
closed: "2026-11-27"
---

## 问题

inspect 子代理只有 `read_file`、`grep`、`glob`，没有 shell。这是刻意设计（`docs/parallel-subagents/design.md:119-124`：general shell access for inspect children 属于 out of scope，且不尝试把 shell 命令分类为只读）。缺的几件事让这个限制变成"反复试到超时"：

1. **错误信息毫无引导。** `internal/agent/agent.go:997` 的查找失败只写 `unknown tool: %s`，不列当前可用的工具。模型要查 diff、staged 状态、blame、history 时自然会调 `bash`，拿到一句"不存在"，然后换个参数再试。
2. **协调者不知道 inspect 子代理没有 shell。** `subagentCapability`（`cmd/neo/main.go:96-106`）和 `agent` 工具的 schema（`internal/subagent/supervisor.go:230-234`）只写 "read-only and parallel-safe"，都没说没有 shell。于是协调者会派一个 inspect 子代理去跑测试或看 diff，这从一开始就不可能完成。
3. **子代理自己也不知道。** `dynamicAgentSystemPrompt`（`internal/subagent/runner.go:16-18`）只说"用需要的工具"。

对照 `internal/tools/search.go` 在缺 `rg` 时的做法（`docs/developer/tools.md:164-168`）：它明确不提示改用 `bash`，因为 "an inspect subagent has no shell, so suggesting one would only waste a turn"。同一条判断没有用在真正浪费回合的地方。

## Evidence

实测（`internal/subagent`，临时探针已删除）：

- `Supervisor.toolsFor(AgentModeInspect)` = `[read_file grep glob]`；`registryWithOptions` 过滤后仍是这三个。
- inspect 子代理调用 `bash`，工具结果是 `unknown tool: bash`；调用 `script` 是 `unknown tool: script`。事件流里协调者看到的是 `tool body="bash error: unknown tool: bash"`。
- 让 provider 每次换参数继续调 `bash`（绕开 `repeatGuard` 的 12 次阈值），在 `Budget.MaxWall = 500ms` 下得到：

```text
ok=false code="timeout" took=1s wall=501ms
output="\n[subagent hit its wall-clock limit]"
```

`code="timeout"` 就来自 `Supervisor.runAgent`（`internal/subagent/supervisor.go:182-184`）。默认预算 `MaxWall` 是 15 分钟（`DefaultBudget`），所以真实会话里表现为"子代理跑了 15 分钟后超时关闭"，而协调者只看到一句 `[subagent hit its wall-clock limit]`，不知道是能力缺失导致的。

注意：这里**没有**任何权限/审批拒绝。子代理构造时不传 `RequiresApproval`/`Approve`（`cmd/neo/subagent.go:52-75`），`agent.go:1037` 的 gate 因此不会装上；`docs/developer/guides/permissions.md:63-73` 也说明审批不覆盖 work/inspect 子代理。根因是能力缺失与诊断信息缺失，不是权限。

## Impact

`MISSION.md` 第 2 优先级是"agent、工具与子代理正在做什么的可见性"，第 3 优先级是"有用且可诊断的失败信息"。当前形态两边都失守：委派一方的投资被烧在一个不可能完成的任务上，浪费 15 分钟与整轮协调者上下文，最后只拿到一个不说原因的超时。

## 验收标准

- `unknown tool` 的结果列出该会话实际可用的工具名（不是全量清单），措辞与现有工具错误风格一致。
- inspect 子代理一旦调用不存在的工具，错误文本说明它没有 shell，并指向它可用的只读替代（`read_file`/`grep`/`glob`），或说明该动作必须由父代理执行。
- 协调者能知道 inspect 的边界：`agent` 工具 schema 的 `mode` 描述或 `subagentCapability` 说明 inspect 无 shell、需要 shell 的动作应走 `mode=work`。**必须核对固定 prompt 预算**（`TestFixedPromptFloorBudget`、`TestScriptFloorBudget`）后再落地，超预算时优先改 schema 的 `mode` 描述而不是系统提示。
- 单测：inspect 子代理调 `bash` 得到的错误里出现可用工具名；协调者事件流里这条错误仍可见（不静默丢弃）。
- 不改动 inspect 的工具白名单，不给它 shell。

## Notes

排查"另一个 Neo 进程报告子代理因为缺少 shell 工具权限超时关闭"时发现。结论：超时属实且可复现，但原因不是权限拒绝，而是能力缺失 + 诊断信息缺失。修复方向是让模型和协调者都能一眼看出边界，而不是放宽白名单。

## 修复

代码改动三处，都只在失败路径或 schema 上记账，不改 inspect 的工具白名单。

1. **未知工具的报错列出会话实际持有的工具**（`internal/agent/agent.go`）。新增 `Agent.unknownToolError`：`unknown tool: bash; this session has: glob, grep, read_file`。列表取自运行中的 registry，不是内置全集——过滤后的 registry 正是差异有意义的地方。空 registry 时不追加列表。
2. **inspect 子代理缺失工具时说明边界**。新增 `Agent.Config.UnknownToolNote`，由构造方填充；`internal/subagent/runner.go` 的 `unknownToolNote(mode)` 只在 inspect 下返回一句「inspect subagents have no shell, so commands, diffs, and git history are unavailable: read files with read_file, grep, and glob, and report what the parent must run itself.」。work 子代理有 shell，缺失工具是组合缺陷，加这句只会误导。模式经 `RunOptions.Mode`（`internal/subagent/subagent.go`）从 supervisor 传入。
3. **协调者能看见 inspect 无 shell**。`agent` 工具 schema 的 `mode` 描述改为 `work (default, writable, serial, has a shell) or inspect (read-only, parallel-safe, no shell)`。

选择 schema 而非系统提示的原因如实记录：先试过在工具描述里加一整句（+144 B），`TestScriptFloorBudget` 升到 10,098 / 10,000 失败；按验收标准回退成只改 `mode` 描述（+17 B），两个预算都通过。

## 验收对照

- `unknown tool` 列出本会话实际可用工具名：`TestAgent_UnknownToolNamesTheAvailableTools`（并断言不会出现该会话没有的 `read_file`）。
- inspect 子代理调 `bash` 的错误含可用工具名与无 shell 说明：`TestInspectChildMissingShellIsDiagnosable` 从子代理下一次请求的 `tool_result` 读取，断言包含 `unknown tool: bash`、`read_file`、`grep`、`glob`、`no shell`。
- 协调者事件流里这条错误仍可见：同一测试断言事件里存在 `bash error: …unknown tool: bash`。
- 协调者的边界信息：`TestAgentToolSchemaStatesInspectHasNoShell`、`TestUnknownToolNoteFollowsTheMode`、`TestInspectOptionsCarryTheMode`。
- 固定 prompt 预算核对后落地：`TestFixedPromptFloorBudget` 8,259 / 8,500（原 8,242），`TestScriptFloorBudget` 9,971 / 10,000（原 9,954），`TestCodeModeFloorBudget` 3,421 / 3,500 不变。`docs/performance.md` 与 `docs/design/token-budget.md` 同步更新，含 `agent` schema 825 B。
- 白名单未动：`TestInspectModeNeverGetsScript`、`TestChildSurfacesDeliverTheToolsTheyAskFor` 仍通过。

## 检查

`go test ./...`、`go vet ./...`、`go test -race ./internal/subagent`（相关用例）、`qlty check`（0 issue）全部通过。

文档：`docs/developer/tools.md` 增补失败路径说明，`docs/parallel-subagents/design.md` 记录边界可见性契约，`docs/performance.md` 与 `docs/design/token-budget.md` 更新实测字节。
