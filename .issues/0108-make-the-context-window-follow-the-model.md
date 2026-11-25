---
id: 108
title: Make the context window follow the model instead of one global value
status: closed
priority: high
labels:
    - config
relations:
    related-to:
        - 109
        - 104
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`context_window_tokens` 目前是**全局单值**，而上下文窗口本质上是**每个模型各自的属性**。实测确认这一设计在同一进程内切换模型时会失效。

配置结构（internal/config/config.go）：

    type Config struct {
        Provider string
        Model    string
        Providers map[string]CustomProvider   // 无窗口字段
        Compaction Compaction                // 唯一的窗口来源
    }
    type Compaction struct {
        ContextWindowTokens int `yaml:"context_window_tokens"`
    }

CustomProvider（internal/config/providers.go:22-40）与 CustomModel（:43-47）都没有任何窗口字段。

## 实测证据（2026-11-25，本机 llama-server）

本机 8080 端口跑 Bonsai2.5-27B，llama-server 在 /v1/models 里明确回报：

    "meta":{"n_ctx":40192,"n_ctx_train":262144,...}

即窗口信息对本地模型是**可得**的，Neo 只是没有读取它。

同一份 cfg 下用两个不同模型的 chatCompactor 对比（临时探针）：

    local 40k model:  Summarizer{Model:"Bonsai2.5-27B",              TriggerTokens:28134}
    cloud 200k model: Summarizer{Model:"anthropic/claude-sonnet-5",   TriggerTokens:28134}

两者拿到完全相同的触发阈值 28134（= 40192 × 0.7）。也就是说：

- 为 40K 本地模型把值设成 40192 后，切到 200K 云端模型会**每 28K tokens 压缩一次**，代价是频繁摘要与上下文损失。
- 保持默认 200000 时，40K 模型**永远等不到压缩**（触发线 140000 远超窗口），实测会直接撞服务端的 400 错误。

涉及的全部读取点只有三处，都取同一个 cfg 值：

| 位置 | 用途 |
| --- | --- |
| cmd/neo/main.go:1482 chatCompactor | 协调者压缩器 |
| cmd/neo/subagent.go:47 | 子代理 runner |
| internal/subagent/runner.go:104 | 每个 worker 的 summarizer |

## 目标

让上下文窗口跟着**具体模型**走，而不是整个进程走一个值。

## 实现要点

解析优先级（后者覆盖前者）：

1. 内置默认 200000（保持现状）
2. 顶层 compaction.context_window_tokens（保持兼容，作为兜底）
3. providers.<name>.models[].context_window_tokens（按模型）
4. 若 provider 只有一个默认模型，也可允许 providers.<name>.context_window_tokens（按 provider 的简写，可选）

要点：

- 新增字段加在 CustomModel 上，不要动 CustomProvider 的既有语义。
- 解析函数要能按 (provider, model) 二元组查表；供三处读取点共用，避免各写一份。
- 三处读取点（协调者、子代理 runner、worker）都要走同一个解析函数，否则会再次漂移。
- 运行时切换模型（/model、/provider）重建 summarizer 时必须重新解析，不能沿用旧值：chatCompactor 现在每次都调用，但传的是同一个 cfg，需要在其中接上 (provider, model) 查询。
- 子代理的 context_window_tokens 解析要跟协调者一致；若 subagents 指定了不同 backend，按其 backend 解析。
- 保持顶层 compaction.context_window_tokens 的现有行为与测试不变（它是兜底，不是要删除）。
- 不改 compact 包本身：TriggerTokensForContextWindow 的逻辑与 0.7 比例不变，只是喂进去的数字来源变精确。

## 验收标准

- 单测：两个不同窗口的模型在同一 cfg 下解析出不同阈值。
- 单测：只配顶层值时，所有模型仍用该值（向后兼容）。
- 单测：模型级配置覆盖顶层值。
- 单测：/model 切换后 summarizer 的触发阈值随之改变（可用现有 provider.go 的切换测试扩展）。
- 单测：子代理 runner 的窗口与协调者一致（或按 subagents backend 解析）。
- 文档：docs/developer/config.md 的 compaction 段说明新字段与优先级；CHANGELOG 记录新能力。
- go test ./...、go vet ./...、qlty check 通过。

## 备注

