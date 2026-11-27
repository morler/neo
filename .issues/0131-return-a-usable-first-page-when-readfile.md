---
id: 131
title: Return a usable first page when read_file exceeds the byte cap
status: open
priority: high
labels:
    - bug
    - tools
created: "2026-11-27"
updated: "2026-11-27"
---

## 背景

这是 Maki token 优化对照的第一批，也是唯一直接立项的修复。Maki 的 `plugins/read/init.lua` 在有界读取后返回已读内容和续读位置。Neo 的 `internal/tools/fs.go` 在带行号的读取超过 64 KiB 时丢弃已缓冲内容，却让调用方从首个未装下的行继续，照做会跳过从未交付的前段。#44 只保证提示给出可执行参数；#110 定义了 raw 原文契约。

## 范围与预期收益

只修 `read_file` 带行号的超限路径：在现有字节预算内返回装得下的完整行及明确的续读偏移。从第一页就能使用证据；如果目标已在第一页，少一次补读及随后的模型请求。不是下调输出上限，也不是全局裁剪。

## 验收标准

- 覆盖多行超限、有无显式 limit、非 1 offset：按返回偏移续读，原始行号和内容不漏、不重，返回值（含标记）不超过 64 KiB。
- 若第一页一整行都装不下，给出可区分的错误，不建议用同一偏移无效重试；不把半行或损坏的 UTF-8 当正文交付。
- `raw: true` 的成功输出与超限失败契约不变；未超限时带行号的输出不变。不改工具 schema、grep/glob 或编辑语义。
- 增加回归测试并更新 `docs/developer/tools.md`；验证通过现有工具与提示词预算测试。
