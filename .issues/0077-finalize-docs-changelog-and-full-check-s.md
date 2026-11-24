---
id: 77
title: Finalize docs, changelog, and full check suite for the script tool
status: closed
priority: medium
labels:
    - docs
relations:
    depends-on:
        - 76
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #62 的第 3 步。功能和度量都完成后的收尾：让文档与实际一致，并跑完整检查。

## 目标

文档、变更记录、检查全部到位，特性可以合入。

## 实现要点

- `docs/developer/tools.md`：`script` 行与白名单、限制、审批行为最终一致。
- `docs/design/js-batch-tool.md`：把实现与设计对齐，删掉未落地的段落或标注为后续 issue。
- `docs/performance.md`：体积与 prompt 预算两处数字最终一致。
- `CHANGELOG.md` 的 Unreleased 段记录该特性（新增工具、能力边界、预算调整）。
- `docs/developer/index.md` 有设计文档入口。
- 清理 #64 留下的体积探针（若已由真实调用替代）。

## 验收标准

- `go test ./...` 通过。
- `go test -race ./internal/...` 通过。
- `just performance` 通过，体积在 28 MiB 预算内。
- `qlty check --all` 退出码 0。
- `go test ./cmd/neo -run TestDocsLinks` 通过（所有新文档链接有效）。
- 仓库内 `grep -ri "tura"` 只出现在讨论性注释/文档中，不含任何 AGPL 代码或逐字文案。
