---
id: 94
title: Let script tools accept a bare string argument instead of failing on the object shape
status: in-progress
priority: low
labels:
    - llm
    - feature
relations:
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

#91 的测量里，solo 模式 5 次运行共出现 5 次 `glob` 相关失败，是最高频的错误。实测确认当前行为：

```
tools.glob("*")  -> Error: glob takes an object of named arguments, not string; write glob({...})
```

错误文案本身是对的（那是 #91 加的），但**根源没解决**：单工具模式里同时存在两种调用风格——

- `tools.glob({pattern: "*"})`：对象参数，与 schema 一致；
- `run(["ls", "-la"])`：数组参数，`console.log("x")`：位置参数。

模型在同一个脚本里混用三种形状，于是把 `glob` 也写成 `glob("*")`。日志显示它看到错误后下一次就改对了，但这仍然是一次浪费的调用（5/5 次运行都发生了）。

## 目标

让 `tools.*` 的工具接受"单字符串参数"这种最常见的误写，或在提示词里用一句话消除歧义。二选一，不要把两者都做。

## 实现要点

- **方案 A（推荐，改代码）**：在 `hostTool` 里，若参数是字符串且该工具恰好只有一个必填的字符串字段（如 `grep`/`glob` 的 `pattern`、`read_file` 的 `path`），把它绑定到那个字段。这样 `glob("*")` 直接等价于 `glob({pattern:"*"})`。
  - 需要工具能自报"必填字符串字段"，现有 `Tool` 接口没有这个信息。**不要**为此加宽接口，可在 `Script` 内维护一张明确的映射表（`grep`→`pattern`、`glob`→`pattern`、`read_file`→`path`），与白名单同层，新增工具时必须显式加一行。
  - 保持现有报错：映射表里没有的工具收到字符串时，仍报"takes an object of named arguments"。
- **方案 B（改提示词）**：在 solo 提示词里加一句"tools.* 接受对象，run() 接受数组"。代价是固定字节，且 `TestSoloFloorBudget`（3,500）需要复核。
- **只选一个。** 两处都做会让"何时用哪种形状"出现两个说法，这正是 #87/#89 反复处理的那类漂移。

## 验收标准

- 采用方案 A 时：单测断言 `tools.glob("*")`、`tools.grep("x")`、`tools.read_file(p)` 与对象形式结果一致；断言映射表外的工具收到字符串仍报错；断言返回类型仍是字符串。
- 采用方案 B 时：单测断言 solo 提示词含该句；记录 `TestSoloFloorBudget` 的新字节数并在文档同步。
- 默认模式行为不变（默认模式的 `script` 也走 `hostTool`，需确认不意外改变其语义）。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 实施记录（2026-11-25）

采用方案 A（改代码，不加提示词字节）。

`hostTool` 现在多收一个 `required` 参数，由 `globals` 在注册时显式给出（`read_file`/`write_file`/`edit_file` → `path`，`grep`/`glob` → `pattern`）。参数是字符串时绑定到该字段；是对象时行为不变；其他类型报错并给出正确写法。

这条映射与白名单同层：新增工具必须显式写一个字段名，否则它拒绝字符串参数。

测试覆盖：三种工具的单字符串调用与对象形式结果一致；`write_file("x")` 仍报缺 `content`（两个必填字段，无法猜）；默认模式与 solo 行为相同。
