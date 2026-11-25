---
id: 91
title: Evaluate a radical codemode mode that replaces the tool surface with one eval tool
status: closed
priority: medium
labels:
    - performance
    - llm
relations:
    depends-on:
        - 87
        - 89
    related-to:
        - 90
        - 83
        - 86
        - 92
        - 93
        - 94
        - 95
        - 96
        - 97
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

用户提出的判断：`script`（codemode）至今体现不出效率价值，是否应当干脆把它做成 `../smol/` 那样的激进模式——模型只看到一个工具，一切动作都靠写代码完成——再从这个形态上谈优化。

这个判断指对了机制的一半。`docs/performance.md` 的三组测量给出的结论一致：

- 初测（三任务）：请求数不降，input +6.7% / +8.7% / +10.6%，三次都是模型选 `bash`+`grep`，从未调用 `script`。
- 修正文案后重测：计数任务 3/3 会选，但 input +13.7%；另两个任务 0/3。
- #83（专为脚本设计的“读→判断→写”任务）：`read_file` 调用 **0 次**，模型用 `sed -n '1499,1503p'` 把窗口收窄了。脚本竞争的不是“读整个文件”，而是一条已经很窄的 shell 命令。

`script` 无效的成因有两个，激进化只解决第一个：

1. **存在更便宜的替代路径**：`bash` 是每次都能选中的捷径，模型没有理由包一层脚本。→ 单工具化能强制消除。
2. **即使强制写了代码，当前 `script` 也合并不了动作**：#86 把判据换成布尔信号后测得 2/6 次调用脚本、**读-判断-写合并数 0**（#83 中同样为 0，其中一次只是把 `grep` 换成了脚本，编辑仍在顶层）。→ 单工具化**解决不了**，只会让主路径变贵。

因此本任务不是“把 codemode 打开得更彻底”，而是检验第 2 个成因是否可解，以及第 1 个成因带来的收益是否覆盖代价。**在 #87、#89 结论出来之前不启动**：两者针对的是同一个已被测量到的障碍（决策点无提示、基础提示词与脚本串行互相抵消），成本极小且可归因，先在它们上面花掉的力气必须先用完。

## 目标

用一次**完整的工具面替换**（不是叠加）验证单工具 codemode 模式是否值得保留，并给出预注册判据下的明确判定：保留 / 回退 / 不改工具面而是补能力。

## 现状事实（本次实测，供任务内复算）

- 默认 chat 工具 schema 合计 5,022 B；其中 `bash`+`read_file`+`write_file`+`edit_file`+`grep`+`glob` = 2,893 B，`script` = 851 B。
- 默认固定地板 8,084 B（tools 5,022 + prompt 3,062，截至 2026-11-25 未变）；`script` 开启后 9,796 B（tools 5,971 + prompt 3,825）。#87 给 `bash` 描述加了 98 B 提示、#89 给批处理规则加了 143 B 限定，两者只在 `script` 注册时出现。
- `scriptCapability` 实测 620 B，`script` schema 851 B，合计 1,471 B。**已修正（提交 `00e573a`）**：此前 `cmd/neo/performance_test.go`、`docs/performance.md`、`docs/design/token-budget.md`、`docs/developer/config.md`、`docs/developer/tools.md`、CLI 帮助与 `CHANGELOG.md` 一致写着 682 B / 1,533 B，均按实测改为 620 B / 1,471 B；同一句里"重写涨 78 B"也纠正为 56 B（564 → 620）。地板总数原本就是对的，682 疑似用 3,682 − 3,000 算出而非实测。本任务开始前**无需**再处理此项。
- 由此推算：把六个文件/命令工具换成单个 `script`，工具面约 2,980 B、prompt 3,825 B，合计约 6,805 B，即**约 −1.3 KB（≈ −320 tokens）每请求**。这是推算值，不是实测值，且前提是 `script` 的 schema 在加上 `run()`/持久语义后不增长——实际会增长，所以真实收益小于此数。

