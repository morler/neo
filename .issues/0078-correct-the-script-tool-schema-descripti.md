---
id: 78
title: Correct the script tool schema description so it matches the exposed tools
status: in-progress
priority: high
labels:
    - bug
    - feature
relations:
    blocks:
        - 79
    related-to:
        - 81
created: "2026-11-24"
updated: "2026-11-24"
---

## 背景

`script` 的 schema 描述与实际暴露的能力不一致，且从创建起就未更新：

- 描述（`internal/tools/script.go:99-103`）写 `Available inside the script: tools.read_file({path, offset?, limit?}) -> string. Nothing else is defined; there is no bash, no write, and no import.`
- 实际 `globals`（`internal/tools/script.go:207-215`）暴露 `read_file`、`grep`、`glob`、`write_file`、`edit_file` 五个。
- `git log -S'no write, and no import' -- internal/tools/script.go` 只有 `55f5330` 命中；`e7e26ba feat(tools): let a script write` 加入写入能力后未同步描述。

后果：模型从 schema 得出的结论是“脚本只能只读探测”，而读-改-验是它的主工作流，于是合理绕开该工具。同一时刻系统提示（`cmd/neo/main.go:104-113`）却列出全部五个工具，两个信号源互相矛盾，模型按最保守的解读行动。这与 `docs/performance.md:95` 记录的“三次失败调用才发现调用方式”是同一问题的两面。

## 目标

`Spec()` 成为脚本能力的唯一事实来源，描述与实际 `globals` 逐项一致。

## 实现要点

- 描述中列出全部五个宿主函数，删除 “no write” 这一错误断言。
- 保留两条真实约束：无 `bash`、无 `import`/网络（依据 `docs/design/js-batch-tool.md` §2）。
- 注明返回形态：五个函数都返回字符串；`grep` 返回 `path:line:text` 行文本（`internal/tools/search.go:64`），不是数组——这是模型最容易踩空的点。
- 保留既有说明：同步执行，最后一条表达式即结果，字符串原样返回，其余值 JSON 序列化。
- 净字节应减少或持平，记录改动前后的 schema 字节数。

## 验收标准

- `Script.Spec().Description` 中出现的 `tools.<name>` 集合与 `globals` 的键集合完全一致。
- 新增单测锁定该一致性（从描述中提取 `tools.<name>` 与 `globals` 键比对），防止再次漂移。
- `TestScriptFloorBudget` 与 `TestFixedPromptFloorBudget` 通过；`docs/performance.md:73` 的 704 B 若变化则同步更新。
- `go test ./internal/tools ./cmd/neo` 通过。
