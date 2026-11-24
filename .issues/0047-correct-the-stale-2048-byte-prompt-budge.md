---
id: 47
title: Correct the stale 2048-byte prompt budget in performance.md
status: closed
priority: medium
labels:
    - docs
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`docs/performance.md:50` 写「the base prompt must remain at or below 2,048 bytes」，并称该预算由正常 Go 测试在 CI 中检查。但代码 `cmd/neo/performance_test.go:14-29` 实际执行的是两个预算：

```go
corePromptByteBudget = 1400
chatPromptByteBudget = 2600
```

而且注释明确解释了为什么是两个（「the prompt is no longer one size」）。文档停留在拆分之前的单预算版本，属于纯粹的文档漂移，会让 contributor 按错误的数字做判断。

## 目标

让 `docs/performance.md` 与 `cmd/neo/performance_test.go` 的描述一致。

## 实现要点

- 把「2,048 字节」改为两档：核心提示词 1,400 字节（每种模式都发送）、聊天提示词 2,600 字节（含 workflow 与 delegation 段）。
- 说明两档各自的覆盖范围，与 `performance_test.go` 的注释保持一致。
- 若 issue #38 的固定下限预算已经落地，一并补上第三档；否则只写现有两档，不要提前声称。
- 不要在本文档里重复 `docs/developer/prompt-caching.md` 的缓存规则，链接过去即可。

## 验收标准

- `docs/performance.md` 中的预算数字与 `cmd/neo/performance_test.go` 的常量逐一对得上。
- 文档不再出现「2,048」这个单一预算。
- `qlty check --all` 通过。