该量级（约 4% 的固定地板）本身不足以证明一次工具面重构，这是本任务必须用真实任务而非字节数来判定的原因。

## 实现要点

- **启动时选定，不做运行中开关。** 现在的 `/codemode on|off` 会在会话中途替换工具集，而 `docs/design/token-budget.md` 规则 1 要求工具列表与基础系统块在会话内不变，每次切换都会让缓存前缀失效。模式走 `--codemode=solo` 与 `features.codemode: solo`，在 `registryFor` 这一个组合处决定，符合它现有“single decision”的定位。
- **完整替换，不是叠加。** 模式内只注册 `eval`（沿用 `script` 这个名字或改名均可，但只能有一个），`agent`/`goal`/`workflow` 是否保留由本任务实测决定并在 issue 中记录理由。保留 `bash` 就等于没做这个实验。
- **补齐 smol 真正的效率来源**，否则只承担成本：
  - `run()` 执行程序。这是单工具模式能工作的前提：没有它，模式内无法跑 git、测试、构建。**注意** `docs/design/script-vs-bash.md` §2 已论证“宿主注入 exec 等于没有分类器”，但本模式的前提恰恰是承认这一点并退到“环境负责隔离”，与 `docs/developer/guides/permissions.md` 的现有立场一致；不要在实现里假装它仍可拦截。
  - 文件访问改为 moejs `node` 包的根约束（`FSOptions.Root`/`Mount`/`Write`/`Allow`），或继续走现有白名单工具。**二选一，写清选哪个及理由**，不要两套并存。
  - 会话级持久 `Runtime`。`Runtime` 已提供 `ResetAllocation`/`ClearInterrupt`，显然是按复用设计的；但持久化与会话存档的交互要明确：`internal/session` 只存 `[]llm.Message`，解释器变量无法进存档，所以“持久”只能限定在单个进程生命周期内，`/clear`、resume、重启后变量消失，且必须让模型知道。
- **审批语义要写清。** `script_approval_test.go` 证明脚本内的 `write_file` 仍走真实 matcher；单工具模式下唯一失守的是进程执行，它从“可按前缀拦”退到“完全不可拦”。这是模式级取舍，要在 `docs/developer/guides/permissions.md` 与 `docs/design/script-vs-bash.md` §5 Option B 里如实记录，不能只在代码注释里。
- **不要**在本任务里删除 `bash` 工具本身的代码；删的是模式内的注册。回退成本必须是“改一个组合点”，不是“还原一次重构”。

## 判据（预注册，运行前写死，不得事后调整）

以 #86 的教训为准：n=3 分辨不出 token 与 requests 的中位数差异（`off` 臂标准差 2,203、`on` 臂 4,517），所以 token 数据只记录、不作判据。

**主判据（布尔，唯一决定项）：** 在一次真实工程任务中，同一 `eval` 调用内是否出现“读 + 判断 + 写”的合并，且该任务随后被验证通过（测试或等价检查）。判法：日志中该次 `eval` 的 `source` 同时含至少一个读取类动作与一个写入类动作，且该轮结束时用户可见的验证确实执行。

- 出现合并并验证通过 → **保留**，并把该形态作为后续优化的基线。
- 出现合并但任务失败率明显高于默认模式 → **不改工具面而是补能力**，记下失败原因（错误可教性、调用约定、持久性缺失中的哪一个）。
- 未出现合并（与 #83/#86 一致）→ **回退**，结论写“单工具化不是障碍所在”。#87、#89 的提示词侧改动已在 2026-11-25 落地（提交 `0972bff`、`dcf976c`），#90 的轮末提示已按实测判为不可行并 `wontfix`（见 #90 的判定记录），因此回退后不要再指望提示词或轮末反馈能改变合并率，余下的选项只有真实任务上的成功率与请求数对比。

**次判据（记录用）：** `requests`、`usage.input_tokens`、固定地板字节数、`eval` 调用次数。每个模式至少 5 次运行，报中位数与跨度。

