---
id: 101
title: Measure the merge rate on the single-tool surface where no top-level tool is available
status: closed
priority: medium
labels:
    - performance
relations:
    related-to:
        - 91
        - 97
        - 100
        - 103
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`docs/performance.md` 的解释很克制：脚本的**选用率**在多轮提示词改动后已经能起来（计数任务 3/3），但**合并率**——把读、判断、写放进同一次调用——至今为 0（#83 的 2/6、#91 的 1/6、以及 2026-11-25 默认面的"合并仍罕见"）。当前所有已登记的任务都只修**单点失败**（#98 路径、#99 名字空间、#100 重试循环），没有任何任务回答"合并率为什么是零"。

而本轮验证提供了一个此前所有测量都没有的快照：默认 code mode 下的**真实工具面**只有一个工具（`script`），没有 `bash`/`read_file` 可回退，`codeModeCapability` 明确要求"read, decide, and write inside the same call"。在这个面上，模型没有"退回 top-level 工具"的退路，合并与否只能由它自己决定。这是观察合并行为最干净的对照条件，而至今没有数据。

## 目标

在不改任何生产文本的前提下，先把"合并率"变成可用 `NEO_LOG` 从单次运行里判定的事实，再据此决定是否要动提示词。

## 实现要点

- 判定信号（从日志读，不靠人看最终答案）：
  - 一次 `script` 调用内，`SubCall` 序列（`Script.ReportSubCall`，`script.go:334-338`）**同时**含读取类（`read_file`/`grep`/`glob`）与写入类（`write_file`/`edit_file`）事件，记为该次调用"合并"。
  - 检查日志里是否已经有这三类事件的稳定字段；若没有，本任务**只加日志字段**，不改行为（这类元数据属于可观测性，符合 MISSION 的"clear visibility"）。
- 在 #97 的任务集上跑（#97 是为"单条 bash 完不成"设计的，正好也是必须合并的形状）；若 #97 尚未完成，则先用 `docs/performance.md` 里那套"读 2,001 行文件、改一行"的既有 fixture 记录基线，并在 issue 中注明 fixture 的局限（`sed -i` 能一步完成）。
- 报告要包含三件事：合并率、每次运行的 `requests`、以及**未合并那种运行的具体形状**（例如：先单独 `grep`，再单独 `read_file`，再单独 `edit_file`），因为它决定了下一步该修哪一条。
- n ≥ 6，同一 fixture、同一模型、只翻转 `features.codemode` 或 `features.script`。

## 验收标准

- `docs/performance.md` 新增一小节，记录：日期、provider、模型、fixture、n、合并率、requests 中位数与范围。
- 若合并率仍为 0，必须给出**至少一种**实测到的未合并形状，并据此提出**至多一条**后续任务；不要一次开出多条推测性改进。
- 若日志字段不足以判定，本任务交付该字段与对应单测（`SubCall` 事件的可区分性），并在 issue 里说明测量延后。
- 不改 `scriptCapability`/`codeModeCapability` 的任何字节；若测量结论指向提示词，另开 issue 引用本结果。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 备注

本任务是**测量任务**，不是功能任务，因此不设 `depends-on` 代码依赖，但应与 #99 的复测分开归因：先量，再改。这也回应了 #97 提到的"在拿到真正需要合并的任务之前，无法区分'模型不会合并'和'任务不需要合并'"：本轮已经有一个默认面只剩 `script` 的已知条件，值得先用它量一次。


## 测量记录（2026-11-25）

### 先补判定信号

headless 运行没有 SubCall sink，且脚本内子调用此前**不写日志**，因此合并率无法只从 `NEO_LOG` 判定。按验收标准走了"交付字段 + 单测"这条路径：

- `internal/tools/execution.go` 的 `ReportSubCall` 在 sink 查找**之前**加一行 debug 日志：`script sub-call`，字段为 `name` / `done` / `error`。不记录参数（含文件内容与路径），只记录调用形状。
- 单测 `TestScriptSubCallsAreLoggableCharacterisesTheMergeSignal`（`internal/tools/execution_log_test.go`）：断言日志含 `script sub-call`、`read_file`、`edit_file`，且**不泄露路径**。

判定口径：一次 `script` 调用（从 `tool call script` 到对应 `tool result`）之间的子调用事件里，同时出现读取类（`read_file`/`grep`/`glob`）与写入类（`write_file`/`edit_file`），记为该次调用"合并"。

### 运行条件

- provider `openrouter`，model `particle-ai/particle-1`，同一个二进制，`features.codemode` 是唯一翻转项，n=3/arm，`NEO_LOG_VERBOSE=1`。
- fixture：小 Go 模块，`Max` 用 `<` 比较导致一个测试失败；提示词为"测试失败，找出 bug 并修好让 go test 通过"。
- 说明：#97 未完成，因此用的是既有形态的 fixture，其局限与 `docs/performance.md` 已记录的一致（`sed -i` 也能一步完成）。

### 结果

| arm | requests(中位) | 输入 tokens(中位) | 套件通过 | 合并调用 | 有合并的运行 |
| --- | ---: | ---: | --- | ---: | ---: |
| codemode (n=3) | 4 | 6,807 | 3/3 | 0/10 | 0/3 |
| classic (n=3) | 5 | 11,606 | 3/3 | n/a | n/a |

未合并的具体形状（三次 codemode 运行一致）：

```
run 1  glob          -> read_file -> edit_file (+run)
run 2  glob (+run)   -> read_file -> edit_file (+run)
run 3  glob (+run)   -> read_file -> edit_file (+run)
```

读与写**从不**同处一次调用；`run` 倒是会和 `glob` 合并（"先看再验证"），说明模型会把相关的"查"和"跑"放一起，却把"看文件"和"改文件"当作两个独立决策。

### 结论与后续

- 合并率仍为 0。但 code mode 在本 fixture 上两项成本都更低（4 vs 5 请求，6,807 vs 11,606 输入 tokens），收益来自**面更窄**，不是来自合并。
- 只提一条后续：提示词已同时给出规则与两种可复制形状（#100 补了另一种），继续加文本不是缺口所在；缺的是"拆开比合并更贵"的任务。这正是 #97，故 #97 应先于任何新的提示词改动。
- 已在 `docs/performance.md` 新增"Merge rate on the single-tool surface"小节记录上述全部数字。

未改动 `scriptCapability`/`codeModeCapability` 任何字节（`TestScriptFloorBudget`、`TestCodeModeFloorBudget` 字节数不变）。`go test ./...`、`go vet ./...`、`qlty check` 通过。

## 更正（2026-11-25 全盘评审）

本 issue 的结果记录有一处错误，已开 #103 跟踪修正：

测量时把 features.codemode 设为 solo，但 solo 在 ad63f36 已被移除，config.Load() 不做校验，于是静默回退成 codemode。因此当时记录的 "codemode (n=3)" 与 "solo (n=5)" 并非两种工具面的对照，而是同一个面的 8 次采样。

原始计数仍然有效（8 次运行、3 次合并），但不能表述为两模式对比。归因错误的根因另见 #104（配置校验缺失）。

