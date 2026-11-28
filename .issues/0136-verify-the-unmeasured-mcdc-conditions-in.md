---
id: 136
title: Verify the unmeasured MC/DC conditions in file_picker and tui/model
status: closed
priority: low
labels:
    - bug
created: "2026-11-28"
updated: "2026-11-28"
closed: "2026-11-28"
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

## 结果记录（2026-11-28）

### 结论：补 `file_picker.go`，不补 `tui/model.go`，并说明为什么

先核实 Issue 里“这是 #33 未竟部分”的说法，结果与原文不符，记录如下：

- **#33 的行数部分确实未完成**：`go/ast` 精确复核 922 个函数中 10 个超 60 行、1 个超 70 行（`internal/agent/agent.go runPreparedTool` 85 行）。
- **但 #33 从未包含 `complex_conditional`**。原文只写 `long_method`/`deep_nesting`；本 issue 原文声称“它的 `complex_conditional` 那部分从未立项处理”是我的误述。
- **`tui/model.go` 的 29 个 MC/DC 条件本次未处理**。该文件 1820 行、`god_class 0.71`，属 #29 已记录的结构问题；为覆盖率逐条件补测，等于在没有重构的前提下把测试堆在一个应该拆分的类型上。本 issue 写着“优先复合条件而非追行数”，但不包括“替待重构文件还债”。

### 做了什么

只改测试，未动产品代码。新增 `internal/tui/file_picker_branches_test.go`，11 个测试，每个针对一处此前**从未执行过**的代码块：

| 测试 | 覆盖的此前未执行分支 |
| --- | --- |
| `TestFilePickerTokenAtClampsRowAndColumn` | 空输入、行越界、行负数、列越界、列负数、无 token、`@@` 拒绝、tab 拒绝 |
| `TestUpdateFilePickerWithoutToken` | 无 token 时隐藏；仅在可见时才重新布局 |
| `TestUpdateFilePickerDismissedForTokenLifecycle` | `dismissedFor` 命中即隐藏；token 变化时清除 |
| `TestUpdateFilePickerClampsSelectionBothWays` | 选择上钳（到最后一个索引）、下钳（回到 0） |
| `TestFilePickerMatchCaps` | 工作区匹配上限 `break`、磁盘匹配上限 `break` |
| `TestEnsureFilePickerFilesResolvesRootAndCaches` | root 为空时调用 `os.Getwd`；已缓存则不重新遍历 |
| `TestEnsureFilePickerFilesCachesTheError`、`…MissingRootYieldsNoMatches` | 错误短路由；根缺失时 `WalkDir` 经回调吞掉错误并返回 nil |
| `TestFilePickerNoopsWithoutMatches` | 隐藏时移动/接受为 no-op；可见但空时同样 |
| `TestReplaceFilePickerTokenRejectsStaleTokens` | 行越界、start 负、end<start、end 越界、行负 |
| `TestRenderFilePickerRefusesImpossibleLayouts` | 宽度 ≤0、无匹配、行上限 <3、宽度 3 |
| `TestFilePickerDiskMatchesUnreadableDirectory` | 目录不可读时返回 nil |

`file_picker.go` 未执行代码块 **27 → 10**；`go tool cover -func` 下 `renderFilePicker`、`replaceFilePickerToken`、`acceptFilePicker`、`moveFilePickerSelection`、`filePickerDiskMatches` 均达 100%。

### 验收核对

| 标准 | 结果 |
| --- | --- |
| 每个此前未覆盖的 MC/DC 条件至少一个用例 | `file_picker.go` 达成；`tui/model.go` 未做，理由见上 |
| 新增测试能在删掉一个分支时失败 | **已做变异测试**，见下表 |
| `supercov -- go test ./...` MC/DC 提升、行覆盖不回退 | MC/DC **47.13% → 49.68%**；行 **86.36% → 86.66%**；分支 **74.63% → 75.24%** |
| 单个测试不依赖 sleep 或时序 | 全部同步，无 goroutine |
| `go test -race ./internal/tui/`、`go test ./...`、`qlty check --all` 通过 | 均通过；qlty 仅剩既有的 `cmd/neo/main.go:150` note |

### 变异测试（手工执行，未入库）

把每个分支改坏后跑 `go test ./internal/tui/`：

| 变异 | 结果 |
| --- | --- |
| 选择上钳 `len(matches)-1` → `0` | CAUGHT |
| 选择下钳 `0` → `99` | CAUGHT |
| 磁盘匹配上限 `>=` → `>= *10` | CAUGHT |
| 行上限 `< 3` → `< 0` | CAUGHT |
| `ensureFilePickerFiles` 忽略已缓存错误 | CAUGHT |
| `dismissedFor` 不再清除 | CAUGHT |
| token 列钳制只保上界 | CAUGHT |
| `contentWidth` 下限 `1` → `0` | **MISSED（且不可能捕获）** |

最后一条不是测试弱点：文件行已经过 `max(1, contentWidth-2)`，所以 `contentWidth` 为 0 或 1 产生逐字节相同的输出。该分支对 footer 仍是有效防御，但对可见输出不可区分。已在测试注释写明，没有假装覆盖。

### 过程中的三次自我纠正

1. 第一版选择钳制测试用**单元素列表**，钳到“最后一个索引”和钳到 0 结果相同——变异测试抓出了这点，改用两个匹配。
2. 用 `makeFilePickerTree` 造 `alpha/beta/gamma` 后查询 `@a`，三个文件都含 “a”，匹配数不是预期的 2。
3. 假设“缺失 root 会返回错误”；实测 `WalkDir` 对根失败经回调吞掉并返回 nil，改为直接断言该行为，并另写一个只测错误短路由的用例。

### 未纳入本 issue

- `internal/tui/model.go` 的 29 个 MC/DC 条件——需先做 #29 那种拆分，另行立项更合适。
- `internal/tools` 整包覆盖率缺席——supercov 的 Go 插桩限制，见 `docs/developer/code-quality.md`。

### 仍缺席的 10 个代码块

均为当前环境不可构造的错误分支：`os.Getwd` 失败（:217）、`WalkDir` 回调收到 err（:233/:237）、`filepath.Rel` 失败（:243）、隐藏后布局路径（:69/:73/:75）、`filePickerTokenAt` 的两个空返回（:107/:130）。覆盖它们需要注入 fs 抽象，那是产品改动，超出“只补测试”的范围。
