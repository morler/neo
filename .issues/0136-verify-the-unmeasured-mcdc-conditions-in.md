---
id: 136
title: Verify the unmeasured MC/DC conditions in file_picker and tui/model
status: open
priority: low
labels:
    - bug
created: "2026-11-28"
updated: "2026-11-28"
---

## 背景

`supercov -- go test ./...`（2026-11-28，run `run_a457c36ba93d4ca4`）测得的覆盖率：

    Lines      86.36% (7457/8635)
    Branches   74.63% (3728/4995)
    MC/DC      47.13% (296/628)

行覆盖 86% 与 MC/DC 47% 之间相差 39 个百分点，这不是测量噪声，而是同一个问题的两面：复合条件被执行了，但没有被独立验证。

## 这是历史 issue #33 的未竟部分

`#33` 的验收标准之一是"拆分后无函数超过 70 行"，本次用 `go/ast` 精确复核：

    functions > 60 lines: 10 of 922
    over 70: 1  (internal/agent/agent.go runPreparedTool, 85 行)

即 #33 留下 10 个超 60 行的函数、1 个超 70 行的。而它的 `complex_conditional` 那部分从未立项处理。

## 核实过程

```
$ supercov runs latest gaps --limit 0
internal/tui/model.go          MC/DC conditions 29
internal/tui/file_picker.go    MC/DC conditions 21   (34 行未覆盖)
internal/llm/google/client.go  MC/DC conditions 19
cmd/neo/provider.go            MC/DC conditions 14
internal/agent/agent.go        MC/DC conditions 13
internal/tools/fs.go           MC/DC conditions 12
```

`internal/tui/file_picker.go`（358 行，34 行未覆盖、21 个 MC/DC 条件）是单位行数里最集中的缺口，且它已有 `file_picker_test.go`、`picker_test.go`、`attach_test.go` 三个测试文件，说明补的是分支而非整块功能。

## 范围

只补测试，不改产品代码。优先复合条件而非追行数：`file_picker.go` 与 `tui/model.go` 的未验证条件。

## 与已否决项的区别

- 「20 个测试有断言无覆盖证据」已核实**全部正当**（文档断言、子进程测试、常量表），不在本 issue。
- `internal/tools` 整包缺席是 supercov 的 Go 插桩限制（`docs/developer/code-quality.md` 已记录），修它要改测试包布局，另议。

## 验收标准

- 针对上表前两个文件，为每个此前未覆盖的 MC/DC 条件至少写一个用例；新增测试能在**删掉该条件的一个分支**时失败（手工验证一次即可）。
- `supercov -- go test ./...` 的 MC/DC 从 47.13% 提升，且行覆盖不回退。
- 单个测试不得依赖 sleep 或时序。
- `go test -race ./internal/tui/`、`go test ./...`、`qlty check --all` 通过。
