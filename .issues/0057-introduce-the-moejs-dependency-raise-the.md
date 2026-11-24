---
id: 57
title: Introduce the moejs dependency, raise the binary budget to 28 MiB, and add third-party notices
status: wontfix
priority: high
labels:
    - config
    - performance
relations:
    blocks:
        - 58
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`docs/design/js-batch-tool.md`（#56）决定用可嵌入 JS 运行时实现批处理工具。选定 `github.com/MoeForge/moejs`：纯 Go（库本身零 cgo，cgo 只出现在其 bench 工具）、Apache-2.0（与 Neo 的 MIT 兼容）、可按 `context` 中断、内置沙箱化 `node:fs`。

实测代价（`CGO_ENABLED=0 -trimpath -ldflags "-s -w"`，与 `scripts/performance.sh` 同一套 flags）：

| 构建 | 字节 | MiB |
| --- | ---: | ---: |
| Neo 当前 | 17,961,120 | 17.13 |
| Neo + moejs（仅引用 `moejs.Compile`） | 24,072,352 | 22.96 |
| 增量 | +6,111,232 | +5.83 |

`scripts/performance.sh` 的 `binary_budget_bytes` 是 `20 * 1024 * 1024`，会超限 2.96 MiB。引擎没有裁剪 Intl/CLDR/Unicode 数据的构建标签，这 5.83 MiB 基本是固定成本。用户已明确接受该体积变动。

## 目标

引入依赖、抬高预算闸门，并把“为什么接受这笔体积”写成文档，使体积增长是一次有记录的产品决策而非悄悄漂移。

## 实现要点

- `go.mod` 增加 `github.com/MoeForge/moejs`，`go mod tidy` 后确认只新增 `moejs`、`moego`、`yaml/v3`。
- `scripts/performance.sh`：`binary_budget_bytes` 从 20 MiB 提到 28 MiB。保留超限即 `exit 1`，不加旁路开关。
- `docs/performance.md`：更新预算数字，写明 28 MiB 的来源（+5.83 MiB）与“产品决策”定位；补一句“新增依赖前必须重测体积”。
- 新增 `THIRD_PARTY_NOTICES.md`，列出 moejs 与 moego 的 Apache-2.0 声明和上游地址；不改 Neo 自身 MIT 声明。
- `.goreleaser.yaml` 的 `archives.files` 增加 `THIRD_PARTY_NOTICES.md`。
- 在 `internal/tools` 加一个最小的编译期引用（后续任务会替换为真实调用），让闸门在功能接入前就能反映真实增量。

## 验收标准

- `go build ./...`、`go test ./...` 通过。
- `just performance` 通过，`stripped_binary_bytes` 落在 22–24 MiB 量级。
- 把预算临时改回 20 MiB 时 `just performance` 失败（证明闸门仍有效）。
- `docs/performance.md` 与 `scripts/performance.sh` 数字一致；`TestDocsLinks` 通过。
- `THIRD_PARTY_NOTICES.md` 含两个依赖的许可证与版本。

## Closed
已拆分为 #63（依赖与 NOTICE）+ #64（体积预算与探针），按顺序执行替代
