---
id: 22
title: Document /goal in developer docs, changelog, and the CLI surface
status: open
priority: medium
labels:
    - docs
relations:
    depends-on:
        - 18
        - 20
        - 15
created: "2026-11-21"
updated: "2026-11-21"
---

## 背景

AGENTS.md 要求行为变更时同步文档；草案 §12/§14 第 11 步要求更新配置、CLI、会话与工具文档。当前 `docs/developer/index.md` 没有一次性讲清 goal 的页面，`docs/design/goal-runner.md` 仍是 Draft，`/help` 与 `neo help` 也没有 `/goal`。

## 目标

让用户只看 `docs/developer` 与 `/help` 就能理解、使用、排错 `/goal`，并让草案状态与实现一致。

## 实现要点

- 新增 `docs/developer/goals.md`：概念（goal vs workflow vs skill）、五个子命令、状态机图、attempt 预算与 handoff、blocked 阈值、与 `/clear` 和中断的关系、一个完整示例对话、「不支持什么」（无 judge 模型、无 `/loop`、无 goal 文件）。
- `docs/developer/index.md` 加链接；`docs/developer/tools.md` 补 `goal` 工具行与 ownership 边界；`docs/developer/sessions.md` 补 `goal` 字段（与 #13 一致）；`docs/developer/config.md` 的 `goals` 小节与 #15/#20 对齐；`docs/developer/architecture.md` 模块表加 `internal/goal`，并在启动流程里说明 manager 的位置。
- `ARCHITECTURE.md` 模块表同步 `internal/goal`（保持与该文档既有结构一致）。
- `docs/design/goal-runner.md` 顶部 Status 由 Draft 改为 Implemented（Phase 1），并注明哪些后续项未做（judge、`/goal draft`、wait barriers、token 预算）。
- `CHANGELOG.md` Unreleased 增加 `/goal` 的 Added 条目（一句话讲清控制面 + 预算 + 与 /clear/中断的关系）。
- `cmd/neo/main.go` 的 `usageText` 与 `/help` 的 `/goal` 条目已在 #17 随命令一起落地；本 issue 只需核对措辞与最终行为一致，不要重复添加。
- 所有文档示例与实现一致；不写尚未实现的命令（如 `/subgoal`、`/goal wait`）为可用特性。
- 测试：`docs/developer` 内的相对链接不指向不存在文件（可用脚本或人工核对）；`/help` 输出含 `/goal`。

## 验收标准

- `docs/developer/goals.md` 中的 YAML/命令示例可直接照做，且与代码行为一致。
- 文档明确写出 Phase 1 的边界与已知权衡（模型自评完成度）。
- `go test ./...` 通过；文档站点构建不受影响（`docs/developer/**` 变更即可触发）。
