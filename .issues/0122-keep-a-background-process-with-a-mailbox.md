---
id: 122
title: Keep a background process with a mailbox alive across script calls
status: wontfix
priority: medium
labels:
    - feature
    - tools
relations:
    related-to:
        - 58
        - 68
        - 69
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

## 背景

`ScriptSession`（`script.go:115`）让 code mode 在多次调用之间保留一个 runtime，但它是**串行**的（`sync.Mutex`），且没有「进程」概念：没有 pid、没有邮箱、没有监督。一个脚本不能把工作交给下一次调用继续，也不能被单独杀掉。

`docs/design/js-batch-tool.md:105-113` 把这点写成设计边界：「Not stateful across calls, except in code mode」。这条边界对「一次性批处理」成立，但对「等待型任务」不成立：盯 CI、等文件变化、轮询长任务，本质都是「挂起 → 消息唤醒 → 再跑一段」。今天这些场景只能退化成 `bash` + 后台进程 + 模型自己写轮询循环，每次往返都付 token。

`moejs` 的 `eventloop` 提供了挂起原语：`Hold`(:503) 让循环在没有可跑任务时阻塞等待，`RunOnLoop`(:483)/`Post`(:1178) 从任意 goroutine 投递任务唤醒，`NewPromise`(:528) 把一次唤醒变成已结算的 Promise。这正是 otter（`/var/home/msc802/code/tools/otter`）用 `parked`/`sleeping` 两张等待表手工实现的东西；otter 的 docs/README.md:27-28 与 src/scheduler.rs 记着这两张表。行号已对 moejs v0.1.1 源码核对。

## 目标

让脚本可以启动一个**跨调用存活**的后台进程：有 pid、有邮箱，挂起时不占 CPU，消息到达时被唤醒，可被显式 kill，并随会话结束回收。

## 实现要点

- 新增 `ScriptProcess`：pid + `*eventloop.Loop` + goroutine + 有界 inbox（`chan string`）。生命周期挂在 `ScriptSession` 上，而不是单次 `Script` 调用。
- JS API 收敛到最小集，命名与现有工具一致：`spawn(code) -> pid`、`send(pid, value)`、`await recv(timeoutMs?)`、`kill(pid)`、`list()`。**不要**搬 otter 的 `setName`/`restrictSandbox`：moejs 是封闭世界，能力收窄没有增量。
- 消息统一走字符串（JSON 文本），跨进程不传 runtime 值（`ErrForeign`）。
- `recv` 用 `Hold` + `NewPromise`：邮箱为空时循环阻塞，投递方 `Post` 唤醒。超时用循环自己的 timer，不要用 Go `time.After` 包住循环（那会把挂起变成占线程）。
- 上限必须显式：单会话进程数、单进程 inbox 容量、单条消息大小、空闲回收时间。缺任一项就是无界资源。
- 回收：会话结束、`/clear`、进程退出时 `Loop.Terminate()` 并等待 goroutine 退出，测试用计数证明无泄漏。
- 输出归属：后台进程的 `console.log` 走父脚本的子调用事件 sink（#68/#69），不直接写终端，否则 TUI 布局错位。
- **不做持久化**：与 `ScriptSession` 一致，重启即空，提示词里明说。跨进程重启的持久化需要磁盘状态与恢复语义，属于另一个任务。

## 验收标准

- 单测：脚本 spawn 一个 watcher，本次调用返回；下一次 `script` 调用 `send` 消息，watcher 被唤醒并产出结果。两次调用之间不占 CPU（`OnIdle`/`OnWake` 计数已实测可用作证据，见下方核对）。
- 单测：`recv` 超时返回可读错误，不挂死；并明确写清超时后消息是否留在邮箱（沿用 otter 语义则保留，并写进文档）。
- 单测：`kill` 后被 `Terminate` 回收；重复 kill 幂等。
- 单测：进程数 / 邮箱容量 / 消息大小上限各自触发时可读失败，不 panic。
- 单测：会话结束（`/clear` 或进程退出）后无残留 goroutine。
- `-race` 通过。
- `docs/developer/tools.md` 与 `docs/design/js-batch-tool.md` 更新状态边界：从「无跨调用状态」改为「code mode 有单会话后台进程，不持久化」。

