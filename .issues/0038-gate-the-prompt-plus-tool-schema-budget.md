---
id: 38
title: Gate the prompt plus tool schema budget with one test
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 44
    depends-on:
        - 36
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`cmd/neo/performance_test.go:14-29` 只给系统提示词设了字节预算：核心 1,400 字节、聊天 2,600 字节。工具定义完全没有预算约束，而它才是固定开销的大头。

本次实测（`chatRegistry().Specs()`，JSON 序列化）：

| 组成部分 | 字节 | ~tokens |
| --- | ---: | ---: |
| 9 个工具定义 | 5,022 | 1,255 |
| 系统提示词（含内置技能目录） | 3,062 | 765 |
| **固定下限合计** | **8,084** | **~2,020** |

单条最大的是 `agent` 808 B、`goal` 775 B、`grep` 645 B、`workflow` 536 B。

`docs/performance.md:50` 的单一 2,048 字节预算与代码里的 1,400/2,600 已经漂移；那份修正单独由 #47 负责，本任务不重复改文档。

## 目标

用一个 Go 测试同时约束两件事，合并自原 #39：

1. 「系统提示词 + 工具 schema」的固定开销不超过预算（原则 3）。
2. 同一会话状态下反复渲染得到逐字节相同的前缀（原则 1）。

两者读的是同一份 `Specs()` 与 `chatSystem` 输出，拆成两个测试只会多一次评审。

## 实现要点

- 在 `cmd/neo/performance_test.go` 增加 `TestFixedPromptFloorBudget`：渲染 `chatRegistry().Specs()` 的 JSON 字节数，加上 `chatSystem(...)` 的提示词字节数，断言总和不超过具名常量 `fixedFloorByteBudget`。
- 常量初值取当前实测向上取整（8,084 → 建议 8,500），并在注释里写明来源与复核方式，避免成为没人敢改的数字。
- 失败信息必须打印实测值与预算值，让评审看到差多少，而不是只报一句超限。
- 同时修正 `docs/performance.md` 的预算描述为 1,400 / 2,600 / fixed floor 三档，与代码一致。
- 加一个反向断言，证明测试确实能发现问题：把某个工具描述改成一个依赖调用次数的值，或改用 `reg.Filter([]string{"bash","read_file"})` 的注册表，前缀必须随之改变。
- 断言可缓存块（`SystemBlocks[0]`）的 `Cache` 为真，且两次渲染的 `SystemBlocks` 逐字节相等。
- 测试不得依赖时间或随机量。`environmentSection` 接收 `now`，需要稳定化就在测试里固定，不要改生产签名。
- 不改任何生产代码行为。

## 验收标准

- 新增测试在没有改动时通过；把任一工具描述加长 200 字节后失败并打印实测值。
- `docs/performance.md` 不再出现与代码矛盾的 2,048 单一预算。
- `docs/developer/prompt-caching.md` 增加「Prefix stability contract」一节，列出冻结项（工具列表与顺序、缓存块、已发送历史）、允许的例外（`/clear`、`/provider`、`/model`、压缩），并指向本测试。
- `go test ./cmd/neo/`、`go vet ./...`、`qlty check --all` 通过。
