---
id: 99
title: Make a ReferenceError on a whitelisted tool name teach the tools.<name> form
status: closed
priority: high
labels:
    - tools
    - prompting
relations:
    depends-on:
        - 98
    related-to:
        - 85
        - 91
        - 102
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

2026-11-25 用桩服务对默认 code mode 做端到端验证时，我的两个验证脚本都踩了同一个坑，而且第一次的报错**完全没有指向修法**：

```
写 read_file({path: "hello.txt"})
-> script threw: ReferenceError: read_file is not defined
```

工具在 `tools.*` 名下（`internal/tools/script.go:271-286` 的 `globals`），而 `run()`/`console` 在顶层（`:282`、`:293-305`）。模型在**同一个脚本**里同时用三种形状：

- `tools.glob({pattern:"*"})` —— 对象参数，必须在 `tools` 下
- `run(["git","status"])` —— 数组参数，顶层
- `console.log("x")` —— 位置参数，顶层

于是它很容易把 `read_file(...)` 写成顶层，得到一条只说"未定义"的 `ReferenceError`。

## 为什么值得修

- `ReferenceError` 是引擎对**任意**未定义变量给出的通用错误，模型无法区分"拼错了名字""忘了 `tools.`""记错了旧版本 API"。它唯一的线索是"重新读一遍提示词"，这正是 zero-shot 失败一次后不再使用该工具（#80 记录：任务 2、3 全程 0/3）的形态。
- #85 已经确立了"失败结果自带可复制修法"的原则，且已覆盖 `await` 与 `edit_file` 两条；**名字空间这一条是同类里最高频、却还没覆盖的**。
- 提示词本身已经声明了 `tools.`（`codeModeSpec` 与 `Spec` 都写了 `tools.read_file`），所以这不是"没说"，而是"说了但没兜住"。按 #84/#87 的教训，再往提示词加字节是低效的；正确位置是错误路径，成本只在失败那一次支付。

## 目标

当脚本因为把白名单工具当作顶层函数调用而失败时，错误信息直接给出正确写法。

## 实现要点

- 位置：`describeScriptError`（`internal/tools/script.go:423-449`），在 `moejs.Exception` 且 `Name() == "ReferenceError"` 的分支追加提示，与现有 `awaitHint`/`editFileHint` 同一层，**只加一行**。
- 判定必须靠**已知名字集合**而不是正则猜消息：`globals` 的键就是那份集合（`read_file`/`grep`/`glob`/`write_file`/`edit_file`），从异常消息里取未定义标识符，若命中则提示 `use tools.<name>({...})`。命中不到就**不加任何提示**（宁可少提示也不误报，遵循 #85 的约束）。
- 若代价允许，同时覆盖来路相反的一条：`tools.run(...)` / `tools.console`（`TypeError: undefined is not an object` 或等价）—— `run`、`console`、`print` 是顶层而非 `tools.` 下。若实现不可靠，就只做第一条并在 issue 里说明。
- 不改错误类别措辞：`script_test.go:106-109` 的表格必须继续通过。
- 不改任何 prompt/schema 字节：`TestCodeModeFloorBudget`（3,500）、`TestScriptFloorBudget`、`TestFixedPromptFloorBudget` 必须不变。
- `globals` 新增工具时必须同步这份名字集合，否则提示会漏；在该映射旁加注释，或用 `TestScriptSpecMatchesGlobals` 同样的思路加一条一致性测试。

## 验收标准

- 单测：脚本写 `read_file({path:"x"})` 的错误消息包含 `tools.read_file`。
- 单测：`grep`、`glob`、`write_file`、`edit_file` 顶层误用同样给出对应提示。
- 单测：未定义变量 `fooBar()` 的错误消息**不含** `tools.` 提示（防误报）。
- 单测：`script threw: ReferenceError: read_file is not defined` 的类别前缀不变。
- 三个 floor 预算测试字节数不变。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 备注

本 issue 只改错误路径，不改成功路径、不改提示词，因此与 #85 的记录方式一致，可在 #98 修好后再复测 `glob` 相关失败率，以区分"路径错"与"名字空间错"两种失败来源。


## 修复记录（2026-11-25）

按 issue 的实现要点落在 `describeScriptError` 的 `Exception` 分支，与 `awaitHint`/`editFileHint` 同层，仅加一行拼接。判定靠**已知名字集合** `scriptHostToolNames`，从 `ReferenceError: <name> is not defined` 里取出标识符精确匹配，命中不到不加任何提示。

改动：
- `namespaceHint(err)`：只处理 `ReferenceError: X is not defined` 形态，`X` 命中白名单才追加提示。
- `scriptHostToolNames` + 导出函数 `ScriptHostToolNames()`（返回副本，供测试对齐）。

实际输出：

```
写 read_file({path: "hello.txt"})
修复前: script threw: ReferenceError: read_file is not defined
修复后: script threw: ReferenceError: read_file is not defined; the host tools live on the tools object, so call tools.read_file({...}) — a bare read_file(...) is undefined
```

未定义变量 `fooBar()` 的输出**保持原样**，未加提示（防误报）。

新增测试：
- `TestScriptNamespaceHintTeachesToolsForm`：5 个工具各自给出 `tools.<name>`，且 `script threw: ReferenceError: ` 前缀不变。
- `TestScriptNamespaceHintStaysSilent`：未知名字不含 `tools.` 提示。
- `TestScriptHostToolNamesMatchGlobals`：名字集合与 `Object.keys(tools)` 一致，防止新增工具时漏配。

范围说明：issue 里"可选的第二条"（`tools.run(...)` 反向误用）实测**不报错**（`tools.run([...])` 返回 `nil`），没有失败可提示，故按 issue 允许的范围只做第一条。

提示只出现在失败路径，不新增任何 prompt/schema 字节；`TestCodeModeFloorBudget`、`TestScriptFloorBudget`、`TestFixedPromptFloorBudget` 未变。

验证：`go test ./...` 通过，`qlty check` 无问题。