## 关系

**依赖关系已实测证伪。** 原假设是「没有 #121，`Hold`/`Post` 无处挂载」，但 `eventloop.Loop.Start()`（`:380`）本来就是给这个场景准备的：循环跑在自己的 goroutine 上，空闲时阻塞，宿主通过 `RunOnLoop` 进去执行代码。探针证实了完整链路：`Start()` → 脚本里 `await recv()` 挂起 → `OnIdle` 触发（循环确实睡着，不是自旋）→ 塞消息 → `OnWake` 触发 → 结果可见。全程不需要 `Promise.all`，也不需要 #121 的任何改动。

因此本任务与 #121 解耦，不再被它阻塞。唯一真实的共享点是「脚本能 `await`」，而这一点用 `CompileFunction(Async: true)` 就能得到，与 #121 无关。

## 实测核对（2026-11-26）

用 moejs v0.1.1 源码写探针验证：

- `Loop.Start()` 启动后台循环后，脚本里 `await recv()` 能挂起，`OnIdle` 计到 1（循环进入阻塞，没有自旋）；从另一个 goroutine 投递消息后 `OnWake` 计到 2，投递的值被循环读到。跨调用存活的机制成立。
- `Hold()` 只在 loop goroutine 上取用时才维持 `Run` 存活；在 loop 外取用会让 `Run` 一直阻塞（实测 300 ms 未返回）。实现时不能在宿主 goroutine 上随手 `Hold`。
- `Terminate()` 会丢弃排队任务并留下待处理的中断，必须 `ClearInterrupt` 后才能复用 runtime；同一个 runtime 可以再建新 Loop。
- 未结算的 promise 在 `Terminate` 后不会复活，其 host goroutine 必须自己响应取消，否则泄漏。

## Closed

wontfix。**与 #121 无关的独立否决**，不是「等 #121」。

理由不是「做不出来」，是它违反了 Neo 的形态：

1. **它就是后台进程。** mission 的非目标写着「Neo does not hide autonomous background work behind an opaque interface」，而 `ScriptProcess` 恰恰是一个模型看不见、只能靠脚本轮询的自主执行体。要让它可见，就得接入 TUI（pid 列表、消息、kill 键、布局）；那是第二个进程管理器，不是脚本功能。
2. **任务自己在扩大范围。** 验收要求「空闲回收时间」「会话结束、`/clear`、进程退出时回收」「输出归属走子调用 sink」。这是一个调度器加一套生命周期管理，`MISSION.md` 的取舍是「Prefer deleting accidental complexity to adding another abstraction」。
3. **想解决的问题 Neo 已有更简单的做法。** 盯 CI、等文件变化、轮询长任务，今天就是 `bash` 起后台进程再轮询，或者干脆一次 `run(["gh","pr","checks"])` 拿到结果。多一层 JS 进程模型换来的不是新能力，是同一件事的另一种写法。
4. **`ScriptSession` 的串行 mutex 会与它冲突。** #122 的进程要跨调用存活，而现有 session 是「一次调用独占 runtime」；两者共存需要把锁改成生命周期管理，这正是第 2 点说的复杂度。

真正有价值的子集是它的另一半：一次 `script` 调用里并发跑几个独立的 `run(["go","test",...])`，把两分钟的命令从串行变并行。那有秒级收益，且不需要跨调用状态、pid 或邮箱。若将来要做，按这个范围另开任务。

## Closed
违反 MISSION 非目标（隐藏自主后台工作）、扩大为第二个进程管理器与现有串行 session 冲突，且已有 bash/run 的简单替代。与 #121 无依赖关系，已解除。详见正文 Closed。
