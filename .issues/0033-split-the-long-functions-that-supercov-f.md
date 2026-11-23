---
id: 33
title: Split the long functions that supercov flags in agent, fs, openai, google and cli
status: closed
priority: low
labels:
    - refactor
relations:
    related-to:
        - 29
created: "2026-11-23"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`supercov quality` 全仓 7.89/10，命中最多的是 `long_method`（12 次）。下列函数长度用括号计数实测，可逐条核对：

| 函数 | 位置 | 行数 |
| --- | --- | --- |
| `func (a *Agent) run(ctx context.Context) (string, error)` | `internal/agent/agent.go:334-478` | 143 |
| `func runChatSession(ctx context.Context, store *session.Store, sess *session.Session, agentName string, streams stdio) int` | `cmd/neo/main.go:683-863` | 181 |
| `func readFileWindow(ctx context.Context, path string, offset, limit int) (string, error)` | `internal/tools/fs.go:138-234` | 97 |
| `func (a *Agent) executePreparedGroup(ctx context.Context, calls []preparedToolCall) []llm.ContentBlock` | `internal/agent/agent.go:646` | 68 |
| `func toInput(req llm.Request) []inputItem` | `internal/llm/openai/responses.go:243` | 68 |
| `func (c *Client) toLLMResponse(out response) (*llm.Response, error)` | `internal/llm/google/client.go:420` | 59 |
| `func toParts(blocks []llm.ContentBlock, toolRefs map[string]toolRef) []part` | `internal/llm/google/client.go:295` | 59 |
| `func (m *model) modelBrowserView() string` | `internal/tui/models.go:160-254` | 95 |
| `func (m *model) providerBrowserView() string` | `internal/tui/providers.go:142-220` | 79 |

`internal/tui/model.go` 的 `Update`（206 行）与 `newModel`（101 行）另列 issue，不在此处重复处理。

## `internal/tools/fs.go` 的实际结构问题

`readFileWindow` 所在区间有 40 行缩进 ≥3 层的代码，最深 6 层出现在 `internal/tools/fs.go:185`。核心是个大 `for` 循环同时负责：读切片、剥除换行、拼行号前缀、统计已选行数、处理 `bufio.ErrBufferFull`、处理 EOF 与尾行。`deep_nesting 0.84` 指的就是这里。

可拆成：`readChunk`（返回一行与是否结束行）、`writeSelectedLine`（拼行号与前缀）、`handleReadError`（`bufio.ErrBufferFull`/`io.EOF` 分支）。

## 目标

按"一个函数只做一件事"拆分上表函数，行为不变。`internal/tui` 的两个视图函数与 `magic_values` 的 issue 相邻，可在同一次改动里完成。

## 实现要点

- `internal/agent/agent.go:334` 的 `run` 是一个 `for turn := 0; turn < a.cfg.MaxTurns; turn++` 循环，末尾 `:472-477` 是 max-turns 兜底。把单轮逻辑抽成 `runTurn(ctx) (done bool, text string, err error)`，外层只保留循环与兜底。注意 `finalText` 是跨轮累积的，必须留在外面。
- `cmd/neo/main.go:683` 的 `runChatSession` 181 行里有多段可分离的关注点：加载配置（`:684-687`）、恢复 provider（`:689-712`）、模型切换回调（`:790-825`）、收尾持久化。建议先只拆出 provider 恢复与模型切换两段。
- `internal/llm/openai/responses.go:243` 的 `toInput` 处理多种 `ContentBlock` 类型，按类型分支抽成小函数即可。
- `internal/llm/google/client.go:295` 与 `:420` 是镜像的编解码，可各自按 block 类型拆分。
- 每个函数拆完补一个测试断言拆分前后的输出一致（这几个包已有较全的测试，`internal/agent` 87.9%、`internal/llm` 75.5%）。

## 验收

- `go test ./...` 与 `go test -race ./...` 全部通过。
- 拆分后无函数超过 70 行。
- `supercov quality file <path>` 对应文件不再命中 `long_method`／`deep_nesting`。
