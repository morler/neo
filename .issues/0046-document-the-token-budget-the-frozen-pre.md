---
id: 46
title: Document the token budget, the frozen prefix and the cache switches
status: in-progress
priority: medium
labels:
    - docs
relations:
    depends-on:
        - 43
        - 44
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

本次计划引入多个用户可见的概念与开关：固定开销预算、缓存前缀冻结契约、`prompt_cache_key`、per-model `cache_control`、压缩的比例与增长地板。developer 文档目前分散且部分过时：

- `docs/developer/prompt-caching.md` 只描述 Anthropic 的块布局与一个滚动断点，没有说明契约与测试。
- `docs/performance.md:50` 的 2,048 字节预算与代码中的 1,400/2,600 不一致。
- `docs/developer/config.md` 没有缓存与压缩的新开关。

`AGENTS.md` 要求「Keep those docs current when behavior changes」。

## 范围（合并自原 #46 + #53）

一并覆盖压缩旋钮与图片计费口径：这些数字是同一个「用户看不见」的问题，分散成两篇只会让读者找不到。

## 目标

让 contributor 从 `docs/developer/index.md` 出发两跳内找到：预算是什么、前缀为什么不能变、每个开关何时该开。

## 实现要点

- 更新 `docs/developer/prompt-caching.md`：新增「Prefix stability contract」一节，列出冻结项（工具列表与顺序、缓存块文本、已发送历史）、允许的例外（`/clear`、`/provider`、`/model`、压缩、`/reload` 类显式重建），并指向守护它的测试。
- 更新 `docs/performance.md`：预算改为三档并说明各自覆盖范围，消除 2,048 的旧表述。
- 更新 `docs/developer/config.md`：写清 `compaction.context_window_tokens` 现有效果，并记录「已知窗口 / 未知窗口」两种默认触发线分别是多少（避免读者自己乘）。
- 同一节记录内联图片的估算口径：固定 reserve，不随 base64 长度增长；明确这是**估算**，真实值来自 provider 的 `Usage`。
- 不再列出被评审砍掉的开关（`tools.max_output_bytes`、`compaction.trigger_ratio`、`compaction.growth_floor`、`cache_control`、`prompt_cache_key`）——它们没有实现，文档不得提前声称。
- 在 `docs/developer/index.md` 增加指向 `docs/design/token-budget.md` 的条目，让计划本身可发现。
- 保持与 `MISSION.md` 一致：不引入服务器、遥测或运行时插件。

## 验收标准

- 三份文档的新增内容与对应 issue（#38、#39、#40、#42、#44）的最终实现一致；若某任务未落地，文档不得提前声称已支持。
- `docs/performance.md` 不再出现与代码矛盾的预算数字。
- `docs/developer/index.md` 能直达设计文档与性能文档。
- `qlty check --all` 通过。
