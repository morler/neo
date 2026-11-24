---
id: 60
title: Enforce approval and sandbox gates at the tool boundary inside scripts
status: wontfix
priority: critical
labels:
    - security
relations:
    blocks:
        - 61
    depends-on:
        - 59
    related-to:
        - 58
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`script` 工具把工具调用藏进一段 JS 里。Neo 现有的审批是“按工具名和参数前缀匹配”（`internal/approval`，由 `cmd/neo` 注入 `agent.Config.RequiresApproval`，在 `agent.prepareTool` 里串行判定）。如果只在最外层判 `script`，那么 `tool_approvals: [write_file]` 这类规则会被脚本完全绕过——脚本内部调 `write_file` 时，外层工具名是 `script`，前缀也对不上。

这是安全边界问题，不是体验问题。必须先于任何写操作在脚本内可用之前落地。

## 目标

脚本内每次工具调用都必须经过与顶层调用相同的审批与沙箱判定；被拒绝的调用在脚本内得到可读错误，且不能靠重试绕过。

## 实现要点

- **复用而非复制**：把 `RequiresApproval` 与 `Approve` 下推到宿主函数层。`internal/tools` 已通过 `tools.WithCallMetadata` 接收上下文，用同样的方式注入 `tools.WithApproval(ctx, matcher, approveFn)`，由 agent 在 `runPreparedTool` 注入，避免 `internal/tools` 依赖 `internal/agent`。
- **判定顺序**：先校验参数、再做审批匹配、最后执行。脚本无法在审批后改写参数（`map[string]any` 是深拷贝，见 `docs/developer/guides/agent-loop.md` 的“Who Owns A Message”）；需有测试证明 JS 侧修改入参对象不会影响已审批的实际参数。
- **拒绝即失败**：用户拒绝时，宿主函数返回 error，JS 侧抛出；脚本捕获后**不得**自动重试同一调用。若脚本捕获并继续调用其他工具，那是新的一次审批，允许。要在注释里写清这个边界。
- **嵌套不递归**：脚本内**不允许**再调用 `script`（白名单里不包含自身），避免无限嵌套与审批栈过深。
- **沙箱**：若后续开放 `node:fs`，必须走 `FSOptions.Allow`，且 `Allow` 的判定要与审批共用同一套规则，不能出现“审批看 JS 路径、沙箱看解析后路径”导致的不一致。第一版若不开 `fs`，就在文档里明确记录这个决定。
- **非交互环境**：`neo run` 没有审批回调（`cmd/neo/main.go:933` 只在有 `tool_approvals` 时装配）。脚本内的写操作在无 approver 时必须**失败**而不是静默放行——复用 `runPreparedTool` 现有语义“approval required but no approver is configured”。
- **审计**：被拒绝与被批准的子调用都要出现在 #59 的事件流里，用户能回溯脚本到底尝试了什么。

## 验收标准

- 单测：`tool_approvals: [write_file]` 下，脚本内调用 `write_file` 触发一次审批；批准则执行，拒绝则脚本收到错误。
- 单测：无 approver 时，脚本内需要审批的调用返回明确错误，且未产生任何文件写入（用临时目录断言）。
- 单测：JS 在调用前修改入参对象，已审批的参数不受影响（证明没有 TOCTOU 缺口）。
- 单测：脚本内 `script` 自身不可见/不可调用。
- 集成测试：交互式路径下 TUI 收到审批请求并正确回传结果。
- `go test -race ./internal/tools ./internal/agent ./internal/tui` 通过。
- `docs/developer/tools.md` 与设计文档写明“审批在工具边界，不在脚本边界”。

## Closed
已拆分为 #70（审批下推）+ #71（写操作与防绕过）+ #72（node:fs 决策）