任务集必须包含默认模式能用 `bash` 一条命令解决、且单工具模式必须写代码才能解决的形态（跑测试、看 git 状态、构建失败后定位）。这是唯一能体现“去掉 bash”代价的任务类型，而 #83 的 fixture 恰好缺这一项。

## 拆除与回退范围

- 回退时删除：`--codemode=solo` 与 `features.codemode` 的解析、`registryFor` 里的模式分支、`run()` 宿主函数、持久 `Runtime` 的生命周期管理。
- 保留：`script` 工具本身、`/codemode on|off`（默认模式的既有行为）、审批通配逻辑。
- 若实验证明 `run()` 有价值但单工具化无价值，单独开 issue 讨论“默认模式内给 `script` 加 `run()`”，不要在回退里顺手留下半成品。

## 验收标准

- 模式在启动时选定，`/codemode` 不做运行中切换；默认模式的固定地板与 `TestFixedPromptFloorBudget`（8,084 / 8,500）**逐字节不变**。
- 新增模式有独立预算常量与测试，写清实测值，不挤占默认模式的余量。
- 判据表含：任务、模式、`requests`、input、`eval` 调用次数、是否合并、是否验证通过、结论；数据来自 NEO_LOG 与 `neo run --json`，不靠主观判断。
- 结论只有“保留”“补能力”“回退”三种之一，写进 `docs/performance.md` 与 `docs/design/js-batch-tool.md` §4（“Not stateful across calls”一条会因此失效，必须同步修订），不出现模糊措辞。
- `docs/developer/config.md`、`docs/developer/tools.md`、`CHANGELOG.md` 与新行为一致。
- `go test ./...` 通过；`qlty check` 退出码为 0 后再提交。

## 不在范围内

- 不引入 Python 或第二个解释器；语言保持 moejs。
- 不做 `agent()` 式的脚本内子代理编排（那是 `agent` 工具与新协议的事）。
- 不改 `internal/agent` 的策略中立性；模式的差异只体现在注册表与提示词，由 CLI 边界组合。
- 不在本任务里决定 `/codemode` 是否改名。

## 复测结果（2026-11-25，提交 `b3d8c9b`）

#87（bash 描述指向 script）与 #89（批处理规则限定）落地后的第一次测量。任务、cwd、fixture 与 provider/model 逐字沿用 #83，只改二进制版本。

**环境**：`/tmp/remeasure/ws4`；`big.txt` 2001 行，第 1501 行 `prefix needle-7f3a PLACEHOLDER suffix`；`commandcode` / `deepseek/deepseek-v4.1-flash`；同一二进制 `/tmp/remeasure/neo`（`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`，`remeasure-b3d8c9b`），只改 `neo.yaml` 的 `features.script`。

**任务原文**（与 #83 逐字相同）：

```
Read the line containing the unique needle marker in big.txt, change only the word immediately after it to UPDATED, and write the file back. Do not touch any other line.
```

### 原始数据

| 版本 | 运行 | requests | input_tokens | script 调用 | read_file 调用 | 第 1501 行 | 其余行未变 |
| --- | --- | ---: | ---: | ---: | ---: | --- | --- |
| off | r1 | 5 | 12,135 | 0 | 0 | UPDATED | 是 |
| off | r2 | 5 | 12,342 | 0 | 0 | UPDATED | 是 |
| off | r3 | 5 | 11,481 | 0 | 0 | UPDATED | 是 |
| on | r1 | 8 | 24,186 | 0 | 2 | UPDATED | 是 |
| on | r2 | 5 | 13,732 | 0 | 0 | UPDATED | 是 |
| on | r3 | 4 | 11,110 | 0 | 0 | UPDATED | 是 |
| on | r4 | 5 | 14,789 | 0 | 0 | UPDATED | 是 |
| on | r5 | 6 | 18,689 | **2** | 0 | UPDATED | 是 |
| on | r6 | 6 | 17,267 | 0 | 0 | UPDATED | 是 |

