---
id: 119
title: Correct ARCHITECTURE.md and the design docs, which still name the removed internal/factory package
status: closed
priority: medium
labels:
    - docs
created: "2026-11-26"
updated: "2026-11-27"
closed: "2026-11-27"
---

`ARCHITECTURE.md` is headed `**Status:** Current implementation` but describes a package that has not existed since 2026-08-27, and omits two packages that do exist. The two `docs/parallel-*/design.md` files contain `go test` commands that cannot run.

## Evidence

`ARCHITECTURE.md` refers to `internal/factory` five times:
- `ARCHITECTURE.md:89` — dependency diagram
- `ARCHITECTURE.md:108` — "`internal/factory` reuses the core loop for supervised child agents."
- `ARCHITECTURE.md:337` — tool table row for `agent`
- `ARCHITECTURE.md:405` — "the `agent` tool implemented by `internal/factory`"
- `ARCHITECTURE.md:520` — package reference row

`internal/factory` was renamed to `internal/subagent` in `bed7e01 refactor(subagent): rename the factory package and drop dead max_retries (#311)` (2026-08-27). The current tree has `internal/subagent/` and no `internal/factory/`. `grep -c 'internal/subagent' ARCHITECTURE.md` returns `0`, so the package that actually exists is not documented at all.

Two packages are missing from the section 11 package table entirely: `internal/profile` and `internal/subagent`.

The design docs carry runnable commands that fail:
- `docs/parallel-subagents/design.md:146-147` — `go test ./internal/factory ./internal/agent ./cmd/neo`
- `docs/parallel-tool-calls/design.md:129-130` — `go test ./internal/agent ./internal/tools ./internal/factory`

```
$ go test ./internal/factory
stat .../internal/factory: directory not found
FAIL	./internal/factory [setup failed]
```

The document also stamps `Verification basis: b70f4a1 (origin/main on 2026-08-01)`, which predates the rename, so its verification claim no longer holds.

## Impact

`ARCHITECTURE.md` is the first thing `AGENTS.md` points a contributor or agent at, and the mission treats a dependable, understandable core as priority 1. An agent that follows the doc looks for `internal/factory`, finds nothing, and has to re-derive the supervisor's location from `internal/subagent/` — exactly the kind of avoidable rework the mission's "prefer deleting accidental complexity" argues against. A human reader gets a wrong mental model of where child-agent policy lives.

## Acceptance criteria

- Every `internal/factory` reference in `ARCHITECTURE.md` names `internal/subagent` instead, including the dependency diagram and the section 11 table.
- Section 11's package table lists `internal/subagent` and `internal/profile` with responsibilities and boundaries matching the actual packages.
- The `go test` commands in `docs/parallel-subagents/design.md` and `docs/parallel-tool-calls/design.md` run without a setup failure.
- Either update or remove the `Verification basis` stamp so it does not claim a commit that predates the rename.
- `go test ./cmd/neo/ -run TestDeveloperDocsLinksResolve` still passes (link targets, not code references, are what that test checks).

## Notes

Found by a full-repository review at `899b7b5`. This is documentation drift only: no runtime behavior is affected.

## 完成记录（2026-11-27）

- `ARCHITECTURE.md` 的 5 处 `internal/factory` 全部改为 `internal/subagent`（依赖图、Key rules、工具表 `agent` 行、第 8 节正文、第 11 节包表）。
- 第 11 节补入 `internal/profile/`（职责取自包文档：`~/.neo/agents/` 与 `<workspace>/.neo/agents/` 的 markdown 发现，`--agent` 选择；边界：纯文件，无 frontmatter/schema/config 项，不做提示词组合）与 `internal/subagent/`，并保持表格的字母序；依赖图下的 "Supporting leaf packages" 同步补 `internal/goal` 与 `internal/profile`。
- 顺带修正一处同一来源的漂移：工具表说 `read_file` "capped at about 256 KiB"，实际是 `MaxOutputBytes` = 64 KiB 且用 `offset`/`limit` 分页。
- `Verification basis` 戳（`b70f4a1`，2026-08-01，早于改名）替换为指向当前树的说明，并由新增测试保证不再漂移。
- 设计文档：`docs/parallel-subagents/design.md` 的 2 条 `go test`、4 个源码路径、1 个 `cmd/neo/factory.go` 全部改名；`docs/parallel-tool-calls/design.md` 的 2 条 `go test` 改名；`docs/parallel-execution-ui/design.md` 的 3 处 "factory" 措辞改为 "subagent"。
- 新增 `cmd/neo/docs_links_test.go` 两条测试，固定这一类漂移：
  - `TestArchitecturePackageReferencesResolve`：`ARCHITECTURE.md` 里出现的 `internal/…`、`cmd/…`、`website/…` 路径（反引号内与依赖图裸写都算）必须对应真实目录；`internal/llm/<provider>` 这类占位符只校验到根。
  - `TestDesignDocTestCommandsRun`：`docs/*/design.md` 里每条 `go test` 的 `./` 包参数必须是真实目录。
- 反向验证：把 `internal/factory` 写回依赖图 → 测试报 `internal/factory`；把 `./internal/factory` 写回 `go test` → 测试报该目录不存在。两条都确实能抓住原缺陷。
- 验证：`go test ./... -count=1`、`go vet ./...`、`qlty check` 均通过；`TestDeveloperDocsLinksResolve` 仍通过。
