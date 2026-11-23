---
id: 24
title: Stop the qlty exclude pattern from skipping internal/config
status: closed
priority: medium
labels:
    - tooling
    - bug
created: "2026-11-23"
updated: "2026-11-23"
closed: "2026-11-23"
---

## 背景

`.qlty/qlty.toml:26` 的 `"**/config/**"` 来自 `qlty init` 的默认模板，本意是跳过各生态的应用配置目录。但在本仓库它命中了真实的 Go 包 `internal/config`，使整个包对 qlty 永久失明。

受影响文件（6 个 `.go`，共 1969 行）：

    internal/config/config.go
    internal/config/config_test.go
    internal/config/providers.go
    internal/config/providers_test.go
    internal/config/write.go
    internal/config/write_test.go

## 证据

在同一份故意写坏的文件 `internal/config/zz_probe.go`（未格式化 + 拼写错误）上做对照实验：

    ### A) 默认配置（含 **/config/**）:  ✔ No issues
    ### B) 移除 **/config/** 后:
    internal/config/zz_probe.go:0:0 fmt    Incorrect formatting  golangci-lint:fmt
    internal/config/zz_probe.go:0:0 fmt    Incorrect formatting  gofmt:fmt
    internal/config/zz_probe.go:3:0 medium `teh` is a misspelling of `the`  golangci-lint:misspell
    internal/config/zz_probe.go:4:0 medium File is not properly formatted  golangci-lint:gofmt
    ✖ 4 issues

旁证：`gofmt` 插件只覆盖 138 个文件，而 `git ls-files '*.go'` 是 144 个，差额正是这 6 个。

当前该包代码是干净的（`gofmt -l internal/config` 为空、`golangci-lint run ./internal/config/...` 为 0 issues），所以这是覆盖缺口而非现存缺陷；但 `/provider`、`/model` 持久化都改这个包，缺口会持续存在。

## 目标

让 `internal/config` 重新进入 qlty 的检查范围，且不引入新的误报。

## 实现要点

- 删除 `.qlty/qlty.toml:26` 的 `"**/config/**"` 一行。实测该行只影响本仓库的 `internal/config`：移除后检查数 462 → 478，无新增 issue。
- 若将来确有需要排除的第三方配置目录，改用 `[[exclude]]` 的 `file_patterns` 精确指定，而不是全局目录通配。

## 验收标准

- `qlty check --all` 报告的检查文件数从 462 变为 478，且结果仍为 `No issues`。
- 反向验证：临时在 `internal/config` 放入一个未格式化的 `.go` 文件时，`qlty check --all` 能报出 gofmt 问题（验证后删除该探针文件）。
- `git status` 除 `.qlty/qlty.toml` 外无残留改动。
