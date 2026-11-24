---
id: 69
title: Render script sub-calls under their parent row without layout shift
status: open
priority: high
labels:
    - tui
relations:
    blocks:
        - 70
    depends-on:
        - 68
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

原 #59 的第 2 步。事件已经产生（#68），这一步让它们出现在终端里。`docs/parallel-execution-ui/design.md` 已确立硬约束：组内行必须在开始时**一次性占位**，完成时只更新字形与耗时，不得改变行高或顺序。

## 目标

TUI 把 `GroupID` 等于某个 `script` 调用 `ToolUseID` 的工具事件渲染成父行下的子行；简洁模式下默认折叠，父行失败时展开失败的子行。

## 实现要点

- `internal/tui/model.go` 的 `handleToolCallEvent`/`handleToolResultEvent`：当 `GroupID` 指向一个已存在的 `script` 父块时，把子行挂到该父块下，而不是新开顶层块。复用并行组已有的分组查找逻辑。
- **占位策略**：脚本内调用是**逐个**发生的（不像并行组那样一次声明全部）。`docs/parallel-execution-ui/design.md` 的“组开始时一次性占位”约束针对的是**已知成员**的并行组，这里成员未知，不能照搬。选择做法 (b)：子行逐条追加，父块在执行期间允许增长，但**每次追加都不得移动已渲染的行**（只在父块末尾增长）。在注释里写明为何不采用预先占位。
- 简洁模式：父行显示脚本摘要（例如 `Script (5 calls)`），子行默认隐藏；父行失败时展开第一个失败子行。
- 不新增面板、边框或颜色；沿用 `styTool`/`styOK`/`styErr`/`styDim`。
- 窄终端（40 列）截断而非横向滚动。

## 验收标准

- TUI 测试：脚本内 3 次调用在父行下显示 3 个子行，顺序与执行顺序一致。
- 快照测试（40/80/120 列、去色）：子行出现与完成都不改变已渲染行的位置；composer 与状态行不动。
- 单测：父行失败时，失败子行可见；成功子行在简洁模式不可见。
- 单测：两个并行脚本的子行互不串行归属。
- `go test ./internal/tui` 与 `go test -race ./internal/tui` 通过。
- `docs/parallel-execution-ui/design.md` 补充“脚本子调用”一节，说明折叠规则与占位策略。
