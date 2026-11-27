---
id: 130
title: 'Accept run(argv, {timeout: n}) instead of rejecting the object shape'
status: closed
priority: high
labels:
    - bug
    - tools
created: "2026-11-27"
updated: "2026-11-27"
closed: "2026-11-27"
---

## 问题

模型第一次写 `script` 时，若把 `run()` 的第二个参数按宿主工具的形状写成对象，调用直接失败：

```
run(["go", "test", "./..."], {timeout: 600})
→ script threw: Error: run's timeout must be a number of seconds, got map[string]interface {}
```

`internal/tools/script_run.go:193-199` 只把第二参数交给 `runSeconds`，后者接受 float/int/int64/int32/数字字符串，`map[string]any` 落到 `default` 分支（:226）。而 `script` 里其他每个全局（`tools.read_file`、`tools.grep` …）都吃对象，模型按同一习惯写 `{timeout: n}` 是很自然的误推；报错只说 "got map[string]interface {}"，既不点明正确形状，也不提示已经收到的 timeout 值，模型只能猜。

同一天里有两次真实命中：#91 记录过 `run([...], 10)` 的整数写法被拒三次并已修复；本次会话中协调者自己的脚本也写了一次 `run([...], {cwd: ...})` 同形状失败。

## 建议

- `run()` 的第二参数同时接受 `{timeout: n}`（对象里读 `timeout`，其余键忽略），因为形状无歧义，且 `#91`、`#94` 已确立"一个显而易见的误推形状直接接受"的先例。
- 对象里没有 `timeout` 或类型仍不可解析时，报错点名可写的形状：`run([...], 10)` 或 `run([...], {timeout: 10})`。
- 不改 `codeModeSpec` 描述（prompt 字节不动）。

## 验收标准

- `run(["echo","hi"], {timeout: 5})` 成功，退出码 0。
- `run(["echo","hi"], {cwd: "."})` 报错文本包含 `{timeout: 10}`，即给出正确写法。
- 既有数字/数字字符串写法不变（`TestSoloRunAcceptsEveryNumericTimeout` 仍过）。
- `go test ./...`、`qlty check` 通过；`TestCodeModeFloorBudget` / `TestScriptFloorBudget` / `TestFixedPromptFloorBudget` 字节数不变。
- `docs/developer/tools.md` 的 Command timeouts 小节记录该形状。
