---
id: 124
title: Feed subagent progress into the visible workflow checklist
status: closed
priority: high
labels:
    - bug
created: "2026-11-27"
updated: "2026-11-27"
closed: "2026-11-27"
---

`workflow` 的工具描述、`docs/developer/tools.md` 和 `cmd/neo/main.go` 的 `subagentCapability` 提示都写着：Neo 会自动把子代理活动附加到当前工作流条目。实际没有——supervisor 的事件流从未被消费。

## Evidence

`cmd/neo/subagent.go` 里 `chatAgentTool` 把 `sup.Events` 作为第二个返回值交出来，但 `cmd/neo/main.go:1398` 直接丢弃：

```go
at, _, agentRunner = chatAgentTool(workerProvider, workerModel, cwd, root, cfg)
```

`chatExtraTools` 因此只返回 workflow 通道，`tui.WithWorkflowEvents` 是 TUI 唯一订阅的事件源。`rg 'subagent\.Event' cmd internal/tui` 在 supervisor 之外没有任何命中。`docs/developer/architecture.md:17` 甚至把缺失写成设计（"The TUI renders agent calls as ordinary tool receipts and does not consume the supervisor event stream"），与 `docs/developer/tools.md:15` 和 workflow 工具 schema 的承诺互相矛盾。

后果：回合进行中，清单的活动条目一直显示协调者最后一次工具调用的结果（`toolVerb` 的 "Agent <prompt>"），子代理的每次工具调用、错误和完成都不可见，唯一的子代理信号是最终的 `agent` 回执。提交 7a8b11c (#370) 删掉了子代理树和 supervisor 订阅，但承诺文字留了下来。

## Impact

Mission 把"agent、工具和子代理正在做什么的可见性"列为第 2 优先级，而 `workflow` 是唯一声称提供该可见性的界面。委派型回合中用户看到的是一行冻结的细节，无法判断一个 15 分钟的子代理是在推进还是卡死，也无法在父代理总结前看到子代理失败。prompt 又明确要求协调者不要手工镜像子调用，所以没有兜底。

## Acceptance criteria

- supervisor 事件流到达 TUI，并依据父 `agent` 调用的 tool-use ID（`subagent.Event.CallID`）关联到活动工作流条目。
- 一个子代理的活动只更新活动条目的 detail，不为每个事件追加块，不重排或移动已渲染的块。
- 上一轮或已被 `clear` 的工作流事件被忽略，复用 workflow 事件已有的 generation 检查。
- 子代理失败仍然可见：走现有回执/错误路径，不静默丢弃。
- 测试覆盖附加活动、丢弃陈旧事件、CLI 装配（`chatExtraTools` 返回非 nil 的 subagent 通道）。
- `docs/developer/tools.md`、`workflow` 工具描述、`docs/developer/architecture.md` 与代码行为一致。

## Notes

排查"子代理进度没有进入可见工作流"时发现。修复是接线，不是新增 UI 机制：清单块、原地变更和 generation 守卫都已存在。
