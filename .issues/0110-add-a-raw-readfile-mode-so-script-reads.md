---
id: 110
title: Add a raw read_file mode so script reads can feed edit_file directly
status: open
priority: medium
labels:
    - tools
    - prompting
created: "2026-11-26"
updated: "2026-11-26"
---

## 背景

code 模式（codemode）下，"读→判断→写"放进同一次脚本调用本是设计意图（`cmd/neo/main.go` 的 `codeModeCapability` 明确要求"read, decide, and write inside the same call"），但实测合并率长期偏低，根因不是模型意愿、不是提示词、也不是任务类型，而是**读写文本契约不一致**：

- `read_file` 每行加 `%6d\t` 行号前缀（`internal/tools/fs.go:132-137` 的 `lineNumberPrefix`），设计上"匹配 grep 报的行号"。
- `edit_file` 的 `old_string` 必须匹配**文件原始文本**（`internal/tools/fs.go:346-358` 的 Description 甚至要求"Strip the line-number prefix read_file adds"）。

于是脚本里最自然的写法必然失败：

```js
const text = tools.read_file({path});
tools.edit_file({path, old_string: <从 text 里取出的行>});  // old_string not found
```

模型被迫多付一步"剥行号 + 构造唯一窗口"的心智转换，合并从原子动作变成两步。

### 实测证据（本机 `~/.neo/sessions`，85 个会话）

- 218 次含子调用的 `script` 调用中，合并（同一调用内既有读取类又有写入类子调用）45 次，**20.6%**；主流形态仍是 `split read_file` 72 次、`split grep+read_file` 27 次。
- `edit_file` 相关结果 145 条：`old_string not found` **12 次**、`found N times` **6 次**。
- 模型已自行绕过：会话 `7870d747` 里出现 `cfg.split("\n").map(l=>l.replace(/^\s*\d+\t/,""))`；`bb717db6` 里大量 `slice(...).map((l,i)=>(96+i)+"\t"+l)` 手工重排。

### 为什么不用 hashline（已评估并否决）

hashline（读时给每行附内容哈希、编辑时按哈希定位）解的是"并发改动下的行级精确锚定"，而 neo 的 `fileState.changedSinceRead`（`fs.go:23-52`，modTime+size）已覆盖陈旧检测；hashline 不消除"读→写"中间的转换，反而把协议变复杂，并违反 `docs/design/token-budget.md` 规则 1（稳定缓存前缀）与 issue #100 的决定"不要改 edit_file 的匹配语义、不要放宽必须唯一"。同类收益用 `raw` 出口即可拿到，成本小一个数量级。

## 目标

给 `read_file` 增加一个可选的 `raw` 出口，让它返回**不带行号前缀的原文**，使脚本能把读到的文本直接作为 `edit_file` 的 `old_string`，从而把"读→写"从"读 + 手工去行号 + 写"还原成一个原子动作。

**不改** `edit_file` 的匹配语义（仍是唯一匹配、缺失报错），**不改** `read_file` 默认输出（不带 `raw` 时行为与今天逐字节一致，行号前缀保留）。

## 实现要点

1. **参数**：`ReadFile.Spec()` 的 `properties` 增加 `raw`（`{"type":"boolean","description":"..."}`），不加入 `required`。`ReadFile.Run` 用新 helper 读取（参照 `tool.go:96-135` 的 `optString`/`optInt` 增加 `optBool`）。
2. **传递**：`readFileWindow(ctx, path, offset, limit)` 改为多收一个 `raw bool`，或加 `readFileWindowOptions` 结构；`windowScanner` 增加 `raw` 字段，在 `consume` 中当 `raw` 为真时**不写** `lineNumberPrefix`。
3. **字节预算不变**：`appendReadFileChunk`（`fs.go:271-277`）继续以 `MaxReadBytes` 为准；raw 模式下每行少写前缀，同一字节上限能容纳更多行，这是收益，不是行为变化。
4. **截断建议**：`tooLargeAdvice`（`fs.go:291-300`）生成的重试调用建议保持不变；若采用 raw，重试建议里**不要**凭空加 `raw`（模型自己会带上），避免多一个状态。
5. **策略文档同步**：`ReadFile.Spec()` 的 Description 追加一句说明 `raw` 的用途与"默认带行号是为了对齐 grep"；`script.go` 两处描述（默认 141-143 行、code mode `codeModeSpec` 576-585 行）各加一句"read_file 传 raw: true 可拿到可直接用于 edit_file 的原文"。
6. **提示词**：`cmd/neo/main.go` 的 `codeModeCapability` 增补 2-3 行示例，展示 `const raw = tools.read_file({path, raw: true})` 后直接 `tools.edit_file({path, old_string: raw, new_string})`。**只加示例，不重写判断句**（#97/#101 已证明加文字改变不了模型行为，除非契约本身变简单）。
7. **不要**做的事：不改 `edit_file` 的匹配/唯一性语义；不让 `edit_file` 自动剥行号（那是 #100 明确不要的方向）；不引入哈希定位；不动 `fileState`。

## 验收标准

- `go test ./...` 通过；`qlty check` 退出码为 0。
- 新单测：`TestReadFile_RawOmitsGutter`（raw 输出无 `%6d\t` 前缀，且与默认输出在剥掉前缀后逐行一致）；`TestReadFile_RawHonorsOffsetLimit`；`TestReadFile_DefaultsToGutter`（默认行为不变，锁住既有 `TestReadFile_Basic`）。
- 端到端单测：一个脚本内 `read_file({path, raw:true})` 的返回值直接作为 `edit_file` 的 `old_string` 能成功（用 `script_test.go` 的既有测试骨架）。
- 预算门限：`TestFixedPromptFloorBudget`（8,500）、`TestScriptFloorBudget`（10,000）、`TestCodeModeFloorBudget`（3,500）三个测试仍通过；若提示词增长，同步更新 `docs/performance.md` 与 `docs/design/token-budget.md` 的固定开销数字。当前余量：script 9,796/10,000（204 B）、code-mode 3,379/3,500（121 B）、fixed 8,084/8,500（416 B）。**code-mode 余量仅 121 B，示例必须极短，否则需先腾预算。**
- `docs/developer/tools.md` 与 `docs/developer/config.md` 若有工具描述表，同步 `read_file` 的 `raw` 选项。
- `CHANGELOG.md` 增加一条用户可见变更记录。

## 判据

沿用 `docs/performance.md` 的测量协议，但本任务的**主判据是契约可用性而非合并率数字**：

1. `read_file({path, raw:true})` 的输出能直接喂给 `edit_file` 且成功（单测证明）。
2. 默认路径逐字节不变（既有测试证明）。
3. 合并率的变化作为**观察项记录**，不作成败判据（#86 已证明 n=3 分辨不出 token 与请求数差异）。

## 备注

- 与 #100 的关系：本任务**不**修改 `edit_file` 的匹配语义，只是给读取侧多一个出口，因此不违背 #100 的约束；#100 交付的"构造唯一窗口"示例仍有效，且 raw 出口能让那一步更直接。
- 与 #83/#86/#91/#97/#101 的关系：这些结论一致指向"提示词不是症结"，本任务是把那个结论落到契约层面。
- 若实测发现 raw 出口并未提高合并率，结论应如实写入本 issue，并按 #91 的口径判定"不改工具面、补能力已到尽头"，不要再开新的提示词任务。
