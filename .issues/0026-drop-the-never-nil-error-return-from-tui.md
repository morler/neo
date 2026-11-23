---
id: 26
title: Drop the never-nil error return from tui newModel
status: closed
priority: low
labels:
    - refactor
    - cleanup
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`internal/tui/model.go:304` 的构造函数签名带一个永远为 nil 的 `error`：

    func newModel(ctx context.Context, ag *agent.Agent, modelTag, version, workingDir string, sk []skills.Skill, opts Options) (*model, error)

核实：函数体从第 304 行到第 405 行，只有一条返回语句 `return m, nil`（`internal/tui/model.go:403`）。唯一可能产生错误的是 `glamour.NewTermRenderer`，而它在 `internal/tui/model.go:314-316` 被就地吞掉：

    if err != nil {
        md = nil
    }

## 影响范围

4 个调用点都在判断一个恒为 nil 的 err：

    internal/tui/model.go:127             m, err := newModel(ctx, ag, model, version, cwd, sk, opts)
    internal/tui/model_view_test.go:20    m, err := newModel(context.Background(), base.ag, "test", "dev", "", nil, Options{})
    internal/tui/model_view_test.go:55    m, err := newModel(context.Background(), base.ag, "test", "dev", workingDir, nil, Options{})
    internal/tui/goal_wiring_test.go:100  m, err := newModel(t.Context(), base.ag, "test", "dev", "", nil, Options{

这符合 MISSION 的「prefer deleting accidental complexity」：一个永远不会触发的错误分支会让读者误以为构造可能失败。

## 目标

让签名如实反映行为，同时不改变任何运行时表现。

## 实现要点

- 把返回类型改为 `*model`，删除唯一的 `return m, nil` 中的 nil 错误。
- 同步更新 4 个调用点，去掉 `if err != nil` 分支。
- 保留 `internal/tui/model.go:314-316` 现有的降级行为（渲染器创建失败时 `md = nil`），不要在本次改动里新增错误传播。

## 验收标准

- `go test ./internal/tui/` 通过，且用例断言内容不变（不为了让测试编译而放宽断言）。
- `golangci-lint run --config <临时配置启用 unparam> ./internal/tui/...` 不再报告 `newModel - result 1 (error) is always nil`。
- `go build ./...` 通过。
