---
id: 82
title: Restore cmd/neo coverage above 70% by testing the chat runtime switches
status: closed
priority: medium
labels:
    - test
    - cli
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

2026-11-25 用 `supercov quality` / `supercov security` / `supercov -- go test ./...` 做全仓评审时发现：#32 的验收条件「`cmd/neo` 语句覆盖率 ≥ 70%」已不再成立，而 #32 已关闭。

## 实测数据（可复核）

    $ go test ./cmd/neo/ -coverprofile=/tmp/cmdnow.out
    ok  github.com/owainlewis/neo/cmd/neo  coverage: 68.7% of statements
    $ go tool cover -func=/tmp/cmdnow.out | tail -1
    total:  (statements)  68.7%          # 582/847

对照 #32 关闭时所在的提交 `9356ae2`（干净 worktree 复测）：

    total:  (statements)  71.3%          # 539/756

即分母从 756 涨到 847，分子只从 539 涨到 582。`go test ./...` 全绿，所以这是覆盖率缺口，不是行为损坏。

## 0.0% 的函数（9 个，均用 `go tool cover -func` 核对）

| 位置 | 函数 | 语句数 |
| --- | --- | --- |
| `cmd/neo/main.go:1160` | `switchCodeMode` | 20 |
| `cmd/neo/main.go:1211` | `switchProvider` | 20 |
| `cmd/neo/main.go:1078` | `resolveChatProvider` | 13 |
| `cmd/neo/main.go:1191` | `switchModel` | 12 |
| `cmd/neo/main.go:1016` | `buildChatRuntime` | 12 |
| `cmd/neo/main.go:1136` | `chatExtraTools` | 11 |
| `cmd/neo/main.go:1110` | `ensureChatSession` | 7 |
| `cmd/neo/main.go:126` | `main` | — |
| `cmd/neo/provider.go:109` | `runLogin` | — |

前 7 个共 95 条语句，全部位于 `73e70de`（codemode opt-in）与 `9356ae2`（拆分 chat 运行时）新增/拆出的代码里。

## 为什么值得测

`switchCodeMode`（`cmd/neo/main.go:1160-1189`）有三条回滚路径，每一条都是"失败必须不留痕迹"的语义：

- `SetTools` 失败 → 恢复 `r.cfg.SetScript(previous)`；
- `WriteFeature("script", on)` 失败 → 恢复设置、重建 `registryFor` 与 `chatSystem`、再次 `SetTools`/`SetPrompt`，然后返回错误；
- `previous == on` → 直接返回 nil，不写文件。

`switchModel`（`:1191`）同样有 `WriteBackend` 失败时 `restoreChatModel(...)` 的还原路径。这些分支用假 provider 与临时 HOME 即可确定性测试，不需要真实网络或终端，符合 #32 定下的"只补可确定性测试"原则。

## 验收

- `go test ./cmd/neo/ -coverprofile` 总语句覆盖率 ≥ 70%。
- 上表前 7 个函数不再是 0.0%，且 `switchCodeMode` / `switchModel` 的回滚分支各有一条断言。
- `go test ./...`、`go vet ./...`、`gofmt -l .`、`qlty check --all` 干净。

## 完成情况

新增 `cmd/neo/chatruntime_test.go`（27 个测试），覆盖 chat 运行时的装配与三个开关。全部用内联声明的自定义 provider 与 `llmtest.FakeProvider`，不需要网络、终端或真实凭据；需产生写入失败的用例把 `HOME` 指向普通文件，而非依赖权限位。

| 函数 | 之前 | 之后 |
| --- | --- | --- |
| `switchCodeMode` | 0.0% | 90.0% |
| `switchProvider` | 0.0% | 100.0% |
| `switchModel` | 0.0% | 100.0% |
| `resolveChatProvider` | 0.0% | 100.0% |
| `buildChatRuntime` | 0.0% | 100.0% |
| `chatExtraTools` | 0.0% | 100.0% |
| `ensureChatSession` | 0.0% | 100.0% |

`switchCodeMode` 剩下的 10% 是 `SetTools` 返回错误的分支：`Agent.SetTools` 只在 registry 为 nil 时失败，而 `registryFor` 永不返回 nil，该分支无法从外部构造。`main` 仍为 0.0%，因为它调用 `os.Exit`。

重点覆盖的是"失败必须不留痕迹"的语义，而不是行数：

- `switchCodeMode`：`WriteFeature` 失败后配置与 registry 都回到原状，脚本工具确实不可再调用（用 `agent.RunTool` 实测 `unknown tool`）。
- `switchModel`：拒绝空模型；`WriteBackend` 失败后 agent 模型与 `providerModels` 未记录时从 agent 读取的还原路径。
- `switchProvider`：未知名字、无默认模型、缺凭据三种拒绝都不改变当前后端；`WriteBackend` 失败后 provider 与 model 都还原。

## 验证

    $ go test ./cmd/neo/ -coverprofile=/tmp/new3.out
    ok  github.com/owainlewis/neo/cmd/neo  coverage: 80.6% of statements   # 之前 68.7%
    $ go tool cover -func=/tmp/new3.out | awk '$NF=="0.0%"'
    github.com/owainlewis/neo/cmd/neo/main.go:126:  main  0.0%

全仓：

    $ go test ./... -cover      → 85.72% statements（之前 84.26%）
    $ go test -race ./...       → 全部通过
    $ go vet ./...              → 无输出
    $ gofmt -l .                → 无输出（除 .supercov/）
    $ qlty check --all          → exit 0；1 个既有 comment 模式 note
                                  （cmd/neo/main.go:119 提示词字符串内的 JS 示例被 ripgrep 判为 TODO，
                                   干净 HEAD 上同样存在，非本次引入）

顺带把评审发现的 supercov 插桩限制写入 `docs/developer/code-quality.md`「Coverage has one known blind spot here」一节：同时存在 `package tools` 与 `package tools_test` 的包里，supercov 生成的 `__supercovTest` 只注入内部包，外部测试文件编译失败，整包从覆盖率中消失。已用最小模块（仅内部测试、仅外部测试、两者并存）验证，只有并存时失败。
