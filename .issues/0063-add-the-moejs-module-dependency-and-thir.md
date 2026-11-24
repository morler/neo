---
id: 63
title: Add the moejs module dependency and third-party notices
status: closed
priority: high
labels:
    - config
relations:
    blocks:
        - 64
    depends-on:
        - 56
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

这是体积与依赖任务的第 1 步（原 #57）。先只做“引入依赖 + 记录许可”，不碰任何构建闸门数字，使失败面最小、可独立回退。

## 目标

`go.mod` 引入 moejs，生成第三方声明文件，构建仍然通过。

## 实现要点

- 在 `go.mod` 增加 `github.com/MoeForge/moejs`（版本取当前本地 `../moejs` 可用的 tagged 版本；若无 tag，先记录需要的 commit，并在 issue 里说明）。
- 执行 `go mod tidy`，确认新增模块只有 `moejs`、`moego`、`yaml/v3` 三项，没有额外传递依赖。
- 新增 `THIRD_PARTY_NOTICES.md`：列出 `moejs` 与 `moego` 的 Apache-2.0 声明、上游地址、版本。不改 Neo 自身的 MIT 声明。
- `.goreleaser.yaml` 的 `archives.files` 增加 `THIRD_PARTY_NOTICES.md`。
- 此步不添加任何 Go 引用，因此二进制体积不变；用 `go build ./...` 证明依赖不影响现有构建。

## 验收标准

- `go build ./...`、`go test ./...` 通过。
- `git diff go.mod` 只显示预期内的新增 require。
- `THIRD_PARTY_NOTICES.md` 含两个依赖的许可证名称、版本、上游 URL。
- `go test ./cmd/neo -run TestDocsLinks` 通过（若该文件被文档引用）。
