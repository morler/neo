---
id: 23
title: Add an end-to-end /goal run test through the chat composition
status: open
priority: medium
labels:
    - feature
relations:
    depends-on:
        - 19
        - 20
        - 21
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

草案 §14 的 1-11 步分散在 goal 包、session、工具、TUI 四层；单元测试各自通过并不能证明「`/goal` 一次真实会话能持续推进、自证完成、并在 resume 后还原」。仓库已有同类先例：issue #7 用 httptest 假服务证明自定义 provider 在真实启动流程里成立。

## 目标

用一个不联网的端到端用例覆盖 `/goal` 的主路径与两条终止路径，把它作为 Phase 1 的验收门槛。

## 实现要点

- 位置：`cmd/neo` 下的集成测试（与 `customprovider_e2e_test.go` 同等层级），使用 `llmtest` 或 httptest 假 provider，不访问外网。
- 主路径：构造 TUI model + goal manager + 假 provider，执行 `/goal <objective>`；断言首轮请求包含 objective、会话文件在请求前已有 active goal；provider 第一轮返回文本 + `workflow` 工具调用，第二轮返回 `goal` 工具 `complete`，断言最终状态 complete、attempts 只统计自动续跑、不再有后续 turn。
- 终止路径 A（预算）：`max_attempts: 2`，假 provider 每次都只做普通工作且不 complete，断言 2 次续跑后进入 budget_limited 并输出 handoff。
- 终止路径 B（中断）：自动续跑进行中触发 Esc，断言状态 paused、无下一次自动 turn，`/goal resume` 后恢复。
- 恢复路径：把会话保存到临时目录后重新 `Load` 并重建 manager，断言 `/goal status` 文本与保存前一致。
- 所有断言基于用户可见输出（transcript 块、状态文本、会话 JSON），不直接读 manager 内部字段，以验证分层边界。
- 允许为可测性抽取小函数，但不得改变 `tui.Run` 的对外签名。

## 验收标准

- 用例在离线环境稳定通过（`-count=10` 不抖动），不 sleep 真实时间。
- 失败时能指出是工具未注册、续跑未触发、预算计数错误，还是持久化缺失。
- `go test ./...` 与 `just check-install-script` 不受影响。
