---
id: 109
title: Discover a custom model's context window from its catalogue instead of requiring manual config
status: closed
priority: medium
labels:
    - feature
relations:
    related-to:
        - 108
created: "2026-11-25"
updated: "2026-11-26"
closed: "2026-11-26"
---

## 背景

#108 让上下文窗口可以按模型配置。但配置终究要用户手填，而多数 OpenAI 兼容服务已经**主动回报**了窗口大小，Neo 目前完全不读。

实测（2026-11-25，本机 llama-server 8080）：

    GET /v1/models  ->
    "meta":{"n_ctx":40192,"n_ctx_train":262144,"n_params":27320697856,...}

而 Neo 的文档（docs/developer/config.md:182）目前的说法是：

> Neo cannot discover a custom model's context size, so set it explicitly for models whose window differs from the 200,000-token default.

对 llama.cpp 而言这个前提不成立。

## 各后端可得的信号（需逐项核实）

| 后端 | 位置 | 字段 | 状态 |
| --- | --- | --- | --- |
| llama.cpp (llama-server) | GET /v1/models | meta.n_ctx | 已实测确认 |
| vLLM | GET /v1/models | max_model_len | 待核实 |
| Ollama | POST /api/show | model_info.*.context_length | 待核实 |
| OpenRouter | GET /api/v1/models | context_length | 待核实（内置 provider，可能另有路径） |

注意：OpenAI 官方的 /v1/models 只回 id/owned_by，**不含窗口**，所以不能假设通用可用。

## 目标

在用户没有显式配置时，尽量从服务端发现窗口大小；发现失败则退回默认，绝不因此让启动失败。

## 实现要点

- 复用已有的 catalogue 拉取路径：internal/llm/custom/models.go 已实现 GET <endpoint>/models 与解析（目前只取 ID/Name/Description）。扩展它顺便读窗口字段，避免第二次请求。
- 解析要宽容：字段缺失、类型不符（字符串 vs 数字）、结构未知时一律当作"未发现"，不报错。不同实现的字段名差异很大，靠的是可选探测而非契约。
- 优先级必须服从 #108：显式配置 > 发现值 > 内置默认。不要让自动发现覆盖用户显式写的值。
- 界面可见：在 /model 选择器或 /usage 里显示当前生效的窗口值与来源（配置/发现/默认），否则用户无法判断为何压缩在某处触发。
- 发现是网络调用，失败要静默降级并记 debug 日志，不能阻塞或拖慢启动（现有 catalogue 拉取已经是异步/懒加载，沿用）。
- 不改 compact 包。

## 验收标准

- 单测：解析 llama.cpp 形态（meta.n_ctx）的 /v1/models 响应，得到正确窗口。
- 单测：解析缺字段、字段类型错误、非 JSON 的响应，均返回"未发现"且不报错。
- 单测：显式配置存在时，发现值被忽略。
- 单测：无法发现时回退到内置默认 200000。
- 用本机 llama-server 做一次端到端：不配 context_window_tokens，压缩阈值应等于发现的 40192 × 0.7。
- 文档更新：config.md 的"cannot discover"表述需修正为按后端说明；CHANGELOG 记录。
- go test ./...、go vet ./...、qlty check 通过。

## 备注

与 #108 是互补关系。若 #108 先落地，本 issue 只需在解析函数里加一层"发现值"来源。
