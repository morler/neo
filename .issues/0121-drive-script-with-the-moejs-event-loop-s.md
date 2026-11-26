---
id: 121
title: Drive script with the moejs event loop so one call can run tool calls concurrently
status: wontfix
priority: high
labels:
    - feature
    - tools
    - performance
relations:
    related-to:
        - 66
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

## 背景

`docs/design/js-batch-tool.md:114-116` 明确写了「Not async」，理由是「async/await 需要事件循环、promise 结算和第二套取消模型，而脚本的价值是批量已知步骤，同步 host 函数已经够用」。这条决策的前提是「一次脚本调用内的工具调用只能串行」。

但真实瓶颈不在脚本语言，而在**往返次数**：一个脚本里 20 个彼此没有数据依赖的 `read_file`/`grep`，现在是 20 次串行 host 调用（`internal/tools/script.go:271` 的 `globals` 里每个都是同步 `moejs.NativeFunc`）。

`moejs v0.1.1` 已经自带 `eventloop` 包（`eventloop/eventloop.go`）：`New`(:317)、`Run`(:354)、`RunOnLoop`(:483)、`Hold`(:503)、`NewPromise`(:528)、`Post`(:1178)、`Interrupt`(:473)。这是把同步 host 函数改成「返回 Promise、Go 侧结算」所需的全部原语，**不需要修改 moejs**。行号已对 v0.1.1 源码逐个核对，全部准确。注意这套原语只覆盖「host 函数返回 promise」，不覆盖下面「顶层 `await`」与「拒绝怎么回到宿主」两点。

现状：

- `scriptWrapper`（`script.go:47-59`）用同步 `eval`，并在结果检测到 Promise 时主动报错（`script.go:50`）。
- `runInSession`（`:543`）与 `runCodeMode`（`:641`）都是 `rt.RunScript` + `context.AfterFunc` + `rt.Interrupt`。
- 超时 5 分钟、内存 64 MiB 写死在 `script.go:33-34`。

## 目标

在一次 `script` 调用内允许脚本并发等待多个 host 工具调用；工具边界、审批门禁与资源限制语义不变。

## 实现要点

- 执行路径从 `rt.RunScript` 改为 `eventloop.New(rt, opts)` + `Loop.Run(fn)`；`fn` 内求值一个 async 包装（`(async () => { ... })()`），用 `RunOnLoop`/`NewPromise` 把结果送回宿主。
- `globals()`（`script.go:271`）里的 host 工具改为返回 Promise：Go 侧 `go func()` 跑原同步逻辑，完成后 `loop.RunOnLoop` 结算；失败以 rejection 结算。审批门禁仍在这层之前，脚本拿不到绕过路径。
- 只允许从宿主 goroutine 通过 `Hold`/`Post`/`RunOnLoop` 进入循环；禁止跨 goroutine 直接传 runtime 值（会 `ErrForeign`）。
- 超时统一走 `Loop.Interrupt(cause)`，替换现有的 `context.AfterFunc(... rt.Interrupt ...)`；`Interrupt` 返回后才能释放或回池 runtime。
- 内存语义要写清：`Options.ResetMemoryPerTask` 默认 false，因此 `MemoryLimit` 覆盖整个 `Run` 期间的存活 + 分配，不再是单 job 预算。现有 64 MiB 需重新评估并给出结论。
- 顶层 `await`：**已实测证伪原表述**。classic script 的顶层 `await` 仍是语法错误；在 async 函数里直接 `eval` 含 `await` 的源码同样报 `SyntaxError: Unexpected identifier`（eval 的源码按 Script 解析）。唯一可行形态是把模型源码拼进 `CompileFunction(Async: true)` 的函数体，而 async 函数体没有「最后一个表达式即结果」的语义：不写 `return` 的结果是 `undefined`。因此「最后表达式是结果」这条对模型公开的契约需要另加完成值协议，不是白拿的。
- 取消传播：ctx 取消时，未完成的 host 调用也要收到取消，不能只让主循环退出。
- 拒绝的回传路径：实测 `Loop.Run` 对脚本内未捕获的 rejection 返回 `nil`，错误只能从返回的 promise 上读（`PromiseResult` 得到 rejected，原因要用 `rt.Get(reason, "message")` 取，`rt.ToGo` 对 `Error` 对象返回空 map）。把拒绝翻译成现有的 `script threw` 必须显式实现，不会自动发生。
- `ParallelSafe` 仍恒 false（`script.go:127`）：本任务只做**单次调用内**的并发，不改调用之间的并发分类。

## 验收标准