本 issue 只解决「配置粒度」。自动发现（读 llama.cpp 的 meta.n_ctx、vLLM 的 max_model_len、Ollama 的 /api/show）是另一个问题，见 #109。两者不冲突：显式配置优先，发现值作为兜底。

## 修复记录（2026-11-25）

### 设计决定

配置粒度定为**按模型**，落在 `providers.<name>.models[].context_window_tokens`。

解析优先级（`Config.ContextWindowFor(provider, model)`）：

1. 该 provider 下该模型的 `context_window_tokens`
2. 顶层 `compaction.context_window_tokens`
3. 返回 0，由 compact 包使用内置默认 200000

为什么用"按 provider 查模型"而不是给 provider 加单一数字：一个 provider 可以服务多个窗口不同的模型（本地 llama-server 切换模型就是这种情况），provider 级会重犯全局单值的老毛病。

动态目录里的模型（`GET /v1/models` 拉到、但未在 `models[]` 静态声明的 id）回退到全局键，不做模糊 ID 匹配——这是有意为之：静态声明即"我知道这个模型"，未声明则不该被猜中。这条也写进了 config.md。

### 改动

| 文件 | 改动 |
| --- | --- |
| internal/config/providers.go | `CustomModel` 增加 `ContextWindowTokens` |
| internal/config/config.go | 新增 `ContextWindowFor(provider, model)` |
| cmd/neo/main.go | `chatCompactor` 改用解析函数（每次 /model、/provider 都重新解析） |
| cmd/neo/subagent.go | runner 接上 `WindowFor`，不再复制协调者的全局值 |
| internal/subagent/runner.go | 新增 `WindowFor` 字段；`compactor` 按实际 provider+model 解析，回退旧字段 |
| internal/agent/agent.go | 新增 `Compactor()` 访问器，供测试断言切换后的窗口 |

issue 里点名的风险（三处读取点各写各的）已消除：协调者、runner、worker 现在都走 `cfg.ContextWindowFor`，runner 的 `compactor` 用 worker 实际会跑的 provider+model 查表，因此子代理与父会话不可能对窗口判断不一致。

### 测试

- `TestContextWindowFor`：模型覆盖、已声明但无覆盖、目录中未声明的模型、内置 provider、未知 provider、id 首尾空白，六种情形。
- `TestContextWindowForFallsThroughToZero`：nil cfg、空 cfg、只有模型无任何窗口值，均返回 0（交给默认），不乱造数字。
- `TestSetChatModelSwapsTheCompactionWindow`：本地 40K 模型 → 阈值 28,134；切到无覆盖的云端模型 → 阈值 140,000。这是 issue 要求的"切换后随之改变"。
- `TestChatAgentToolResolvesTheWindowPerWorkerModel`：runner 按 worker 模型解析，覆盖子代理场景。
- `TestProviderNameMatchesItsConfigKey`：**新增的不变式守卫**。解析以 provider 名为键，若某 provider 的 `Name()` 与配置键不一致，模型级覆盖会静默失效。测试遍历所有 provider 验证这一点。

最后一条是写测试时发现的真实风险：`FakeProvider.Name()` 返回 "fake"，我第一次的测试因此假通过——它其实在断言全局键。修正测试时补了这个守卫，确保生产路径（`custom.New` 用配置名、内置 provider 的 `Name()` 与键相同）不会漂移。

### 真机端到端（本机 llama-server 8080，Bonsai2.5-27B，n_ctx 40192）

配置：模型级 `context_window_tokens: 40192`，全局 `200000`。

```
global key: 200000
ContextWindowFor(local40k, Bonsai2.5-27B) = 40192
ContextWindowFor(local40k, other-model)  = 200000
ContextWindowFor(anthropic, claude)      = 200000
live compactor trigger = 28134 (expect 28134)
```

即：本地模型按 40,192 生效，其余一律回退全局。同一次运行的真实任务（3 个文件、500 行）exit=0、3 次请求、答案正确。

### 未验证

本轮没有跑出真实的压缩触发（需要把会话推到 28,134 tokens 以上），因此"压缩动作本身在 40K 窗口下是否按新阈值执行"仍只有单元级证据。压缩逻辑本身未改动（`TriggerTokensForContextWindow` 与 0.7 比例不变），风险低，但如实记录。
