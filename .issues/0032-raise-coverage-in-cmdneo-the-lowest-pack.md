---
id: 32
title: Raise coverage in cmd/neo, the lowest package at 58.8%
status: closed
priority: medium
labels:
    - test
    - cli
relations:
    related-to:
        - 30
created: "2026-11-23"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`supercov -- go test ./...` 报全仓行覆盖 85.40%（5888/6895）、分支 74.08%（2995/4043）。Go 原生工具复核为 82.4% statements。最低的包是 `cmd/neo`：

    $ go test ./cmd/neo/ -coverprofile=/tmp/cmd.out
    $ go tool cover -func=/tmp/cmd.out | tail -1
    total: (statements) 58.8%

对照包覆盖：`internal/approval` 100.0、`internal/goal` 98.4、`internal/config` 93.8、`internal/agent` 87.9、`internal/auth` 72.4、`internal/atomicfile` 61.5。

## 覆盖率 0.0% 的函数（可逐个核对）

| 位置 | 函数 |
| --- | --- |
| `cmd/neo/main.go:103` | `main` |
| `cmd/neo/main.go:129` | `initLogging` |
| `cmd/neo/main.go:249` | `runAgents` |
| `cmd/neo/main.go:267` | `flushTabwriter` |
| `cmd/neo/main.go:669` | `restoreSessionCWD` |
| `cmd/neo/main.go:893` | `sessionMetadata` |
| `cmd/neo/main.go:906` | `loadSkills` |
| `cmd/neo/provider.go:93` | `Token` |
| `cmd/neo/provider.go:109` | `runLogin` |
| `cmd/neo/provider.go:139` | `runLogout` |
| `cmd/neo/sessions.go:30` | `listSessions` |
| `cmd/neo/sessions.go:63` | `searchSessions` |
| `cmd/neo/subagent.go:37` | `Name` |
| `cmd/neo/doctor.go:146` | `doctorModelCheck` |

`main`、`runLogin`、`runLogout`、`listSessions` 依赖网络或真实终端，不适合直接测；其余是可测的纯逻辑：

- `restoreSessionCWD(cwd string, errOut io.Writer)`（`cmd/neo/main.go:669`）——`cwd == ""` 提前返回与 `os.Chdir` 失败两条路径。
- `sessionMetadata(sess *session.Session) session.Metadata`（`:893`）——`sess == nil` 分支。
- `loadSkills(cfg *config.Config, cwd string, errOut io.Writer) []skills.Skill`（`:906`）——`!cfg.SkillsEnabled() || cwd == ""` 的分支与正常加载。
- `flushTabwriter(w *tabwriter.Writer, errOut io.Writer) int`（`:267`）——用写入后已关闭的 writer 制造错误。
- `doctorModelCheck`（`cmd/neo/doctor.go:146`）——用 httptest 服务器模拟 `/models` 响应。

## 目标

把 `cmd/neo` 覆盖率从 58.8% 提升到 70% 以上，只补可确定性测试的函数，不为需要真实网络/终端的入口写脆弱测试。

## 实现要点

- 现有 `cmd/neo` 测试已用 `httptest`（见 `cmd/neo/customprovider_e2e_test.go`、`provider_test.go`），沿用同样模式。
- `main` 与 `runLogin`/`runLogout` 保持不测；在 issue 关闭时明确说明。
- `initLogging` 若只操作全局 logger，可用临时目录设 `NEO_LOG` 验证文件被创建；若难以隔离则跳过。

## 验收

- `go test ./cmd/neo/ -coverprofile` 的总语句覆盖率 ≥ 70%。
- 上表可测函数不再有 0.0%。
- `go test ./...`、`go vet ./...`、`gofmt -l .` 干净。
