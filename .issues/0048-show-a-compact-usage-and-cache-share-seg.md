---
id: 48
title: Show a compact usage and cache-share segment in the TUI footer
status: wontfix
priority: medium
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是 #36 拆出的第二半。`/usage` 提供按需明细，但用户还需要一眼看到当前会话的 token 走势，否则要反复敲命令。

`internal/tui/model.go:991-1007` 的 `footerLine` 目前右侧只渲染 `backendLabel(m.providerTag, m.modelTag)`，左侧是 cwd 与分支。宽度不足时它已经会从 `gap > 0` 退化为 `  ` 拼接，最后 `truncate` 到宽度。

## 前序

#36 必须已完成，它提供两个可复用件：
- `llm.Usage` 的分项读取口径（`PromptTokens()` 为分母）；
- 一个格式化函数（`k`/`M` 缩写、一位小数、0 值显示 `0`）。

本任务只做 footer 接入，不重复实现格式化。

## 实现要点

1. 在 `footerLine` 中计算用量片段，形如 `12.4k in · 84% cached · 921 out`。
2. 放到右侧、`backendLabel` 之前还是之后要显式决定并在注释写明：建议 `其后`，因为后端标签是身份，用量是动态值。
3. 宽度不足时的降级顺序必须确定：先丢用量片段，再丢分支，最后才截断 cwd。实现时把 `left`/`right` 拼接逻辑抽成可测的小函数，不要继续在 `footerLine` 里堆条件。
4. 会话没有任何响应（`PromptTokens() == 0`）时不渲染该片段，而不是显示 `0 in · 0% cached · 0 out`。
5. `/clear` 后 footer 必须回到无片段状态，验证 `resetConversation` 与 footer 读取的是同一份 `Usage`。

## 验收标准

- 表驱动测试：`PromptTokens()==0` 无片段；全部命中缓存时显示 `100% cached`；混合时百分比四舍五入正确。
- 窄宽度（如 40 列）渲染不 panic，且不截断 cwd 的前两级目录。
- 不修改 `internal/agent` 与用量统计逻辑。
- `go test ./internal/tui/`、`go vet ./...`、`qlty check --all` 通过。

## Closed
ponytail review: footerLine 已在窄宽度下做降级，再塞动态数字属于装饰。/usage 按需查询已覆盖。
