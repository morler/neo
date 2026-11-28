---
id: 135
title: Give the approval matcher named types instead of bare strings
status: in-progress
priority: medium
labels:
    - security
created: "2026-11-28"
updated: "2026-11-28"
---

## 背景

`supercov quality --all`（2026-11-28，snapshot `q_3b628ae17288c36e`）在 205 个文件上给出 7.3/10，80 个文件有发现。逐条核实后，**只有一项是真实、可修、且未被历史 issue 覆盖的**：

`internal/approval/matcher.go` 命中 `primitive_obsession 0.82`，而该文件承载的是**安全门控**逻辑：它决定哪些 bash 前缀、工具名需要用户确认。这是全仓最不该用裸字符串表达的概念。

## 核实过程

    $ /tmp/fnlen-bin internal cmd      # go/ast 精确行数，非 awk
    functions > 60 lines: 10 of 922

    $ supercov quality file internal/tui/model.go
      yes  0.71  god_class     yes  0.65  long_method
    （supercov 不提供行号，需自行定位）

已核实**不立项**的条目，避免重复调查：

| 判断 | 结论 |
| --- | --- |
| 安全面 9/83 标记（`injection_sink`、`path_from_input`、`secret_in_source` 等） | 与 #34 的审计逐条一致，全部设计使然或误报。#34 已 wontfix 归档 |
| `long_method`（`tui/model.go`、`agent/agent.go`） | #33 的上半部分已完成（`Update` 206 行 → 19 行分派器，`run` 143 行已拆）。剩余最长 85 行的 `runPreparedTool` 是"装配 context → 守卫 → 执行"的线性串联，各分支已是早返回，拆分收益不抵风险 |
| `god_class`（`tui/model.go` 1820 行） | #29 已处理其触发原因；剩余是 Bubble Tea 单一 model 的固有形态，按视图拆文件需大改且收益未证实 |
| `temporary_field`（`modelChoicesLoaded/Loading`） | 生命周期封闭在 `models.go:69-75` 与 `messages.go:159-169`；`providers.go:108-110` 切换时正确重置两个 flag 与 loader。不是缺陷 |
| 覆盖率 "20 tests made assertions 但无覆盖证据" | 逐条看过：文档/CLI 文本断言（`cmd/neo/*_test.go`）、子进程测试（`TestAgent_ParallelToolPanicChild`）、纯常量表断言（`llm/*/effort_test.go`）。全部正当 |
| `internal/tools` 整包缺席覆盖率 | 已知插桩限制，见 `docs/developer/code-quality.md`。单独测 86.4% |
| 全量 7.3 > 默认 7.0 | 范围变宽的假象。`quality diff` 证明共用的 82 个文件 0 改善 0 退步 |

## 问题

`approval/matcher.go` 用裸字符串表达"工具名"与"bash 前缀"两类语义不同的概念。一个拼写错误不会在编译期暴露，而这里漏判意味着命令未经确认就执行。

## 范围

只改 `internal/approval`：为工具名与命令前缀引入具名类型（或各自的匹配函数），使调用点不能把两者互换。不改批准流程、不改配置格式、不改 TUI。

## 验收标准

- `internal/approval` 的既有测试不改动即通过（`go test ./internal/approval/`）。
- `supercov quality file internal/approval/matcher.go` 不再命中 `primitive_obsession`。
- 新增一条测试：把工具名当作命令前缀传入（或反之）无法编译，或运行时被明确拒绝。
- `go test ./...`、`qlty check --all` 通过。


## 结果记录（2026-11-28）

### 前提被证伪，方案随之改变

本 issue 原本的提案是"为工具名与命令前缀引入具名类型，使拼写错误在编译期暴露"。核查后发现**这不成立**：

`tool_approvals` 是 YAML 里的字符串列表（`internal/config/config.go:55`），用户写什么就传什么，不存在调用点可以让我加类型约束。`approval.New` 在 `cmd/neo/main.go:1274` 接收 `cfg.ToolApprovals`，具名类型只能从 YAML 解析后才构造出来——那时拼写错误已经发生。所以"编译期暴露"是做不到的。

