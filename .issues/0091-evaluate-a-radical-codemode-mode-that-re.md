---
id: 91
title: Evaluate a radical codemode mode that replaces the tool surface with one eval tool
status: open
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
created: "2026-11-25"
updated: "2026-11-25"
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
- 默认固定地板 8,084 B（tools 5,022 + prompt 3,062）；`script` 开启后 9,555 B（tools 5,873 + prompt 3,682）。
- `scriptCapability` 实测 620 B，`script` schema 851 B，合计 1,471 B。**已修正（提交 `00e573a`）**：此前 `cmd/neo/performance_test.go`、`docs/performance.md`、`docs/design/token-budget.md`、`docs/developer/config.md`、`docs/developer/tools.md`、CLI 帮助与 `CHANGELOG.md` 一致写着 682 B / 1,533 B，均按实测改为 620 B / 1,471 B；同一句里"重写涨 78 B"也纠正为 56 B（564 → 620）。地板总数原本就是对的，682 疑似用 3,682 − 3,000 算出而非实测。本任务开始前**无需**再处理此项。
- 由此推算：把六个文件/命令工具换成单个 `script`，工具面约 2,980 B、prompt 3,682 B，合计约 6,662 B，即**约 −1.4 KB（≈ −355 tokens）每请求**。这是推算值，不是实测值，且前提是 `script` 的 schema 在加上 `run()`/持久语义后不增长——实际会增长，所以真实收益小于此数。

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
- 未出现合并（与 #83/#86 一致）→ **回退**，结论写“单工具化不是障碍所在”，并把力气转向 #87/#89/#90 那条线。

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
