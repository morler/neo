---
id: 64
title: Raise the binary budget to 28 MiB with a probe that makes the cost visible
status: open
priority: high
labels:
    - performance
relations:
    blocks:
        - 65
    depends-on:
        - 63
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

这是体积与依赖任务的第 2 步。第 1 步（#63）只引入模块，二进制不变；这一步加上编译期引用，让体积增量真实出现在二进制里，并同步抬高闸门。两步分开是为了让“依赖是否可用”和“体积决策”各自可独立验证。

实测数据（`CGO_ENABLED=0 -trimpath -ldflags "-s -w"`）：

| 构建 | 字节 | MiB |
| --- | ---: | ---: |
| Neo 当前 | 17,961,120 | 17.13 |
| + moejs 引用 | 24,072,352 | 22.96 |
| 增量 | +6,111,232 | +5.83 |

## 目标

加一个最小编译期引用，把 `scripts/performance.sh` 的预算从 20 MiB 提到 28 MiB，并更新文档中的数字。

## 实现要点

- 在 `internal/tools` 新增一个文件，仅做编译期引用（例如 `var _ = moejs.Compile`），带注释说明“这是体积探针，将在运行时桥接任务中替换为真实调用”。不要在 `cmd/neo` 加，避免 CLI 层知道引擎细节。
- `scripts/performance.sh`：`binary_budget_bytes=$((28 * 1024 * 1024))`。保留超限 `exit 1`，不加任何旁路。
- `docs/performance.md`：更新预算数字与来源说明，明确这是产品决策，并要求“新增依赖前必须重测体积”。
- 测量并记录 awk 输出的 `stripped_binary_bytes`，写进 issue（实现时）。

## 验收标准

- `just performance` 通过，`stripped_binary_bytes` 在 22–24 MiB 量级。
- 把预算临时改回 20 MiB 时 `just performance` 失败（先本地验证再改回，证明闸门有效）。
- `docs/performance.md` 与 `scripts/performance.sh` 数字一致。
- 探针文件有注释说明其临时性质，便于后续替换时找到。
