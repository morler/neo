---
id: 44
title: Make read_file and bash truncation suggest the next call
status: closed
priority: medium
labels:
    - feature
relations:
    blocks:
        - 46
    depends-on:
        - 38
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

所有工具共用一个上限：`internal/tools/tool.go:15` 的 `MaxOutputBytes = 64 * 1024`，`bash` 与 `read_file` 都别名到它（`bash.go:21`、`fs.go:20`）。`bash` 的截断标记已经不错（`bash.go:267` 说明省略多少字节、保留多少头尾），但 `read_file` 只说「narrow with offset/limit」（`fs.go:269`），没有给出具体可执行的下一步。

## 范围（已拆分）

本任务只做**可执行性**：让截断结果直接告诉模型下一条调用怎么写。可配置上限已拆到 #52，因为默认值不应改变，且需要独立验证不回归。

## 实现要点

1. `read_file` 超限时，给出下一条可用调用：`offset=<读到下一行>, limit=<本次 limit>`。不要计算「一次能取完」的容量——那需要重复字节预算逻辑，且没人验证过。`fs.go:138` 的 `readFileWindow` 已有 offset/limit 语义，直接复用。
2. `bash` 的标记保持现有格式，补一句可执行建议（例如用管道收敛输出，或 `| tail -n 200`）。
3. 不改 `grep`/`glob` 的输出格式，它们被测试与解析依赖。
4. 不要调整默认上限值——本次不改变任何既有调用拿到的字节数。

## 验收标准

- 测试：读取超限文件时返回文本包含可解析的 `offset=` 与 `limit=`；用该建议值再次调用能取到后续内容（端到端，不只断言字符串）。
- 测试：默认配置下 `bash` 与 `read_file` 的既有截断输出逐字节不变。
- `go test ./internal/tools/`、`go vet ./...`、`qlty check --all` 通过。
- `docs/developer/tools.md` 说明截断提示的格式。
