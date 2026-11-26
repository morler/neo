---
id: 112
title: Replace the glob tool's ripgrep subprocess with a pure Go walker
status: open
priority: medium
labels:
    - refactor
    - tools
    - performance
    - docs
created: "2026-11-26"
updated: "2026-11-26"
---

## 背景

`rg` 是 Neo 目前**唯一的运行时外部依赖**。代码里只有一个调用点：`internal/tools/search.go:157` 的 `exec.CommandContext(ctx, SearchBinary, args...)`，它同时支撑 `grep` 和 `glob` 两个工具，并通过三条装配路径暴露给模型：

- classic 模式主工具表（`cmd/neo/main.go:511-512`、`561-562`）；
- code mode 的 `script` 白名单（`cmd/neo/main.go:535-536` 传入 `Absolute: true`，`internal/tools/script.go:277-278` 绑定为 `tools.grep` / `tools.glob`）；
- 子代理（`internal/subagent/runner.go:146`、`154-155`）。

代价有两处：

1. **inspect 子代理在缺少 rg 的机器上完全失去搜索能力**——它只有 `read_file`/`grep`/`glob`，没有 shell 可以退回（`docs/developer/tools.md:151-155` 已记录该行为）。
2. 安装与 CI 都要额外装 ripgrep：三份 workflow 各有一段 `apt-get install -y ripgrep`，且因为测试遇到缺 rg 是 `t.Skipf` 而不是失败（`internal/tools/search_test.go:15-16`），漏装等于静默不测。

`docs/developer/tools.md:157` 目前的决策是 **There is no Go fallback**，理由是第二套实现会带来两套 ignore 规则和两种输出形状。本 issue 用实测数据重新评估这个决策，并把范围**收窄到 `glob` 一个工具**，让风险可控。

## 实测依据（2026-11-26）

在临时模块中实际编译并运行了候选库，数据如下。依赖数用 `go list -deps | wc -l`，体积为 `CGO_ENABLED=0 -trimpath -ldflags="-s -w"` 的空程序基线 1.44 MiB 之上的增量。

| 候选 | 用途 | 依赖数 | 体积增量 | 结论 |
| --- | --- | --- | --- | --- |
| `boyter/gocodewalker` v1.5.1 (MIT) | 遍历 + 嵌套 `.gitignore` | 72 | +0.55 MiB | 语义最贴合，推荐 |
| `bmatcuk/doublestar/v4` v4.10.7 (MIT) | 仅 `**` glob 匹配 | 63 | +0.03 MiB | 轻量，但不解决遍历与 ignore |
| `go-git/go-git/v5/.../gitignore` v5.19.3 (Apache-2.0) | 模式匹配 | 89 | +0.46 MiB | 行为正确，但嵌套加载要自己写 |
| `sabhiram/go-gitignore` | 单文件 ignore | 67 | +0.32 MiB | 可用，嵌套要自建 |
| `denormal/go-gitignore` | 仓库级 ignore | — | — | **排除：非 git 仓库下 `Ignore` 直接 panic（实测 SIGSEGV）** |

`gocodewalker` 在 `IncludeHidden = true` + `ExcludeDirectory = []string{".git"}` 下，对构造样本树（根与子目录各一个 `.gitignore`，含 `!special.log`、`build/`、`.github/workflows/ci.yml`、`.env`）的实际输出为：

```
[.env a.txt .gitignore sub/special.log sub/.gitignore .github/workflows/ci.yml]
```

即：嵌套 ignore 生效、`!` 否定生效、`build/` 被排除、隐藏文件保留、`.git/` 被排除——正好等价于现有的 `--hidden --glob !.git/ --no-require-git` 三点语义。它另有 `IgnoreBinaryFiles` 可跳过二进制。

基准（2000 文件 / 20 万行 / 单线程，3 次取最优）：

| 操作 | rg（外部进程） | 纯 Go |
| --- | --- | --- |
| 内容搜索 | 5.2 ms | 15.0–16.5 ms |
| 文件列举 | 3.2 ms | 0.3–0.5 ms |

