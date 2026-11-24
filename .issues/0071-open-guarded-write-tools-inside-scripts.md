---
id: 71
title: Open guarded write tools inside scripts and prove no approval bypass
status: closed
priority: critical
labels:
    - security
relations:
    blocks:
        - 73
    depends-on:
        - 70
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #60 的第 2 步。第 1 步（#70）只证明机制在只读工具上生效。这一步才真正放开写操作——风险最高，因此单独成任务，便于在放开前后各做一次审计。

若不做这一步，`script` 只能读，无法完成“读 → 判断 → 写”的核心场景，特性价值大打折扣。

## 目标

让脚本内可以调用 `write_file` 与 `edit_file`，并证明任何路径都无法绕过审批。

## 实现要点

- 白名单加入 `write_file`、`edit_file`（仍**不含** `bash`、`agent`、`goal`、`workflow`、`script` 自身）。
- 复用 #70 的审批通道，不新增第二条路径。
- 不开放 `node:fs`：文件访问只经 Neo 工具，审批、截断、路径语义才有单一来源。`bash` 永不进入白名单——Neo 不解释 shell 语义，`internal/tools/bash.go` 也不做只读判定，放进来等于把审批变成摆设。
- **TOCTOU 防护**：必须证明 JS 在调用前修改入参对象不会影响已审批并执行的实际参数。`runPreparedTool` 传给工具的是深拷贝（`docs/developer/guides/agent-loop.md` 的“Who Owns A Message”），宿主函数层要延续这一保证——在审批**之前**把入参转成 Go 值并冻结，审批与执行都用这份 Go 值。
- 审批文案要能区分来源：用户应看到“script 请求 write_file”之类的上下文，而不是误以为是顶层调用。在 `ApprovalRequest` 里带父工具名（若结构体不便扩展，在 `ToolName` 前缀或另有字段，并在 issue 实现时记录选择）。
- 失败传播：写失败不得让脚本静默继续写成半成品；错误必须回到 JS 并最终体现在父工具结果里。
- 增加一个集成测试：交互路径下 TUI 收到审批请求，批准后文件确实写入；拒绝后文件不存在。

## 验收标准

- 单测：`tool_approvals: [write_file]` 下脚本内写文件触发审批；批准写入、拒绝不写入（临时目录断言）。
- 单测：JS 在调用前篡改入参对象，已审批参数不受影响。
- 单测：脚本内 `bash`/`agent`/`goal`/`workflow`/`script` 均不可见。
- 单测：无 approver 时写操作失败且未产生文件。
- 集成测试（`internal/tui` 或 `cmd/neo`）：审批提示可见，批准/拒绝行为正确。
- 单测：脚本内无法访问 `node:fs`（import 失败或 `require` 不存在）。文件访问只走 Neo 工具，避免出现“审批看 JS 路径、沙箱看解析后路径”的第二条策略。
- `go test -race ./...` 中相关包通过。
- `docs/developer/tools.md` 更新 `script` 行的能力描述。
