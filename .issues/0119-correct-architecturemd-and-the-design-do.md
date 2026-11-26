---
id: 119
title: Correct ARCHITECTURE.md and the design docs, which still name the removed internal/factory package
status: open
priority: medium
labels:
    - docs
created: "2026-11-26"
updated: "2026-11-26"
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
