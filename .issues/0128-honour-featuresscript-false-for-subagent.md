---
id: 128
title: 'Honour features.script: false for subagents in code mode'
status: open
priority: medium
labels:
    - bug
created: "2026-11-27"
updated: "2026-11-27"
---

## 问题

`features.script: false` 在 code mode 下意味着"这个会话不执行代码"。协调者侧按此实现：`registryFor`（`cmd/neo/main.go:531-557`）在 `!scripted` 时把 `base` 置空，只留 workflow/goal 这些协调工具，headless 则直接拒绝启动（#102）。

子代理侧没有跟随：`AgentRunner.registryWithOptions`（`internal/subagent/runner.go:170-195`）的 code mode 分支无条件 `tools.NewScript(codeFiles)`，不看 `r.Script`。而 `Supervisor.toolsFor`（`internal/subagent/supervisor.go:83-85`）在 code mode 下也固定返回 `[]string{scriptToolName}`，不查询 `scriptEnabled()`。

结果：用户关掉了代码执行，协调者自己确实不能执行，但它派出的 work 子代理仍能读写文件和跑程序。

## Evidence

实测（`internal/subagent`，临时探针已删除）：

```text
AgentRunner{Script: false, CodeMode: true}
toolsFor(AgentModeWork)   = [script]
registryWithOptions(...)  = [script]
```

同一份设置下协调者的 `registryFor` 只返回协调工具。classic 面则一致：`AgentRunner{Script: false, CodeMode: false}` 的 work 注册表是 `[bash edit_file glob grep read_file write_file]`，符合预期（classic 面本就由 `bash` 承担执行）。

## Impact

安全与预期不一致：`docs/developer/config.md` 把 `script` 描述为"Register the JavaScript `script` tool"，用户据此关闭它以禁用执行；实际只关掉了协调者自己的执行路径，委派出去的子代理照样能跑命令。这不是权限模型的问题（Neo 的执行边界由环境负责），而是同一配置在两个装配点有两种解释——正是 `MISSION.md` 要避免的那类漂移。

## 验收标准

- `features.script: false` + code mode 时，work 子代理的注册表为空，且 `RunAgentWithOptions` 走既有"no tools is a configuration defect"分支（`internal/subagent/runner.go:113-117`）给出可读说明，而不是返回 ok=true。
- `Supervisor.toolsFor` 的 code mode 分支按 `scriptEnabled()` 决定是否请求 `script`，与 `registryWithOptions` 用同一个判断。
- 单测：上述组合下 work 子代理的注册表不含 `script`，且失败消息点名 `features.script` 与可用修法。
- classic 面行为不变（仍有 `bash` 与文件工具）。
- `Script: true` 时两处仍然一致。

## Notes

排查"子代理缺 shell 导致超时"时顺带发现，与那条问题独立。可分别修。
