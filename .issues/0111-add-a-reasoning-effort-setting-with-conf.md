---
id: 111
title: Add a reasoning-effort setting with config, CLI flag, and /effort slash command
status: closed
priority: high
labels:
    - feature
    - llm
    - config
    - cli
    - tui
    - docs
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
---

## 背景

Neo 目前**完全没有**思考强度（reasoning effort / thinking budget）的控制面，已核实：

- `llm.Request`（internal/llm/provider.go:66-76）只有 `Model / System / SystemBlocks / Messages / Tools / MaxTokens`，没有任何思考字段。
- 四个适配器的请求体都不下发该参数：
  - Anthropic（internal/llm/anthropic/client.go:55-62）：`model, system, messages, tools, max_tokens, stream`，没有 `thinking`。
  - OpenAI Responses（internal/llm/openai/responses.go:33-43）：没有任何 `reasoning.effort`；`include: ["reasoning.encrypted_content"]` 只是回放，不是强度。
  - Google（internal/llm/google/client.go:147-156）：`generationConfig` 只有 `maxOutputTokens`，没有 `thinkingConfig`。
  - Chat Completions / 自定义后端（internal/llm/chatcompletions/client.go:107,177）：没有 `reasoning_effort`。
- 配置面（internal/config/config.go、internal/config/defaults/neo.yaml）没有对应键；CLI 没有对应开关；斜杠命令里没有 `/effort` 或 `/thinking`（internal/tui/info.go:20-30）。

结果是"强度"完全由各家服务端对"省略该字段"的默认值决定，用户无法降低 reasoning token 消耗，也无法在难题上提高质量。

## 目标

给用户一个**统一档位**，覆盖配置、命令行、会话内切换三条路径，并映射到各后端原生参数；不设置时保持今天的线上行为（不下发任何字段）。

## 档位语义（建议，实现时可调整字面值）

统一四档 + 一个"不设置"态：

| 档位 | 语义 |
| --- | --- |
| `low` | 少思考，省钱/省时 |
| `medium` | 默认档（多数推理模型的服务端默认） |
| `high` | 难题、深推理 |
| 未设置 | 今天的行为：请求里不带任何思考字段，交给服务端默认 |

**决策（2026-11-26）：只做以上几个常见默认项，不做本地白名单严格校验。** 取值按原样透传给后端；某个模型/后端不认这个值就让它返回 API 错误，Neo 把服务端的错误原样提示给用户。理由：各家取值集合在变（`minimal`、`none`、`xhigh` 等各不相同），本地写死白名单会把服务端后来支持的档位挡在门外，还会在升级模型后误报。仅做空串/大小写/首尾空白的归一化。

关键约束：**"未设置"必须与当前输出字节级一致**（请求体不变），避免给不关心此项的用户带来行为变化和回归风险。

## 各后端映射（按现有代码落点）

| 后端 | 请求字段 | 落点 |
| --- | --- | --- |
| Anthropic | `thinking: {type:"enabled", budget_tokens:<n>}` | anthropic/client.go `apiRequest`。注意 `budget_tokens` 必须 < `max_tokens`，而现在 `defaultMaxTokens = 16384`（client.go:24）是硬编码，需要在设置 budget 时同步提高上限 |
| OpenAI Responses（api_key + Codex 共用） | `reasoning: {effort: "low"\|"medium"\|"high"}` | openai/responses.go `apiRequest` + `buildAPIRequest`，Codex 走同一函数 |
| Google Gemini | `generationConfig.thinkingConfig: {thinkingBudget: <n>, includeThoughts?: bool}` | google/client.go `generationConfig` |
| Chat Completions（OpenRouter + `providers:` 自定义） | `reasoning_effort`（OpenRouter 也接受 `reasoning: {effort}`，以实测为准） | chatcompletions/client.go `Request` |

无法识别档位或后端不支持时必须**静默降级为不发送**，不能因为一个可选旋钮让请求失败。

## 实现要点

### 1. 请求层（internal/llm）

- `llm.Request` 增加可选字段，建议用 `Effort string`（空字符串 = 未设置），不要用枚举类型贯穿各包，字符串在适配器里各自映射最简单。
- `internal/agent/agent.go:465` 构造请求时带上运行时的 effort；`internal/compact/summarize.go:175` 的压缩摘要调用建议**固定为低档或沿用当前档**——压缩请求要便宜，且它与主对话的思考预算无关。这一点需要明确决策并写进文档。
- 三处 `llm.Request` 构造点（agent、compact 以及测试替身）都要一致处理。

### 2. 配置（internal/config）

- 新增顶层键，例如：

    ```yaml
    reasoning:
      effort: medium   # none | low | medium | high；省略 = 不设置
    ```

  或平铺 `reasoning_effort: medium`。二选一，但必须与现有风格一致（现有 `goals:`、`compaction:` 都是 section，倾向 section 以便后续加 `include_thoughts` 等子项）。
