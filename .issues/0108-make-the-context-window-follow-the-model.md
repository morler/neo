---
id: 108
title: Make the context window follow the model instead of one global value
status: open
priority: high
labels:
    - config
relations:
    related-to:
        - 109
        - 104
created: "2026-11-25"
updated: "2026-11-25"
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
