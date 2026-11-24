---
id: 53
title: Document the compaction knobs and the image token reserve
status: closed
priority: low
labels:
    - docs
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #46 中可独立完成的一小块。#42 引入两个压缩配置项并把图片计费口径固定下来，但这些数字对用户是不可见的——压缩在什么时候发生、为什么这次会话压缩了三次，用户无从判断。

killi 把这件事做到日志层面（decision 45：真实压缩记录 `cause: {context, suffix, limit, reserve, kept_chars}`），Neo 暂时不需要日志，但至少要让配置与默认值可查。

## 前序

#41 与 #42 已完成。

## 实现要点

1. `docs/developer/config.md` 新增两条：`compaction.trigger_ratio`（默认 0.70）、`compaction.growth_floor`（默认 1.2），写清取值范围与非法值的处理。
2. 同一节说明「未知窗口」「已知窗口」两种默认触发线分别是多少，避免读者自己乘。
3. 记录内联图片的估算口径（固定 reserve，不随 base64 长度增长），并说明这是估算、真实值来自 provider 的 `Usage`。
4. 不要重复 `docs/developer/prompt-caching.md` 的缓存规则，链接过去即可。

## 验收标准

- 文档中的默认值与 `internal/compact` 及 `internal/config` 的代码常量逐一对应。
- 不存在「估算值当作计费值」的表述。
- `qlty check --all` 通过。

## Closed
ponytail review: 属于 #46 的一节，合并。