- 解析时**不做取值白名单校验**（见上文决策），只归一化大小写与首尾空白；非法值由后端 API 报错，Neo 需要保证这个错误不被吞掉而是显示出来。
- 需要像 `ContextWindowFor` 一样有单一解析函数，供 coordinator、subagent runner、headless `run` 共用。
- 会话内切换要持久化：参照 `WriteFeature`（internal/config/write.go:181）新增 `WriteEffort`，保持其余行字节不变；若新增的是 section 而不是 features 子键，需要对应的 section 内写入实现。

### 3. 命令行（cmd/neo）

- 参照 `--agent` / `--script` 的全局参数做法（main.go:213-225、`takeScriptFlag`）：`takeEffortFlag` 在子命令分发前抽取，支持 `--effort=high` 与 `--effort high` 两种写法，对 `neo`（chat）与 `neo run`（headless）都生效。
- `--effort` 只在本次进程内生效（像 `--script` 那样写进内存 cfg），**不写回配置文件**；需要持久化用 `/effort` 或手改配置。这个区别要在 `docs/developer/cli.md` 写清楚。
- 参数值不做白名单校验，原样透传（见上文决策）。

### 4. TUI 斜杠命令

- 新增 `/effort`（如果更想要 `/thinking` 可作别名，二者都指向同一处理器；建议主名 `/effort`，因为各后端字段都叫 effort/thinkingBudget 不一致）。
- 支持：
  - `/effort` 无参数 → 显示当前档位与来源（配置/命令行/会话内切换/未设置）；
  - `/effort high` → 立即生效并持久化；
  - 无法识别的值 → 仍然接受并透传（不做本地白名单），只有后端返回错误时才把错误显示出来；本地不拦截。
- 落点：internal/tui/model.go:728 的 switch 分支、`handleCodeModeCommand` 附近的处理器风格、`baseSlashCommands`（internal/tui/info.go:20）加条目，以及 TUI 需要回调到 `chatRuntime` 的 setter（参照 `switchCodeMode` 在 main.go:1368 的"先应用再持久化、失败回滚"模式）。
- 档位应不依赖 turn 是否 busy；但为了稳妥可参照 `slashCommandRequiresIdle` 的现有规则决定，并保持一致。

### 5. 状态可见

- 需要在某处看到当前档位：建议 `/usage`（internal/tui/info.go 的 usageBlock）或 `neo doctor` 输出中增加一行，否则用户无法判断设置是否生效。
- headless `neo run --json` 的 summary 建议增加 `effort` 字段（与 `provider/model` 同级），便于脚本断言。

### 6. 文档

- `docs/developer/config.md`：新增配置段落，说明"未设置 = 不下发字段"。
- `docs/developer/cli.md`：`--effort` 与"不持久化"的说明。
- `docs/developer/index.md` 是否需要新增独立文档视内容量决定。
- `CHANGELOG.md` 的 Unreleased / Added。
- `internal/config/defaults/neo.yaml` 与 `neo.yaml.example` 加注释示例（保持注释掉，不改默认输出）。

## 验收标准

- 单测（internal/llm/anthropic）：设置 medium 时请求体含 `thinking`，且 `budget_tokens < max_tokens`；未设置时请求体与今天一致（可用整段 JSON 快照对比）。
- 单测（internal/llm/openai）：`reasoning.effort` 正确映射；未设置时字段完全不出现（注意 `omitempty` 对嵌套结构的坑）。
- 单测（internal/llm/google）：`generationConfig.thinkingConfig` 正确映射；仅有 `MaxTokens` 时 `generationConfig` 结构不变。
- 单测（internal/llm/chatcompletions）：`reasoning_effort` 正确映射且未设置时不出现。
- 单测（internal/config）：取值解析与归一化（大小写/空白）；未知取值**不报错**而是原样保留；`WriteEffort` 只改目标行、其余行字节不变。
- 单测（cmd/neo）：`--effort` 两种写法、非法值退出码 2、不写回配置。
- 单测（internal/tui）：`/effort` 无参数显示当前值、设置后持久化、非法值不改状态。
- 端到端（可选但有价值）：`neo run --effort low --json` 的 summary 含 effort，且对 mock provider 的请求体断言生效。
- 文档：上述四处更新完成；`CHANGELOG.md` 记录。
- `go test ./...`、`go vet ./...`、`qlty check` 通过。

## 实现记录（2026-11-26）

已按下列方式落地，与上文有几处偏离，记录原因：