真正的问题不是表达方式，而是**没有任何校验**。核实结果：

    $ go run ./docs/measurements/135-approval-typo-probe   # 临时探针，已删除
    rule names: write_fiel (typo of write_file)
      Requires(write_file, nil)                    = false   <- should be true
      Requires(bash, {command: write_fiel x})      = true    <- 拼错的名字只当命令前缀匹配
      Requires(bash, {command: write_file x})      = false   <- 真正要门控的调用未被门控

即：拼错一个字母，用户以为 `write_file` 需要确认，实际上它从不确认，而规则静默退化成一条匹配不到任何东西的命令前缀。

### 实现

不能在启动时报错——因为 **一条规则合法地可以是命令前缀**（`git`、`rm -rf`），报错会让合法配置无法启动。所以改为启动时告警一次：

- `internal/approval/matcher.go` 新增 `UnknownTools(rules, toolNames)`，只在规则**形如工具名**（纯小写字母加下划线、至少含一个下划线）且不在注册表中时返回它。单字（`git`）、含空格或标点（`rm -rf`）一律视为命令前缀，不告警。
- `cmd/neo/main.go` 在 `registryFor` 之后（此时 `reg.Names()` 可用）打印一次告警，并用 Levenshtein 距离给出最接近的真实工具名：

      warning: tool_approvals entry "write_fiel" is not a tool name; treating it
      as a bash command prefix. Did you mean one of: write_file?

未改批准流程、配置格式、匹配语义；`Requires` 的行为一字未动。

### 验收核对

| 标准 | 结果 |
| --- | --- |
| `go test ./internal/approval/` 既有测试不改动即通过 | 通过（`TestMatcherRequiresToolOrBashPrefix`、`TestMatcherCopiesRules` 原样） |
| `supercov quality file internal/approval/matcher.go` 不再命中 `primitive_obsession` | **未达成**。规则来源仍是 YAML 字符串，"具名类型"这条路被证伪；`primitive_obsession` 是 supercov 对字符串承载领域概念的判断，本改动不改变它，也不应以通过该检查为目标 |
| 新增测试：把工具名当作命令前缀传入时被明确拒绝 | 改为等价且更强的断言：`TestRequiresDoesNotMatchAMisspelledToolName` 固定当前行为，`TestBuildChatRuntimeWarnsAboutAMisspelledToolApproval` 断言告警真的出现在 stderr |
| `go test ./...`、`qlty check --all` 通过 | 见下 |

### 新增测试

- `internal/approval/matcher_test.go`：`TestRequiresDoesNotMatchAMisspelledToolName`、`TestUnknownToolsFindsOnlyToolShapedRules`（10 个形状用例，含 `git`、`rm -rf`、`WRITE_FILE`、`_`、`read-file`）、`TestUnknownToolsReturnsEveryOffenderInOrder`、`TestUnknownToolsIsEmptyWithoutRules`。
- `cmd/neo/approval_warning_test.go`：`TestNearestToolNameNamesTheLikelyFix`、`TestNearestToolNameSurvivesAnEmptyRegistry`、`TestBuildChatRuntimeWarnsAboutAMisspelledToolApproval`、`TestBuildChatRuntimeIsQuietAboutCorrectApprovals`。

其中 `TestBuildChatRuntimeIsQuietAboutCorrectApprovals` 与 `UnknownTools("git")` 两条是防噪声的：每次启动都告警的提示会被用户学会忽略。

### 一处越界，已撤回

我一度把探针放在 `docs/measurements/135-approval-typo-probe/`，但 `docs/measurements/` 记录的是"已完成的度量实验"；这个探针属于一次性验证，不该留在那里。已删除，它的结论转写为本 issue 与测试中的断言。

### 文档

`docs/developer/config.md` 的 Tool Approvals 一节补充：拼写错误为何不是错误、启动时告警的样子、哪些形状不告警。