| 指标 | off（n=3） | on（n=6） |
| --- | ---: | ---: |
| requests 中位数（跨度） | 5（5–5） | 5.5（4–8） |
| input 中位数（跨度） | 12,135（11,481–12,342） | 16,028（11,110–24,186） |
| 运行中调用过 `script` | 0 / 3 | 1 / 6 |
| 运行中调用过 `read_file` | 0 / 3 | 1 / 6 |

### 主判据判定：**合并数 0**

`on` r5 是唯一调用脚本的运行。其 `source` 全文：

```js
const path = "/tmp/remeasure/ws4/big.txt";
const raw = tools.read_file({path}).split("\n");
// read_file adds line-number prefixes; read via bash-cat style instead to avoid prefix contamination
```

`has_read: True`、`has_write: False`。脚本只读了文件，判断与写入都留在顶层——随后它用 `sed -i '1501s/^\(prefix needle-7f3a \)PLACEHOLDER\( suffix\)$/\1UPDATED\2/'` 完成改写。

按预注册判据（同一 `eval` 内同时出现读取类与写入类动作），这是**未出现合并**，与 #83 的 2/6 调用、合并 0，以及 #86 的合并 0 完全一致。

### 结论：提示词侧已改到位，仍不足以让脚本承担一次往返

三条观察：

1. **#87 的作用可见但很窄。** `on` 组首次出现顶层 `read_file`（r1，`offset: 1499, limit: 4`），#83 的九个运行里一次都没有。这是 bash 描述里那句提示唯一能指认的效果，但样本只有 1/6，且该次运行请求数反而最高（8 次 / 24,186）。不能据此说提示有效。

2. **#89 没有改变行为。** 两次实测（#83 与本任务）的脚本都只读不写。批处理规则的限定语让"独立调用可并行、依赖调用用一次脚本"在文本上不再矛盾，但模型仍然把脚本当成一个更贵的 `grep`/`read_file`，判断与写入照旧放在顶层。这说明障碍不在两条指令的措辞冲突，而在模型对"一次脚本能同时改文件"的不信任或没想到。

3. **`on` 组成本仍高于 off。** input 中位数 16,028 对 12,135（+32%），请求数中位数 5.5 对 5。差值约等于固定成本（1,712 B ≈ 428 tokens/请求 × 5.5 请求 ≈ 2,354）乘以请求数后的量级，没有返回。

### 对 #91 的影响

#91 的两条前置（#87、#89）已完成，第三条可选路径（#90 轮末提示）已 `wontfix`。按 #91 预先写下的分支，本次属于"未出现合并"，因此：

- **不要**再通过继续加大提示词篇幅来提高合并率——两轮措辞改动（#84、#89）后合并数仍是 0。
- 单工具模式的假设前提（"模型不用脚本是因为 bash 更便宜"）**部分成立**：去掉 bash 确实能强制写代码，但本次数据显示即使强制，模型也未必把读-判断-写合并进一次调用。这正是 #91 需要实测的核心问题，而它现在有了一个明确的对照基线：**合并率 0/6**。
- 若做 #91，主判据应能区分"被迫写代码"与"真的合并"，否则一次成功的单工具实验可能只证明了前者。

## 实验实现与结果（2026-11-25）

### 实现了什么

| 部分 | 位置 |
| --- | --- |
| `run()` 宿主函数，返回 `{stdout, stderr, code}`，非零退出码是数据不是异常 | `internal/tools/script_run.go` |
| 会话级持久运行时（`ScriptSession`，互斥保护，`/clear`/重启失效） | `internal/tools/script.go` |
| `features.codemode: solo` 与 `--codemode=solo`，启动时选定 | `internal/config/config.go`、`cmd/neo/main.go` |
| solo 组合：注册表只有 `script`，带 `run()` | `cmd/neo/main.go` `registryFor` |
| solo 提示词：删掉批处理规则（单工具下无意义），新增 `soloCapability` | `cmd/neo/main.go` |
| `grep`/`glob` 的 `Absolute` 开关，让脚本内无 `cd` 也能把路径喂给 `read_file` | `internal/tools/search.go` |

