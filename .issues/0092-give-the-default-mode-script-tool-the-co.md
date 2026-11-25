---
id: 92
title: Give the default-mode script tool the console that solo mode already has
status: closed
priority: medium
labels:
    - bug
    - llm
relations:
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

#91 实现单工具模式（`features.codemode: solo`）时，把 `console`/`print` 只加在了 solo 的全局命名空间里。默认模式的 `script`（`features.script: true`）在同一处仍然缺失，实测确认：

```
default-mode console.log -> out="" err=script threw: ReferenceError: console is not defined
```

这条缺陷与 #91 修掉的那条是同一个：引擎没有 console，而模型写 JavaScript 时几乎必然先写 `console.log`。solo 模式修了，默认模式没修，于是同一个工具面在两种模式下行为不一致——这正是 #91 判"补能力"时记下的那类调用约定缺口。

## 目标

让默认模式的 `script` 也拥有 `console`（及 `print`），行为与 solo 一致，且不改变任何 prompt/schema 字节。

## 实现要点

- `internal/tools/script.go` 的 `soloExtrasWith`/`soloExtras` 目前把 console 与 `run()` 绑在一起，二者应拆开：console 是"任何 script 都该有"，`run()` 是"只有 solo 有"。
- 把 console 的绑定移到 `bindGlobals` 的无条件分支，`run()` 保留在 `if s.Solo` 内。
- 默认模式仍走 `scriptWrapper`（`eval` 包裹），console 必须是**全局**，不能只在 `tools.*` 下，否则 `console.log` 仍报未定义。
- 输出合并逻辑（`console.drain()` 并入结果、失败时保留已打印内容）在默认模式的无状态路径也要生效。
- 不引入跨调用状态：默认模式仍是每次调用新 `Runtime`，console 缓冲区按调用创建。

## 验收标准

- 单测：默认模式（`Script{}`，非 `Solo`）执行 `console.log("x"); 1` 返回含 `x` 与 `1` 的结果，不报错。
- 单测：默认模式 `print(...)` 同上。
- 单测：默认模式 `typeof run === "undefined"`（`run()` 仍只属于 solo）。
- 单测：默认模式脚本抛错时，抛错前打印的内容出现在错误信息里。
- `TestFixedPromptFloorBudget`（8,084）与 `TestScriptFloorBudget`（9,796）字节数**不变**：本任务只补运行时能力，不动任何文本。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 实施记录（2026-11-25）

已实现，三个预算字节均未变（8,084 / 9,796 / 2,970），符合"只补运行时能力、不动文本"的约束。

**改动**（`internal/tools/script.go`）：

- 从 `soloExtras` 拆出 `consoleGlobals`：console 与 `print` 属于**任何** script，`run()` 仍只属于 solo。这正是两种模式此前漂移的原因——它们被绑在同一个函数里。
- `bindGlobals` 现在无条件装配 console，`run()` 保留在 `if s.Solo` 分支内。
- 默认模式的无状态路径原先直接 `SetGlobal("tools", ...)`，绕过 `bindGlobals`，所以拿不到 console；改为调用 `bindGlobals`。
- 抽出 `consoleLog.join` 与 `consoleLog.describeFailure`，让三条执行路径（无状态、`Session`、solo）共用同一份合并与失败保留逻辑，避免三份实现各自漂移。
- `limitOutput` 去掉了已无用的 `error` 入参（调用方都已先处理错误）。

**为何 console 必须是全局**：默认路径用 `scriptWrapper` 的 `eval(source)`，`eval` 只能看到全局作用域，`tools.console` 不会被 `console.log` 解析到。

**测试**（`internal/tools/script_console_test.go`）：

- `console.log` 与 `print` 的输出并入结果，且值不丢失。
- `typeof run === "undefined"`：默认模式仍没有 `run()`。
- 抛错时保留抛错前打印的内容。
- console 不引入状态：第二次调用看不到第一次的输出。

**端到端**：真实二进制、默认模式、`features.script: true`，一次脚本内 `console.log` 行数并返回匹配行——2 requests、3 tool calls、**0 errors**。修复前该调用会直接 `ReferenceError`。