- 单测：一个脚本 `await Promise.all([...])` 发起 20 个 `read_file`，结果全部正确，并用阻塞桩证明总耗时接近单个而非 20 倍。
- 单测：并行 host 调用中一个失败时，rejection 正确变成 `script threw`，错误类别措辞不变（`TestScriptErrorClassesStayDistinct` 继续通过）。
- 单测：超时后脚本被中断，未完成的 host goroutine 在 ctx 取消后退出，无 goroutine 泄漏（goleak 或计数断言）。
- 单测：`-race` 下并行脚本 + 并行 host 调用不出现 `ErrForeign`。
- `TestScriptFloorBudget` / `TestFixedPromptFloorBudget` 的字节变化必须单独说明：允许 await 会改动 `Spec()` 与提示词，需在同一提交里更新预算并写明理由。
- `go test ./internal/tools ./cmd/neo` 通过。
- 先回答上游问题再开工：本任务要解决的是**单次调用内的墙钟延迟**，而 `docs/performance.md` 记录的实测瓶颈是**合并率**（同一处工作拆成几个请求），两者不是同一个指标。验收标准必须证明延迟本身是瓶颈，否则即使全部通过也不构成收益。

## 需要推翻的既有决策

`docs/design/js-batch-tool.md:114-116` 的「Not async」要在同一提交里改写：写清当时拒绝的原因（没有事件循环原语）与现在接受的原因（moejs 自带 eventloop，取消模型复用 ctx）。

## 实测核对（2026-11-26）

用 `pkg/mod` 里 moejs v0.1.1 的源码写探针程序验证，结论按影响排序：

1. **并发是真的。** 20 个 host 调用用 `NewPromise` + `go` 结算，`Promise.all` 后峰值并发 20、总耗时 51 ms（单个桩延迟 50 ms）。`Loop.Run` 会等所有未结算的 `NewPromise` 才返回。这一条成立。
2. **顶层 `await` 不会变合法。** classic script 顶层 `await` 仍是 `SyntaxError`；在 async 函数里 `eval` 含 `await` 的源码同样报 `SyntaxError: Unexpected identifier`（eval 按 Script 解析）。只能把源码拼进 `CompileFunction(Async: true)` 的函数体，而函数体里没有完成值：不写 `return` 得到 `undefined`。所以「最后一个表达式即结果」这条对模型公开的契约要重做，不是顺手就有。
3. **未捕获的 rejection 不会让 `Run` 报错。** 探针里脚本 reject 时 `Loop.Run` 返回 `nil`，只能在返回的 promise 上用 `PromiseResult` 看到 rejected；原因要用 `rt.Get(reason, "message")` 取，`rt.ToGo` 对 `Error` 对象返回空 map。把拒绝翻译成现有 `script threw` 必须显式实现。
4. **超时会让未结算的 promise 悬空。** `Loop.Interrupt` 之后 `Run` 返回，未结算的 host goroutine 仍持有 promise；下一个 `Run` 不会复活它（实测 `settle` 之后再也没跑）。这要求每个 host goroutine 必须自己响应 ctx 取消并停止，否则就是泄漏。
5. **内存语义确如 issue 所述，但没有实测到的现实影响。** `ResetMemoryPerTask` 默认 false，预算覆盖整个 `Run`；不过 200 个并发 `read_file` 只用了约 100 KiB，16 MiB 的保留型脚本才会触发。64 MiB 在并发场景下不是约束。

## Closed

wontfix。本任务买到的东西，Neo 已经在别处有了：**互相独立的工具调用本来就在 agent 层并发执行**（`MaxParallelTools` 默认 8，`read_file`/`grep`/`glob` 都是 `ParallelSafe`）。模型一次响应里发 20 个 `read_file`，现在就并发跑，不需要脚本。

而 `script` 存在的理由恰恰是相反的那一类：**后一步依赖前一步的结果**（`docs/design/js-batch-tool.md:9-28`）。这类调用天然串行，`Promise.all` 帮不上。

本地工具也确实慢不到值得为它重写执行路径：本机实测 `rg` 单次约 4 ms、20 次串行约 100 ms，读一个 30 KB 文件（含进程开销）约 1 ms；同一次 provider 往返是秒级。`docs/performance.md` 记录的瓶颈是合并率（0/3 与 3/8），不是单调用延迟。

代价则不小：三处执行路径（`script.go:566`、`:672`、`:223`）都要迁到 `loop.Run`，完成值契约要重做（上面的实测第 2 条），拒绝与超时回传要显式实现（第 3、4 条），`codeModeSpec` 里「Synchronous only; top-level await is an error」必须改，而 `TestScriptFloorBudget` 只剩 46 字节余量（9,954 / 10,000）。

mission 的取舍很明确：「Add extensibility only when it supports a demonstrated workflow」「does not preserve features whose complexity exceeds their demonstrated value」。这里没有可演示的工作流，只有可演示的微秒级延迟。

如果将来出现真凭据，最小形态不是本任务：只把 `run()` 做成可并发（几个独立的 `go test` 并行才是秒级收益），而这条改动不依赖 `Promise.all` 暴露给模型。

## Closed
实测：agent 层已并发（MaxParallelTools=8，read_file/grep/glob 均 ParallelSafe），script 的价值在串行依赖步骤，Promise.all 无收益；且顶层 await 不变合法、未捕获 rejection 不使 Run 返回错误、完成值契约需重做。详见正文 Closed。
