---
id: 102
title: Refuse or warn when the default code mode would start a headless session with zero tools
status: closed
priority: high
labels:
    - config
    - cli
relations:
    related-to:
        - 98
        - 99
        - 106
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

本轮验证确认（`features.codemode: codemode`，headless `neo run`，桩服务记录实际请求）：

| 场景 | 发给模型的工具 |
| --- | --- |
| 默认 | 只有 `script` |
| `features.script: false` | **无工具** |

最后一行是实测：`registryFor`（`cmd/neo/main.go:515-521`）在 `scripted == false` 时把 `base` 置为 `nil`，而 headless 恰好不传协调工具（`agent`/`goal`/`workflow` 是交互面才有），于是请求里没有 `tools` 字段。

对**交互面**这是有意的：只剩 `agent`/`goal`/`workflow`，模型仍能委派。对 **headless** 这是静默失能：`neo run` 可以正常启动、正常回话，但它永远无法读写文件或执行命令，而且**没有任何地方告诉用户这一点**。

这正是 zero-shot 里最坏的一类失败：不是工具用错，而是**没有任何工具**，模型会退化成只输出文字建议，用户在收到"改好了"的文本后才发现文件没变。

## 现状核查（2026-11-25 实测）

- `cmd/neo/main.go:516-521` 的注释只提到"leaves only the coordinating tools, which is the file-and-command-free session"，没有覆盖 headless 下协调工具为 0 的情况。
- `docs/developer/config.md:200` 对 `script` 写的是"Set it to false for a session with no code execution"，与实现一致，但没有说明它在默认 code mode + headless 下的实际后果。
- `neo doctor` **不检查**这一组合：用 `features: {script: false}` 的配置实跑，七项检查全部 `pass`/`warn`（只有未建 session 目录、非 git 工作区两条 `warn`），退出码 0。

## 目标

让"默认 code mode + 关闭 script + headless"这一组合**显式失败或显式警告**，而不是静默进入无工具状态。

## 实现要点

- 首选：在 `execute`/`run` 的启动路径里，当 surface 为 code mode 且 `ScriptEnabled()` 为假且无协调工具时，直接报错退出，消息指明修法（`features.codemode: classic` 或 `features.script: true`）。
- 次选（若判定"无工具会话"是合法用途）：保留行为，但在 headless 的 stderr 打一条一行警告，并在 `neo doctor` 里报告。
- 交互面行为保持不变：`/codemode off` 后仍有 `agent`/`goal`/`workflow`，**不要**因此拒绝启动。这需要把"是否有协调工具"作为判断条件，而不是只看 `scripted`。
- 不改 `registryFor` 的默认分支（有 script 时的工具面），也不动任何 prompt/schema 字节。

## 验收标准

- 单测：code mode + `script: false` + headless → 启动报错（或按所选方案打警告），消息含可执行的修法。
- 单测：code mode + `script: false` + 交互面（有协调工具）→ 不报错，工具面只剩协调工具。
- 单测：classic + `script: false` → 行为与改动前一致（仍有 bash 等）。
- `docs/developer/config.md` 的 `script` 行补充这一后果；`CHANGELOG.md` 记录。
- 三个 floor 预算字节数不变（本任务只改启动检查，不改文本）。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 备注

属于"让失败可见而不是静默"这一类（MISSION 的 engineering principles），与 #98/#99/#100 修的是不同层：它们修"用了但用错"，本任务修"根本没用上"。因此在依赖链上放在最末，但它不阻塞任何其它任务。


## 修复记录（2026-11-25）

采用 issue 的首选方案：在 headless 启动路径显式失败。

- `cmd/neo/main.go` 的 `headlessAgent`：构建 registry 后若 `len(reg.Names()) == 0`，写一条 stderr 并以 `ok = false` 返回，**在第一次 provider 请求之前**。判断条件是"实际没有任何工具"，而不是"只看 scripted"，所以交互面（带 `agent`/`goal`/`workflow`）不受影响。
- 新增 `surfaceName(cfg)`，消息里复用 `config.CodeModeCodemode`/`CodeModeClassic` 的值，不另造词。
- 修正 `registryFor` 里"leaves only the coordinating tools"的注释，补上 headless 没有协调工具这一前提，并指出由 `headlessAgent` 拒绝该组合。

实际输出：

```
$ neo run hi      # features: {codemode: codemode, script: false}
修复前: exit 0, stdout "should not be asked"    （无工具，静默失能）
修复后: exit 1, stderr: neo run: features.script is false in codemode mode, which leaves this session with no tools: set features.script: true, or use features.codemode: classic for bash and the file tools
```

新增测试（`cmd/neo/headless_notools_test.go`）：
- `TestHeadlessRunRefusesSessionWithNoTools`：走真实 `run()` 入口，断言非零退出、stdout 为空、stderr 含两种修法、且**一次请求都没发出**。回退守卫后该测试失败（`run exited 0`），保留下来可防回归。
- `TestRegistryForNoScriptSurface`：code mode + script off → 0 个工具；补上协调工具 → 非空（交互面仍可启动）；classic + script off → 仍有 `bash`。

文档：`docs/developer/config.md` 的 `script` 行补充了在 code mode 下与 `neo run` 组合的后果；`CHANGELOG.md` 在 Unreleased/Changed 记录。

验证：`go test ./...`、`go vet ./...`、`qlty check` 全部通过；prompt/schema 字节未改动（三个 floor 预算测试均通过）。
