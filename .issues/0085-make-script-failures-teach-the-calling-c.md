---
id: 85
title: Make script failures teach the calling convention instead of only naming the error class
status: closed
priority: high
labels:
    - feature
relations:
    depends-on:
        - 83
    related-to:
        - 99
        - 100
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`docs/performance.md:104` 记录了一次次真实的失败代价：

> A run with an explicit instruction to use it did, but spent three failed calls discovering the calling convention.

现在 `describeScriptError`（`internal/tools/script.go:309-341`）只报**错误类别**：`script has a syntax error: …`、`script threw: …`。类别本身没错，但模型拿到它之后仍然不知道怎么改，而 #80 的实测显示模型失败一次后就再也没回来用这个工具（任务 2、3 全程 0/3）。

更具体地，三处最容易踩的坑，目前都没有在错误里给出修法：

1. `top-level await is an error`（`Spec()` 的措辞）——模型不知道同步写法长什么样。`script.go:42-48` 已经在结果里拒绝 Promise，但那是成功路径的事后检查，不是修法提示。
2. `read_file` 会加 `%6d\t` 行号前缀（`internal/tools/fs.go:135`），而 `edit_file` 要求 `old_string` 是**原始文本**（`fs.go:349`）。在脚本里 `read_file` 结果直接喂 `edit_file` 是最自然的写法，会失败。
3. `grep` 无匹配返回字符串 `"no matches"` 而不是空串（`internal/tools/search.go:172-174`），`.split("\n")` 会得到一条幻觉匹配。提示段已经警告过这一点（`TestScriptExampleAvoidsAnEmptyStringTrap`），但只在系统提示里说一次。

## 目标

让脚本的失败结果自身携带可复制的修法，成本只在该次失败时支付——稳态每轮字节不变。

## 实现要点

- 在 `describeScriptError` 按错误类别追加一句修法，形如：
  - 语法错误且源码含 `await` → 指出脚本是同步的，给一个同步写法（不要只说 “not supported”）。
  - `script threw` 且在 `read_file` 之后调用了 `edit_file` → 指出 `read_file` 输出带行号前缀，需要剥离后再匹配。
- 只加**一行**提示，不复述整个白名单，不写教程。
- 判断错误类别与上下文时，不要做脆弱的字符串启发式到会误伤正常脚本的程度；宁可少提示也不要误报。若无法可靠判断，只改 `await` 这一条（它有确定的语法特征）。
- 不改变错误本身的类别措辞，`script_test.go` 里现有的错误归类测试必须继续通过。
- 不改 prompt / schema 字节，`TestScriptFloorBudget` 与 `TestFixedPromptFloorBudget` 必须不变。

## 验收标准

- 单测：`await 1` 的错误消息包含同步写法的具体示例，而不只是 “not supported”。
- 单测：在脚本内 `read_file` 后直接 `edit_file` 的失败结果，消息指出行号前缀问题（若选做该条）。
- 单测：正常的 `script threw` 仍报告为 `script threw`，类别不变（用现成的 `script_test.go` 表格）。
- `TestScriptFloorBudget`、`TestFixedPromptFloorBudget` 字节数不变。
- `go test ./internal/tools ./cmd/neo` 通过。

## 实现结果（2026-11-25）

改动在 `internal/tools/script.go`：`describeScriptError(err, source)` 在错误类别之后追加一句修法，**只在源码/错误文本确认原因时**追加。

- `awaitHint(source)`：语法错误且 source 含 `await` 时，给出同步写法与一个 `tools.grep` 的具体例子。依据：引擎把 `await 1` 报成 `SyntaxError: Unexpected number`，消息本身完全看不出“脚本是同步的”。
- `editFileHint(err)`：`script threw` 且错误含 `old_string not found` 时，指出 `read_file` 的行号前缀需要剥离。
- 选择的标准是两条**确定**信号（源码含 `await`；错误串含 `old_string not found`），不是对任意错误做启发式。普通语法错误与无关的 `throw` 保持原样——错位的提示会把模型引向不存在的 bug，比不提示更贵。

验证：

- 新增 `TestScriptErrorsCarryAFixableNextStep`：`await` 提示含同步改写与具体例子；`1 +` 不含 await 提示；无关 `throw` 不含 `read_file` 提示。
- 新增 `TestScriptEditHintNamesTheLineNumberGutter`：真实脚本里 `read_file` → `edit_file` 失败，消息含 “strip the leading line number”。
- `TestScriptErrorClassesStayDistinct` 未改动且仍通过，错误类别措辞不变。
- `TestFixedPromptFloorBudget` 8,084 与 `TestScriptFloorBudget` 9,555 字节数不变 —— 提示只出现在失败结果里。
- `go test ./internal/tools ./cmd/neo` 通过。

文档：`docs/developer/tools.md` 新增「Script error hints」一节，记录两类提示、窄范围的理由，以及“字节只在失败路径上支付”这一点。

**未做**：没有对“脚本内 grep 后 split 得到幻觉匹配”这一条加提示。提示段已有 `grep` 返回 `"no matches"` 的警告（`TestScriptExampleAvoidsAnEmptyStringTrap`），而脚本运行期无法从结果中可靠区分“真的没有匹配”与“模式写错”，加提示会误报。
