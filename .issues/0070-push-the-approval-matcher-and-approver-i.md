---
id: 70
title: Push the approval matcher and approver into the script tool boundary
status: in-progress
priority: critical
labels:
    - security
relations:
    blocks:
        - 71
    depends-on:
        - 69
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

原 #60 的第 1 步，也是本特性最关键的安全修复。Neo 的审批在 `agent.prepareTool` 判定（`cmd/neo/main.go:933` 注入 `RequiresApproval`）。脚本内部调用工具时外层工具名是 `script`，前缀也匹配不上 `tool_approvals` 里的规则，因此会被完全绕过。

这一步只打通“脚本内只读工具也走审批链路”的机制，不开放写操作（下一步做），使风险面最小。

## 目标

脚本内的每次工具调用都经过与顶层调用相同的审批匹配与批准回调。

## 实现要点

- 在 `internal/tools` 增加上下文注入（与 `CallMetadata` 同层），例如 `tools.WithApproval(ctx, requires func(string, map[string]any) bool, approve func(context.Context, ApprovalRequest) (bool, error))`。`internal/tools` 不能 import `internal/agent`，所以这里定义自己的请求类型，由 agent 侧适配。
- agent 在 `runPreparedTool` 注入真实实现，与顶层调用**共用同一份** `RequiresApproval`/`Approve`，不允许另造一份规则。
- 脚本宿主函数执行顺序固定：**校验参数 → 审批匹配 → 等待批准 → 执行**。
- 无 approver 但需要审批时，必须失败并给出与顶层一致的语义（复用 `runPreparedTool` 的 “approval required but no approver is configured” 文案）。
- 被拒绝时返回 error，JS 侧抛出；**不得**自动重试同一调用。允许脚本捕获后调用其他工具（那是新的审批）。
- 此步先在只读工具上验证：给 `script` 加 `grep`/`glob`，并证明它们的审批也生效（即使实际不会有人给只读工具配审批，机制必须是通用的）。

## 验收标准

- 单测：配置 `tool_approvals: [grep]` 时，脚本内 `grep` 触发一次审批；批准执行、拒绝报错。
- 单测：无 approver 且规则命中时，脚本内调用返回明确错误，且未执行（用桩断言调用次数）。
- 单测：审批回调不会被并发调用（脚本内串行调用时，approver 同时活跃数为 1）。
- 单测：脚本捕获拒绝后调用另一个工具的路径可用，且产生第二次审批。
- `go test -race ./internal/tools ./internal/agent ./internal/tui` 通过。
- 代码注释写明“审批必须在此层判定，外层 `script` 的审批不能替代”。
