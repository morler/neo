---
id: 95
title: Resolve script file-tool paths against the script's working directory
status: closed
priority: medium
labels:
    - bug
relations:
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

#91 发现 `grep`/`glob` 在 `Root` 目录下运行、返回相对路径，而脚本内没有 `cd`，于是相对路径喂不进 `read_file`。修法是在 solo 模式给 `Grep`/`Glob` 加 `Absolute` 开关。但同一类问题的另一半没修：**`read_file`/`write_file`/`edit_file` 的相对路径按进程 cwd 解析，不按脚本的工作目录。**

实测（solo，`CWD` 与 `Root` 都指向临时目录）：

```
tools.read_file({path:"./nonexistent"}) -> Error: open ./nonexistent: no such file or directory
```

这里的 `./` 是相对**进程**目录，不是 `Script.CWD`。因此：

- solo 模式必须用绝对路径（提示词里的示例也确实这么写），模型一旦写相对路径就会失败；
- 而 `run()` 里的程序是在 `CWD` 下运行的，两种动作对"相对哪里"的理解不一致。

## 目标

让脚本内的文件工具与 `run()` 对相对路径的解析基准一致。

## 实现要点

- 在 `Script` 内、调用 `Read`/`Write`/`Edit` 之前，把相对路径按 `s.CWD` 解析成绝对路径；`CWD` 为空时保持现状（进程 cwd），以便默认模式行为不变。
- **只改路径解析，不改工具本身**：`internal/tools/fs.go` 的 `ReadFile`/`WriteFile`/`EditFile` 不应获得新字段，否则默认模式的顶层调用也会被改变。
- `edit_file` 的 stale-read 检查依赖路径字符串作为键（`fileState.record`）。改解析处时，必须让"读到"和"改到"使用**同一个**解析后路径，否则读过的文件会被判为未读。
- 绝对路径与 `..` 逃逸不在本任务范围：`permissions.md` 已说明路径包含由环境负责，本任务只统一基准。
- 若判断该改动会影响默认模式，则改为只在 `s.Solo` 时解析，并在 issue 中记录这一限制。

## 验收标准

- 单测：`CWD` 设为临时目录，脚本内 `tools.read_file({path:"./a.txt"})` 能读到 `CWD/a.txt`，`tools.write_file({path:"./b.txt"})` 写到 `CWD/b.txt`。
- 单测：`edit_file` 用相对路径修改前先 `read_file` 同一相对路径，不触发 stale-read 错误。
- 单测：`CWD` 为空时行为与改动前一致（相对进程 cwd）。
- `go test ./...` 通过；`qlty check` 退出码为 0。
