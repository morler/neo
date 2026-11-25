---
id: 93
title: Drop the unbounded process-wide source cache in solo mode
status: in-progress
priority: medium
labels:
    - refactor
    - performance
relations:
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

#91 为单工具模式加了源码编译缓存，让重复的 snippet 不必每轮重编译：

```go
var soloCompiled sync.Map   // internal/tools/script.go:561
```

这是一个**进程级**、**无上界**、**无淘汰**的全局缓存，键是模型写的源码原文。问题有三层：

1. **没有淘汰。** 一个长会话里每次调用写的源码都不同，进程内存随调用次数单调增长，直到进程退出。`/clear`、切换会话、切换模型都不会释放。
2. **是全局状态。** 它不属于任何 `Script` 实例，因此测试之间、多个会话之间共享。当前单会话进程里没有暴露问题，但它与仓库"工具无状态"的既有设计（`docs/design/js-batch-tool.md` §4）相冲突。
3. **收益未经测量。** 缓存假设"重编译代价显著"，但没有任何数据支持；默认模式（无状态、无缓存）在 #91 的测量里错误更少、同样 5/5 成功，说明编译开销并非瓶颈。

## 目标

消除这个无界全局缓存，且不引入可观测的性能退化。

## 实现要点

- **首选：直接删除缓存**，每次 `runSolo` 调用 `moejs.CompileScript`。改动最小，符合"删除偶然复杂度"的原则。
- 若删除后有可测量的退化，改为**挂在 `ScriptSession` 上**的有界缓存（例如只保留最近 N 条，N 取实测需要的最小值），随会话生命周期释放——会随 `Session` 一起被丢弃，而不是留在进程里。
- 无论哪种，缓存都不得是包级变量。
- 注意：`compileSolo` 目前先把源码包进 `runSoloSnippet` 再编译，删除缓存时保持这个包裹逻辑不变，否则会破坏 #91 修掉的"重复 `const` 声明报错"行为。

## 验收标准

- `internal/tools` 中不再有包级 `sync.Map`（或任何包级可变缓存）。
- 单测：连续 200 次不同源码调用后，进程内不再持有这些源码（可用 `runtime.ReadMemStats` 做**定性**断言，或直接断言不存在导出/包级缓存结构）。
- `TestSoloAllowsRepeatedLocalDeclarations` 与 `TestSoloKeepsGlobalStateAcrossCalls` 仍通过（证明包裹逻辑未变）。
- `go test ./...` 通过；`qlty check` 退出码为 0。
