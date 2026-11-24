---
id: 52
title: Add configurable per-tool output limits
status: wontfix
priority: low
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #44 拆出的第二半，独立于「截断提示」的可执行性改进，可以晚做。

现状是全局一刀切：`internal/tools/tool.go:15` 的 `MaxOutputBytes = 64 * 1024`，`bash.go:21` 与 `fs.go:20` 都直接别名到它。不同工具的可接受上限其实不同：`grep` 的 `output: count|files` 模式天然输出很小，`bash` 跑完整测试套件则可能确实需要更多。

killi 的结论（decision 36）是：工具结果占上下文约一半，但其中 98% 命中缓存，所以**真正该优化的是首次写入量**；盲目把上限砍小会换来额外往返，是净亏。因此本任务只做「可配置」，**默认值保持不变**。

## 前序

#44 已完成：截断时给出可执行的下一步参数。

## 实现要点

1. 配置项 `tools.max_output_bytes`（全局默认）与按工具覆盖 `tools.bash_max_output_bytes` 等；未设置时回落到 64 KiB，行为与现在逐字节一致。
2. 配置结构放在 `internal/config`，具体上限由 `cmd/neo` 构造工具时注入（现有 `newRegistry` 已经是注入点），不要给 `internal/tools` 加全局可变状态。
3. 非法值（0、负数、超过一个合理上限如 4 MiB）在加载时报错，不静默回退。
4. 截断标记里的字节数必须反映实际生效的上限，不能在覆盖后仍报旧值。
5. 不改 `grep`/`glob` 的输出格式，它们被解析依赖。

## 验收标准

- 默认配置下 `bash` 与 `read_file` 的截断行为与改动前完全一致（既有测试不改动即通过）。
- 设置 `tools.bash_max_output_bytes: 1024` 后，`bash` 输出上限随之变化，另一工具不受影响。
- 非法值加载失败并给出字段名与允许范围。
- `go test ./internal/tools/ ./internal/config/ ./cmd/neo/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/config.md` 记录两项配置与默认值。

## Closed
ponytail review: 默认上限不改变，配置项写完也没人设，属于死灵活性。等有人真的调过上限再加。
