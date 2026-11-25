---
id: 90
title: Show the model when a turn's calls chained on earlier output, as a turn-end note not a prompt section
status: wontfix
priority: low
labels:
    - llm
relations:
    depends-on:
        - 83
    related-to:
        - 86
        - 91
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`/codemode status` 现在会报告本会话 `script` 调用次数（#81），但那是给人看的：模型自己不知道“这一轮里有三次调用的参数依赖上一次结果”。在工作流里，模型没有任何反馈回路提示它本可以合并。

唯一在会话内可以合法追加信息的位置是**轮末**，因为会话中途改缓存前缀会破坏 `docs/design/token-budget.md` 规则 1。`internal/agent/agent.go:987` 的 `repeatReminder` 已经示范了这种做法：追加到 conversation 末尾，不动系统提示、工具列表和历史。

## 目标

在轮末追加一句可观察的提示，让模型在下一轮能调整策略。

## 实现要点

- **优先在 CLI 边界观察**，不要在 `internal/agent` 里加策略判断：`MISSION.md` 明确“`internal/agent` 保持 policy-free”，`token-budget.md` 规则 4 同义。可行路径是在 `cmd/neo` 的事件消费侧统计“同一轮内存在 tool_result 文本被后续 tool call 的输入显著引用”这类信号，再通过已有的轮末追加通道注入。
- 若上述判断无法做可靠且低误报，**放弃本任务**，不要用启发式制造噪声。误报比沉默更糟：它会让模型在无用任务上也开始写脚本。
- 判据示例（择一，实现前先定）：同一轮内 ≥2 次 `grep`/`read_file` 的结果各自被后续调用的参数使用；或同一文件被读两次。
- 文案短，一句话，不写教程，不复述白名单。
- 不改 prompt、schema 字节；`TestFixedPromptFloorBudget` 与 `TestScriptFloorBudget` 字节数不变。

## 验收标准

- 单测：构造一个“读后按结果再读”的回合，断言轮末追加了什么；构造一个无依赖的并行读回合，断言**不追加**。
- 单测：追加发生在 conversation 末尾，系统提示与工具列表逐字节不变（复用现有缓存前缀测试的渲染路径）。
- 本任务合入前需先有 #83 的基线，否则无法判断这条提示是否让模型在无收益任务上也开始写脚本。
- 若判定不可靠，用 `issues close --wontfix` 并写明理由，不留半成品。

## 判定：不可靠，按本任务的写入条件放弃（2026-11-25）

本任务事先写下的退出条件是"若上述判断无法做可靠且低误报，放弃本任务"。下面是用 `~/.neo/sessions/` 里 78 个真实会话（32 个有消息，5,872 条消息，102 个含工具调用的轮次）取得的证据，判定为**放弃**。

### 判据 1：tool_result 文本被后续调用参数引用（本任务的首选方案）

- 宽松 token 重叠（≥6 字符的路径/标识符片段）：**101 个多调用轮次中 96 个命中（95%）**，命中事件 2,403 次。
- 抽样核对命中源：`internal`、`origin`、`timeout`、`go.mod`、`CHANGELOG.md`、`README.md`、`testing.T`——都是模型本来就掌握的项目词汇或常见文件名，不是从这次结果里抄来的值。仅 7% 的命中能归因到"常见文件名"，说明剩余噪声不是少数特例。
- 收紧为"带内部结构的长标识符"仍为 **86/101**。误报不降，因为结果文本与后续命令共享项目词汇是常态，而共享词汇不构成因果。

### 判据 2：读一轮、写一轮（形态信号）

- 纯读响应之后紧跟含写操作的响应：**61/99** 多轮对话。
- 表面上支持本任务，人工核对 3 例后全部存疑：
  - `grep -n` 被判定脚本错标成写操作（它只读）；
  - `git status` → `git commit` 确实是依赖，但脚本帮不上：提交要的是决定，不是数据变换；
  - 读测试文件 → 写新文件，写入内容是新代码，**不依赖**读到的文本。
- 这条判据测的是"轮次边界"，而 #90 要判断的是"参数依赖"，两者不是同一件事。

### 判据 3：写操作的载荷是否包含读结果中的标识符

- **520/523** 命中，明显失真：标识符匹配把 `package`、`import`、`return`、`Fatalf`、`testing` 这类语言与库词汇一并算入。
- 没有任何纯静态的文本层判据能把"抄了结果里的值"和"恰好用了同一个常见词"分开。

### 结论

依赖关系是**语义**的，而轮末只能看到文本。要判断"这次 grep 的输出决定了那次 edit 的 old_string"，需要理解意图，而这恰是 #86 里连模型自身都没做到的事（2/6 选择脚本、合并数 0）。

代价不对称：若 95% 的轮次都收到"本可合并"的提示，模型会学会忽略它——这比沉默更差，因为它既消耗 token，又污染后续所有判断。本任务不实现，也不留半成品。

**对 #91 的影响**：轮末提示这条路已排除。激进模式的评估不得再指望用轮末反馈提升合并率，判据只能建立在真实任务的成功率与请求数上。

## Closed
The only two signals available to a turn-end note are unreliable. On 78 real sessions (102 tool turns) tool_result text reuse fires in 96 of 101 multi-call turns because results and later commands share project vocabulary, and a read-then-write shape fires in 61 of 99 while sampling found the common cases either misclassified (grep -n counted as a write), unhelpful to script (git status then git commit), or genuinely independent (read a test, write a new file). Payload-vs-result identifier matching hit 520 of 523 because language and library words dominate. A dependent step is semantic and only text is visible at turn end, so the note would fire on nearly every turn and be learned as noise. Numbers and sampling are recorded in the issue.
