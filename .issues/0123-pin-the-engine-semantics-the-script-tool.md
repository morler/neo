---
id: 123
title: Pin the engine semantics the script tool depends on and record why rye, tengo and risor were rejected
status: closed
priority: low
labels:
    - tools
    - docs
created: "2026-11-27"
updated: "2026-11-27"
closed: "2026-11-27"
---

## 背景

`internal/tools/moejs_test.go` 只有两条断言（`1 + 41` 与无顶层 await），而 `script.go` 依赖的引擎行为远不止这两条：completion value 取末表达式、eval 与 new Function 的取值差异、host error 变成可 catch 的 throw、globalThis 跨脚本存活、Interrupt 与 ClearInterrupt、MemoryLimitError 是独立类型、引擎本身没有 console。任何一条在 moejs 升级后变化，现在都会以"工具结果奇怪"的形式出现，而不是定位到引擎。

同时，用 rye、tengo、risor 替换 moejs 这个问题没有书面结论，容易被反复重开。

## 目标

1. 把 `moejs_test.go` 扩成引擎语义契约测试，每条注释说明它为什么是承重的。
2. 在 `docs/design/js-batch-tool.md` 第 3 节记录三个候选的实测体积与否决理由。

## 验收

- `go test ./internal/tools -run TestMoejs` 覆盖全部承重语义并全部通过。
- `docs/design/js-batch-tool.md` 有 "Engines considered and rejected" 小节，含实测数字与每条否决理由。
- `qlty check` 通过。
