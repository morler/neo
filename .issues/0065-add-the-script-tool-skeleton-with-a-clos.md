---
id: 65
title: Add the script tool skeleton with a closed tool whitelist and RunScript bridge
status: closed
priority: high
labels:
    - feature
relations:
    blocks:
        - 67
    depends-on:
        - 64
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

原 #58（运行时桥接）拆成 3 步，否则一次改动同时涉及“工具注册、运行时池、资源限制、白名单、值转换”，失败后无法定位。这是第 1 步：**只跑通一条最短路径**——脚本能读一个文件并返回字符串。

实测语义（moejs，已在本地验证）：

- `CompileScript` + `Runtime.RunScript` 是**表达式求值**路径：`1 + 41` 得到 `42`，最后一条表达式的值即结果。
- classic script **不支持顶层 `await`**（`SyntaxError`）。
- `Compile` + `Hook("default")` + `Call` 拿到的是 `[object Promise]` 而不是值——本方案的宿主函数全部同步，`async` 只增加一层无用包装。

> ponytail 评审：原计划写“内部模块 + 约定导出函数 + JSON 序列化”三层，对应的是 `Compile`/`Hook` 路径，既拿不到值也解决不了问题。改为 `CompileScript`/`RunScript` 一条路径。

## 目标

新增 `internal/tools/script.go`：一个 `script` 工具，接受 JS 源码，在单次运行时里执行，只能调用 `read_file`。

## 实现要点

- 工具名 `script`，输入 schema 只有 `source`（string，必填）。description 写明“**最后一条表达式的值即工具输出**”，这比任何抽象都省。
- 每次调用 `moejs.NewRuntime(moejs.Options{})`，用完即弃（此步不做池化——`ParallelSafe` 恒为 false，没有第二个消费者）。
- 白名单是**封闭**的：`SetGlobal("tools", ...)` 只放 `read_file`。不要用“注册表过滤”这种可被后续误扩的方式表达，直接在构造处硬编码并在注释写明“新增工具必须在这里显式添加”。
- 值转换：入参 `rt.ToGoStrict(args[0])` 得到 `map[string]any`；返回值用 `rt.ToGo` 转成 Go 值，字符串直接返回，其他类型 `JSON.stringify` 后返回（在 JS 里包一层最简单，不必在 Go 侧再写序列化分支）。
- 错误：编译错误、运行期异常两类都返回 `isError=true` 且消息可读，不要 panic。`RunScript` 返回 `nil` 脚本时也要有可读错误。
- 此步**不做**：池化、内存限制、超时、其他工具、子事件、审批。

## 验收标准

- 单测：`tools.read_file({path:"x"}).length` 返回文件长度。
- 单测：`tools.bash` 与 `tools.write_file` 为 `undefined`（断言白名单封闭，而不是“调用时报 unknown”）。
- 单测：语法错误与运行期 throw 各自有独立且可读的消息。
- 单测：`async`/`await` 的脚本给出明确错误而不是静默返回 Promise（断言错误信息提到不支持）。
- `go test -race ./internal/tools` 通过。
- `docs/developer/tools.md` 表格新增 `script` 一行，注明“仅 `read_file`、无超时与内存限制（后续任务补齐）”。