## 目标

把 `glob` 的实现从 rg 子进程换成纯 Go 遍历，使 `glob` 在**没有任何外部依赖**的机器上可用；`grep` 保持现状继续使用 rg。

## 范围

**做**：`internal/tools/search.go` 中 `Glob.Run` 的查找实现；新增遍历代码与其测试；更新 `docs/developer/tools.md` 与 `THIRD_PARTY_NOTICES.md`。

**不做**：`grep` 的实现、`doctorRipgrepCheck`、CI 里的 ripgrep 安装步骤、`internal/tools/script.go` 的 `tools.glob` 契约。这些在 `grep` 也迁移后才有意义，留给后续 issue。

## 实现要点

- 对外契约必须保持不变：工具名 `glob`、schema（`pattern` / `path` / `max_matches`）、`ParallelSafe` 为 true、每行一个路径、无结果返回字符串 `no matches`（不是错误）。
- `Absolute: true` 时仍要走现有的 `rebaseSearchPath(..., searchLinesPaths, ...)` 语义，保证 code mode 下路径能喂给 `read_file`。
- 必须保留：`max_matches` 默认 200、超过上限时追加现有的截断提示行、`context` 取消后返回错误且不返回部分结果、单行长度受 `MaxOutputBytes` 约束。
- 遍历需显式设置 `IncludeHidden = true` 与 `ExcludeDirectory = []string{".git"}`，这两项是默认值之外的行为，缺一个就会与今天的结果不一致。
- `pattern` 语义需与 ripgrep 的 `--glob` 对齐，至少覆盖 `**/*.go`、`*.txt`、`src/**/test_*.py`；建议用 `doublestar.Match` 做匹配，不要自己写通配。
- 依赖须纯 Go（`CGO_ENABLED=0` 可构建）、许可证与 MIT 兼容，并按 `docs/performance.md:40` 的要求在提交前重测体积。

## 验收标准

- `t.Setenv("PATH", "")` 后调用 `Glob.Run` 仍返回正确路径列表（证明不再依赖 rg）；同一条件下 `Grep.Run` 返回现有的 `errNoRipgrep` 文案。
- 新增测试覆盖：嵌套 `.gitignore`、`!` 否定规则、隐藏文件被包含、`.git/` 被排除、`build/` 这类目录排除、`**` 与 `*` 的差异、`max_matches` 截断提示、`context` 取消无部分输出。
- 现有 `internal/tools/` 下与 `Glob` 相关的用例全部通过，尤其是 `search_test.go` 里覆盖 classic / code mode / 子代理三种面孔的输出形状断言。
- `go test ./...` 通过，`qlty check` 退出码为 0。
- `just performance` 通过，并在该 issue 的完成记录里写明迁移前后的 `stripped_binary_bytes`。
- `docs/developer/tools.md` 的 Search 一节区分 `glob`（纯 Go）与 `grep`（仍用 rg），并更新原来的 There is no Go fallback 段落，避免文档与实现相反。

## 已知风险

- 两套实现的输出形状从此并存一段时间，这正是原文档拒绝 fallback 的理由。缓解方式是本 issue 只迁移 `glob` 并保留全部既有断言，让 `grep` 在后续 issue 里按同样步骤收口。
- 正则方言差异本阶段不存在（`glob` 不做内容匹配），这是先做 `glob` 的主要原因。
- `gocodewalker` 不读 `.git/info/exclude` 与全局 `core.excludesFile`，而 rg 支持。若判定这一差异不可接受，需要在本 issue 里记录并升级为阻塞项。

## 后续（另开 issue，不在本 issue 内做）

`grep` 迁移到 `gocodewalker` + 标准库 `regexp`：届时才需要处理 `searchExitError` 的退出码协议与 `isNoFilesSearched` 对 rg stderr 原文的依赖，并同步修改 `TestGrepStillReportsRealErrors`。两者都完成后才能删掉 `doctorRipgrepCheck` 与 CI 的安装步骤。
