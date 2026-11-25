---
id: 100
title: Teach the script path a one-pass read-locate-edit shape instead of a retry loop
status: open
priority: medium
labels:
    - tools
    - prompting
relations:
    depends-on:
        - 98
    related-to:
        - 85
        - 84
        - 101
created: "2026-11-25"
updated: "2026-11-25"
---

## 背景

把 `read_file` 的带行号输出直接喂给 `edit_file` 会失败：

```
const text = tools.read_file({path});   // 每一行是 "   123\tcode"
tools.edit_file({path, old_string: "func main()"});
-> Error: edit_file: old_string not found in <path>; read the file and retry with exact text
```

`editFileHint`（`internal/tools/script.go:467-472`）**已经覆盖了这一条**，且 2026-11-25 用临时测试实测确认它在脚本路径上确实触发（`hostTool` 把工具错误包成异常后仍会经 `describeScriptError`）：

```
script threw: Error: edit_file: old_string not found in /tmp/.../a.txt; read the file and
retry with exact text; if old_string came from read_file, strip the leading line number
and tab it adds, since edit_file matches the file's raw text
```

所以这条缺口**不需要再修**，本任务只处理它的另一半：

真正还没覆盖的是**同一类错误的另一个方向**：

```
tools.edit_file({path, old_string, new_string})   // old_string 定位到了 3 处
-> Error: edit_file: old_string found 3 times in <path>; include more surrounding text so it is unique
```

这条消息是对的，但它让模型**再读一遍文件、再猜一次上下文**。在脚本里，正确做法是根本不需要它猜：读出内容、按行裁剪出唯一窗口、再写。也就是说，这里缺的不是错误文案，而是**提示词里少了一个"如何构造 unique old_string"的可复制形状**。

## 目标

在零新增字节与一条新示例之间做取舍，让"读→定位→改"在脚本内一次成型，而不是读一次、改失败一次、再读一次。

## 实现要点

- 预算余量（2026-11-25 实测 `go test ./cmd/neo -run 'Test.*Floor.*' -v`）：code mode **3,075 / 3,500（余 425 B）**，script **9,796 / 10,000（余 204 B）**，默认 floor 8,084 / 8,500。余量足够容纳一条 2–3 行示例（约 120–180 B），因此**优先方案 A**；若最终示例超过余量，退到方案 B，不要顶破预算。
- 方案 A（推荐，余量已确认足够）：在 `codeModeCapability` 现有的"读、判断、写"示例后补 2–3 行，展示从 `read_file` 带行号输出中切出 raw 文本：
  ```js
  const line = tools.read_file({path}).split("\n").find(l => l.includes("marker"));
  const raw = line.replace(/^\s*\d+\t/, "");
  ```
  只加这一句，不增加新的章节；`TestScriptExampleRuns` 需扩展到能跑它。
- 方案 B（仅当示例超出余量时）：不碰提示词，只在 `describeScriptError` 的 `found %d times` 分支追加一句"在脚本里可以直接从 read_file 的输出构造唯一窗口"，并给出 1 行形式。成本只在该次失败时支付。
- 不要两处都做（#94 已确立"只选一个，否则出现两种说法"）。
- 不要改 `edit_file` 的匹配语义（`internal/tools/fs.go:386-392`），也不要放宽"必须唯一"。

## 验收标准

- 采用方案 A 时：`TestScriptExampleRuns` 覆盖新示例；`TestScriptFloorBudget`/`TestCodeModeFloorBudget` 若增长，同步更新 `docs/performance.md` 的固定开销小节与对应常量。
- 采用方案 B 时：单测断言 `found 3 times` 的错误消息含构造唯一窗口的提示；三个 floor 预算字节数不变。
- 无论哪个方案：`old_string not found` 的既有提示保持不变（`editFileHint` 的现有测试继续通过）。
- `go test ./...` 通过；`qlty check` 退出码为 0。

## 备注

与 #85 的关系是"补完同一原则的剩余分支"，不是重复：#85 做了 `await` 与 `old_string not found`，本任务处理"匹配到多处"这一分支，或在预算允许时用示例从根上避免它。与 #84 的关系：`codeModeCapability` 是 #89 之后新增的文本，本任务只在其后追加，不重写判断句。
