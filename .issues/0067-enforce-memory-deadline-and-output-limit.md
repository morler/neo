---
id: 67
title: Enforce memory, deadline, and output limits on the script tool
status: closed
priority: high
labels:
    - performance
relations:
    blocks:
        - 68
    depends-on:
        - 65
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

> ponytail 评审删除了原 #66 的运行时池，本任务同步去掉“等 Interrupt 返回再归还”的池约束和相关验收项。

第 3 步（原 #58 的剩余部分）。没有资源限制，一段 `while(true){}` 或大量分配的脚本会挂住 Neo 或耗尽内存，且这会破坏 `tools.MaxOutputBytes` 的既有契约（单次工具调用最多贡献 64 KiB 上下文）。

## 目标

让脚本在执行时间、内存、输出三方面都有硬边界，超限时返回可读错误而不是卡死或 OOM。

## 实现要点

- **超时**：用 `context.WithTimeout` + `context.AfterFunc(ctx, func(){ rt.Interrupt(cause) })`。中断后 `RunScript` 返回 `*moejs.InterruptedError`，转成 `isError=true` 的可读消息。运行时用完即弃（见 #65），因此没有“归还前必须等 Interrupt”的池约束。
- **内存**：`moejs.Options.MemoryLimit` 设非零默认值（建议 64 MiB，与输出上限量级匹配），在注释里写清依据。`ErrMemoryLimit` 要映射成独立可读消息，不要和超时混用。
- **输出**：脚本返回值在宿主侧按 `tools.MaxOutputBytes` 截断，复用现有截断 marker 语义（`internal/agent/tool_result.go` 的约定），不要另造格式。
- **取消传播**：Neo 的 `ctx` 被取消（用户 esc / `--timeout`）时，脚本必须尽快中断，不能等自己的超时。
- **默认值可覆盖**：超时与内存上限做成 `Script` 结构体字段（默认值在构造处），但**不**加 YAML 配置项——`docs/design/token-budget.md` 的“Deferred On Purpose”原则：没有测量需求就不加旋钮。

## 验收标准

- 单测：无限循环脚本在超时后返回可读错误，且耗时接近设定值（允许宽松上界）。
- 单测：大量分配脚本触发 `ErrMemoryLimit`，消息与超时错误可区分。
- 单测：返回 1 MiB 字符串时输出被截断到 `MaxOutputBytes` 并带截断标记。
- 单测：`ctx` 取消立即中断执行（用阻塞桩验证，不依赖超时值）。
- 超时后再次调用仍返回正确结果（证明被中断的运行时没有污染后续调用）。
- `go test -race ./internal/tools` 通过。