默认模式**零回归**：固定地板仍是 8,084，script 地板仍是 9,796。solo 地板 **2,970 B**（856 schema + 2,114 prompt），独立预算 3,500。

### 实现中修掉的四个可用性缺陷（都有实测证据）

1. **持久运行时下 `const` 重复声明直接语法报错。** 第一次测量 16 requests / 30 calls / **12 errors**，模型自述"工具泄漏声明"。改为源码在 eval 内执行（每次调用独立作用域），跨调用状态走 `globalThis`，并在提示词与 schema 里如实写明。修后同任务降到 5–7 requests / 11–15 calls。
2. **`run([...], 10)` 被拒。** 只接受 `float64`，整数超时在 3 次调用里各失败一次。改为接受整数/小数/数字字符串，沿用 `Bash.timeoutFor` 的既有约定。
3. **`tools.glob("*")` 报 "missing required input: pattern"。** 字符串参数被静默转成空 map，错误指向了错误的地方。改为明确报 `glob takes an object of named arguments, not string; write glob({...})`。测量显示模型看到该错误后**下一次就改对**。
4. **`console`/`print` 不存在。** 引擎没有 console，模型写 `console.log` 直接 ReferenceError，输出丢失。补上并并入工具结果；失败时保留已打印内容。

### 测量

**环境**：`/tmp/remeasure/solo`，一个 Go 模块（`Add(a,b)` 正确，测试断言 `Add(2,2)==5` 错），`commandcode` / `deepseek/deepseek-v4.1-flash`，同一二进制，只改 `neo.yaml` 的 `features.codemode`。每臂 5 次，每次前恢复损坏的测试文件。

**任务**（两臂逐字相同）：`The test suite fails. Find the bug, fix it, and prove the suite passes.`

| 臂 | requests 中位（跨度） | input 中位（跨度） | 工具调用 | 工具错误 | 测试最终通过 | 同一次调用内读+写 |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| 默认（n=5） | 7（6–10） | 21,114（16,723–34,842） | 8–13 | 0–1 | **5 / 5** | 不适用（多工具） |
| solo（n=5） | 7（5–9） | 16,917（14,059–27,107） | 11–15 | 2–4 | **5 / 5** | **2 / 5** |

solo 的 input 中位数低约 20%，请求数相同，但工具调用更多、错误更多。

### 主判据判定：**补能力**（不是"保留"，也不是"回退"）

按预注册判据：同一次 `eval` 内出现读+判断+写，且任务随后验证通过。solo 臂 5 次里有 2 次满足（r4 用 `git log` 判断后在同一次调用里 `edit_file`；r5 用 `git checkout` 复位后在同一次调用里重写文件），且 5/5 任务都通过测试。

**但没有满足"保留"所需的说服力**，理由三条，每条都有数据：

1. **合并率 2/5 与默认模式的 0 相比不再为零，却也不是主要行为。** 三次测量（#83、#86、复测）在默认模式上合并率恒为 0；单工具模式确实把它推离了 0，这是本任务唯一确凿的正面结论。但 5 次里 3 次仍然把读、写、验证拆在三次调用。
2. **错误率显著更高（2–4 对 0–1），且失败集中在调用约定上**：`glob("*")` 传字符串 5 次里出现 5 次，`print` 未定义 1 次，`package.json` 不存在 1 次。前两项是引擎与提示词的差距，不是模型的错。默认模式下 `bash` 兜住了这些差距。
3. **成本优势来自工具面更小，不是来自合并。** input 低 20% 主要是地板从 8,084 降到 2,970，与合并无关；换成更长的会话，省下的固定成本占比还会更低。

因此结论是**补能力**：单工具模式证明"能强制写代码"，但还没证明"写代码更省"。按 #91 预设的第三分支，应记录失败原因是"错误可教性/调用约定"，而不是继续加提示词篇幅。