- **配置形态选平铺顶层键 `reasoning_effort`，没有用 `reasoning:` section。** 现有写回机制（internal/config/write.go）是按顶层 scalar 重写单行，section 需要另写一套缩进块编辑；平铺键完全复用了 `WriteBackend`/`WriteModel` 的 setTopLevelScalar 路径，改动面最小。
- **Anthropic 与 Gemini 无法透传"词"。** 这两个 API 只接受 token 预算，所以 `low/medium/high` 被映射为预算（Anthropic 2048/8192/16384，Gemini 1024/8192/24576），另接受纯整数直接作为预算。无法映射的值**返回明确错误**（指明可用形式），而不是静默丢弃——因为词根本不会被发送，服务端永远无法报错，这是用户唯一能得知设置未生效的地方。OpenAI / OpenRouter / 自定义后端则原样透传，由服务端报错。
- **Anthropic 的 max_tokens 冲突已解决**：设置档位时把 `max_tokens` 抬到 `budget + 16384`，保证预算之外仍有回答空间（API 要求 budget < max_tokens）。
- **压缩摘要不继承档位**：compact/summarize.go 的请求不带 Effort。摘要是机械改写，为它付思考预算是纯浪费。
- **子代理继承档位**：subagent.AgentRunner 增加 `Effort` 字段与 `SetEffort`，`/effort` 切换时同步，符合"子代理跟随协调者"的既有约定。
- **/`--effort` 与 `/effort` 的区别**：flag 只在本次进程内生效且从不写配置（有测试守护）；`/effort` 立即生效并持久化，写失败会回滚内存状态。
- **顺带修复**：config 写回在内嵌默认配置路径下第二次写入会从内嵌模板重建、丢掉上一次的写入。已抽出 `writeTarget()` + `rememberSource()` 统一 `writeScalars`/`WriteFeature`/`removeTopLevelScalar` 的写目标解析。
- 另外修正了 internal/agent/agent.go 中 `Backend` 与 `Compactor` 注释错位（qlty staticcheck ST1020 报出）。

验证：`go build ./...`、`go vet ./...`、`go test ./...`、`qlty check --all`（退出码 0）均通过；端到端确认 `neo run --effort=bogus` 的 summary 含 `effort` 且错误信息可读，未设置时该字段不出现。

## 档位扩展（2026-11-26 后续）

按用户要求把默认词表从 `low/medium/high` 扩为 `none/low/medium/high/xhigh/max`：

| 档位 | Anthropic budget | Gemini budget |
| --- | --- | --- |
| `none` | 省略 `thinking` 字段 | `thinkingBudget: 0` |
| `low` | 2048 | 1024 |
| `medium` | 8192 | 8192 |
| `high` | 16384 | 24576 |
| `xhigh` | 24576 | 32768 |
| `max` | 32000 | 65536 |

两个实现要点：

- **Gemini 的 0 必须显式序列化。** `ThinkingBudget` 原本带 `omitempty`，`0` 会被丢弃、回退成模型的动态默认，等于把 `none` 悄悄失效。已去掉该 tag，并有测试断言 JSON 里出现 `"thinkingBudget":0`。
- **Anthropic 没有"关闭"值。** Messages API 只能靠省略字段关闭扩展思考，所以 `none` 在 Anthropic 上产生的请求与"未设置"完全相同。这是该 API 能给的最诚实表达，已在 docs 与状态文案里写明区别。
- `EffortNames` 常量在两侧各有一份，并有测试校验它与 `EffortBudgets` 不漂移；另有测试保证档位预算随名称单调递增。
- OpenAI / OpenRouter / 自定义后端仍是原样透传：`none`/`xhigh`/`max` 是否被接受由模型决定，服务端报错即提示。

验证：`go test ./...`、`go vet ./...`、`qlty check --all` 通过；本地实测 `neo run --effort=<各档>` 均正常进入 provider 层（仅因假凭证 401），`bogus` 给出带完整词表的错误。

## 透传后端自查（2026-11-26 后续）

用户实测时反馈 `/effort max` 的确认文案 `the backend rejects an unsupported value` 读起来像报错。查证：

- 该句是 `internal/tui/model.go` 里 `effortStatusLine` 的**成功回执**，不是错误，措辞把设计原则（"后端会拒绝不认识的值"）拼进了成功提示。已改为 `effort %s: sent as the reasoning strength on every request`。
- 同时发现一个真实缺口：**Chat Completions 适配器（含所有 `providers:` 自定义后端）此前完全没有请求体日志**，而它恰恰是最可能静默忽略 `reasoning_effort` 的一类后端——出错和成功在日志上无法区分。已补 `logx.Debug("provider request", ..., "reasoning_effort", ...)`，并在 `NEO_LOG_VERBOSE=1` 时输出完整 body（默认为 sha256）。
- 文档新增一节 "Checking whether a pass-through backend accepted the value"，给出两条自查手段：查 `NEO_LOG` 里的 `reasoning_effort` 字段；对比同 prompt 不同档位下的 `usage.output_tokens`（完全相等即说明被忽略）。

实测（本地假 endpoint）：默认日志已出现 `"reasoning_effort":"max"`；`NEO_LOG_VERBOSE=1` 可看到完整请求体。

## 备注

- 与 #108/#109（上下文窗口按模型解析、从目录发现窗口）同类：都是"把一个 provider 差异参数收敛成单一解析函数，并让 UI 可见"。建议复用同样的处理风格，而不是每个调用点各自读配置。
- Anthropic 的 `budget_tokens` 与硬编码 `defaultMaxTokens = 16384` 会互相约束，这是本改动里唯一有实质风险的适配器，需要单独确认策略（设置档位时把 max_tokens 提到多少、是否允许用户覆盖）。
- 是否让 subagent 继承 coordinator 的档位，建议**继承**（与 model/provider 的 `subagents` 逻辑一致），如需独立应另开 issue。
- 明确不做：不暴露 `temperature`/`top_p` 等采样参数，本次只做思考强度。
