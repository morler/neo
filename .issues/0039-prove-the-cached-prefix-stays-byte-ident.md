---
id: 39
title: Prove the cached prefix stays byte-identical across turns
status: closed
priority: high
labels:
    - test
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`docs/developer/prompt-caching.md` 把「缓存前缀不能被破坏」当作隐含约定，但它没有任何测试保护。当前有两处机制在起作用：

- `internal/tools/tool.go:67-83` 的 `Specs()` 与 `Names()` 对工具名排序，所以工具定义顺序稳定。
- `cmd/neo/main.go:442-457` 的 `capabilitySections` 按注册表里是否存在 `workflow`/`agent`/`goal` 决定追加哪些段落。

问题在于：这两处都只是「当前恰好稳定」。任何一次重构（例如把工具描述改成由配置渲染，或在会话中途 `Filter` 工具）都会静默地让缓存前缀失效，而代价只是账单变高，测试不会红。killi 把这条列为硬规则（decisions 9、26），并用测试守住。

## 目标

一个测试，证明同一会话状态下反复渲染得到字节完全相同的前缀；并且当工具集合或提示词构成发生变化时前缀确实改变，从而证明测试不是在自我验证。

## 实现要点

- 新增 `cmd/neo` 测试：用固定的 `config.Config` 与 `chatRegistry()` 连续调用 `chatSystem` 两次，断言两次的 `SystemBlocks` 逐字节相等，且第一块（可缓存块）的 `Cache` 为真。
- 同一测试内连续取两次 `reg.Specs()`，断言 `json.Marshal` 结果相等，且顺序与 `Names()` 一致。
- 加一个反向断言：用 `reg.Filter([]string{"bash","read_file"})` 得到的注册表，其 `Specs()` 与完整注册表不同——证明测试能发现变化。
- 测试不得依赖时间或随机量。`environmentSection` 接收 `now`，若需要稳定化就在测试里固定，不要改动生产签名。
- 文档：在 `docs/developer/prompt-caching.md` 增加一节「Prefix stability contract」，说明哪些东西冻结、由哪个测试守护，以及新增工具时必须读它。

## 验收标准

- 测试在未改动时通过；把某个工具描述改成一个依赖调用次数的值后测试失败。
- `docs/developer/prompt-caching.md` 明确列出冻结项（工具列表、缓存块、历史）与唯一的例外（`/clear`、`/provider`、`/model`、压缩）。
- `go test ./...`、`go vet ./...`、`qlty check --all` 通过。

## Closed
ponytail review: 与 #38 读同一份 Specs() 与 chatSystem，合并进 #38 一个测试函数。