### 对后续的建议

按接受标准，以下三条各自独立、都基于本轮实测：

- **引擎能力补齐**：`console`/`print` 已补；`glob` 等工具接受 `{pattern}` 对象这一约定，是 `tools.*` 与 `run()` 两种调用风格并存造成的，应在 schema 之外用一句提示说明，或让 `glob("x")` 直接等价于 `glob({pattern:"x"})`。
- **不要在默认模式里保留单工具**：默认模式 5/5 成功、错误更少，没有理由替换。
- **若还要再试一次这个方向**，需要换一个"无法用一条 bash 命令完成"的任务，本 fixture 里 `sed`/`git checkout` 仍能一步到位，这是模型拆开调用的重要原因之一。

### 后续 issue（2026-11-25 建）

本轮实测发现的问题已拆成独立 issue，各自有可验证的验收标准：

| Issue | 内容 | 来源 |
| --- | --- | --- |
| #92 | 默认模式的 `script` 仍缺 `console`/`print`（本任务只在 solo 修了） | 收尾时复测发现 |
| #93 | `soloCompiled` 是进程级无界源码缓存，无淘汰、收益未测量 | 实现自审 |
| #94 | `tools.glob("*")` 等"裸字符串参数"应被接受或由提示词消歧 | 测量：5/5 次运行各失败一次 |
| #95 | `read_file` 等文件工具的相对路径按进程 cwd 解析，与 `run()` 的 `CWD` 不一致 | 测量后复现 |
| #96 | `glob` 无结果时回吐 ripgrep 原生报错，应像 `grep` 一样返回 `"no matches"` | 测量日志 |
| #97 | 需要设计"单条 bash 无法完成"的任务集，才能公平判断合并收益 | 本轮 fixture 的已知局限 |

其中 #92、#95、#96 是同一类问题的不同侧面：单工具模式把默认模式里由 `bash` 兜住的粗糙处暴露了出来。或者说，#91 的价值不只在于那个模式，也在于它把这些粗糙处变成了可测量、可追踪的条目。

## 修复后复测（2026-11-25，提交 `162ba9a`）

上面判"补能力"时的错误率数字**是脏的**，原因有三条，均已修复：

1. `run()` 的非零退出码被记为 tool error，于是脚本里每次失败的 `go test`（那正是任务要修的东西）都算一次错误。
2. `tools.glob("*")` 的裸字符串参数在 5/5 次运行中各失败一次（#94）。
3. `grep` 在工作目录无文件时回吐 ripgrep 原文（#96）。

### 同一 fixture 重测（每臂 5 次，同一二进制 `clean1`，只改 `features`）

| 指标 | solo | default |
| --- | ---: | ---: |
| requests（中位，跨度） | **6**（5–8） | 8（7–9） |
| input tokens（中位，跨度） | **17,135**（15,473–26,441） | 23,406（19,922–32,113） |
| tool_errors（每次运行） | 0,0,0,2,0 | 1,1,1,2,1 |
| 测试最终通过 | 5 / 5 | 5 / 5 |
| 同一次调用内读+写 | 1 / 5 | 不适用 |

### 结论修正

原判定里"错误率 2–4 对 0–1"这一条**不再成立**：修复后 solo 是 0–2，默认模式是 1–2，solo 反而略好（5 次中 4 次零错误）。剩下的一次错误是 `edit_file: old_string not found`，属于真实的编辑失败，不是模式缺陷。

同时 requests 与 input 的中位数也都低于默认模式（6 对 8、17,135 对 23,406），这与修复前"Solo 只靠更小的地板省成本"的解释不同——现在它同时用更少的请求。

因此 #91 的"补能力"结论里，**错误率不再是反对 solo 成为默认形态的理由**。仍然成立的两条理由：

- 同一次调用内合并只有 1/5，绝大多数运行仍把读、写、验证拆开；
- 本轮 fixture 能被 `sed`/`git checkout` 一步解决，无法证明"必须合并"的场景（#97）。
