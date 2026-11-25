---
id: 98
title: Fix glob returning doubly-joined paths when Absolute is set on the default code-mode surface
status: open
priority: critical
labels:
    - bug
    - tools
relations:
    blocks:
        - 99
        - 100
    related-to:
        - 95
        - 96
        - 102
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

本轮 codemode 验证用的本地桩服务（`features.codemode: codemode` 默认面，headless `neo run`）实测发现一个可复现缺陷：**`glob` 在 `Absolute: true` 时把每个路径拼接了两次。`**

复现（`/tmp/globcheck` 下有 `a.txt`、`b.txt`、`sub/c.txt`）：

```
Glob{Root: "/tmp/globcheck", Absolute: true}.Run(pattern: "*.txt")
-> "/tmp/globcheck/sub/c.txtsub/c.txt
    /tmp/globcheck/b.txtb.txt
    /tmp/globcheck/a.txta.txt"
```

把结果直接喂回脚本会失败：

```
tools.read_file({path: "/tmp/globcheck/a.txta.txt"})
-> Error: open /tmp/globcheck/a.txta.txt: no such file or directory
```

同一目录下 `Grep{Absolute: true}` 是正确的（`/tmp/globcheck/a.txt:1:needle`）。

## 影响面（已核对代码）

- `cmd/neo/main.go:509`：默认 code mode 的 `script` 固定用 `tools.Glob{Root: root, Absolute: true}`。
- `internal/subagent/runner.go:123`：code-mode 子代理同样用 `Absolute: true`。

也就是说，**默认面唯一可用的文件查找工具在返回不可用的路径**。zero-shot 下模型按提示词"glob 返回一行一个路径"直接 `tools.read_file(paths[0])`，必然踩坑；而它拿到的错误信息（`open ...a.txta.txt`）看起来像"文件不存在"，会误导模型去怀疑 fixture 而不是工具。

## 根因

`internal/tools/search.go:168-171` 对**每一行**调用 `rebaseSearchPath(line, root)`。该函数为 `grep` 的 `path:line:text` 设计：只重写第一个冒号前的 `head`，再拼回 `:tail`。对 `grep` 正确；对 `glob` 的纯路径行，`strings.Cut(line, ":")` 切不出冒号，`rest` 保持整行，于是返回 `filepath.Join(root, head) + rest` = 绝对路径 + 原路径。

## 目标

让 `glob` 在 `Absolute: true` 下返回每行一个**可直接使用**的绝对路径，且不改变 `grep` 的现有输出与默认（`Absolute: false`）行为。

## 实现要点

- 建议在 `runSearch` 里显式区分两种形状，而不是靠猜冒号：给 `runSearch` 传一个"路径行 / path:line:text 行"的指示（例如新增参数或两个小包装函数），`glob` 走纯路径重写、`grep` 走现有的 `path:line:text` 重写。
- 不要用"行里有没有冒号"做判别：Windows 盘符与含冒号文件名都会误判，且 `grep` 的 `text` 里本身可能含冒号。
- `Absolute: false`（classic 面与子代理 inspect 面）必须逐字节不变。
- 保持 `"no matches"` 与 `max_matches` 截断提示行为不变。
- 不要顺手改 `read_file` / `edit_file` 的路径解析（#95 已建立 `CWD` 基准，别打乱）。

## 验收标准

- 单测：`Glob{Root: tmp, Absolute: true}` 每个输出行 `filepath.IsAbs` 为真，且 `strings.HasSuffix` 不等于重复的文件名；最直接地，断言 `os.Stat(line)` 成功。
- 单测（回归）：`Grep{Root: tmp, Absolute: true}` 输出仍为 `path:line:text`，`text` 中含冒号时不被破坏。
- 单测：`Absolute: false` 的 `glob`/`grep` 输出与当前实现一致。
- 单测（端到端形状）：脚本内 `tools.glob({pattern:"*.txt"}).split("\n").map(p => tools.read_file({path:p}))` 在默认 code mode 下成功（这正是提示词教给模型的写法）。
- `go test ./...` 通过；`qlty check` 退出码为 0。
- 合入后在 issue 里记录修复前后的实际输出各一行。

## 备注

本 issue 由 2026-11-25 的 codemode 运行时验证发现，与 #95（相对路径基准）、#96（无结果当值）属于同一类"工具返回值不能直接喂给下一步"的缺口，故 `related-to` 二者。
