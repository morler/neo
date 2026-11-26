---
id: 110
title: Add a raw read_file mode so script reads can feed edit_file directly
status: closed
priority: medium
labels:
    - tools
    - prompting
created: "2026-11-26"
updated: "2026-11-26"
closed: "2026-11-26"
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

## 实现记录（2026-11-26）

已按 `raw` 出口方案落地，未采纳 hashline（理由见上）。

### 改动

- `internal/tools/fs.go`：`ReadFile.Spec()` 增加 `raw` boolean 属性；描述改为"raw: true drops that prefix so the text feeds edit_file directly"。`readFileWindow` 增收 `raw bool`，`windowScanner` 增 `raw` 字段，`consume` 在 `raw` 为真时不写 `lineNumberPrefix`。**默认输出逐字节不变。**
- `internal/tools/tool.go`：新增 `optBool`，只接受真 bool——字符串 `"true"` 视为未设置，与 `optInt` 拒绝非数值字符串同一原则。
- `internal/tools/script.go`：code 模式描述加一句 `read_file({raw:true}) drops line numbers for edit_file.`（该模式下 read_file schema 不可见，故写在这里）。
- `cmd/neo/main.go`：`codeModeCapability` 的"构造唯一窗口"片段由手工 `replace(/^\s*\d+\t/, "")` 改为直接 `raw: true`，**这一步反而省了字节**。

### 预算（实测，未抬门限）

| 面 | 改动前 | 改动后 | 预算 |
| --- | ---: | ---: | ---: |
| fixed floor | 8,084 | **8,242** | 8,500 |
| script floor | 9,796 | **9,954** | 10,000 |
| code-mode floor | 3,379 | **3,421** | 3,500 |

第一次改动曾把 script floor 顶到 10,015（超 15 B）与 10,045（超 45 B）。处理方式不是抬预算，而是：classic 模式下 `read_file` 是顶层工具、其 schema 本身可见，因此把说明从 script schema 里移走，只保留 code 模式那一句，并把示例改成更短的 `raw` 形式。

### 测试

- `TestReadFile_RawOmitsGutter`、`TestReadFile_RawMatchesGutterStripped`（raw 必须等于"默认输出剥掉前缀"）、`TestReadFile_RawHonorsOffsetLimit`、`TestReadFile_RawIgnoresNonBooleanFlag`、`TestReadFile_RawPreservesCRLFAndMissingNewline`、`TestReadFile_RawAbsentKeepsTheGutter`。
- 端到端：`TestScriptRawReadFeedsEditFile`（脚本内 raw 读→直接 edit 成功），`TestScriptGutterReadStillFailsEditFile`（对照：默认读仍不能让 edit_file 直接用，防止契约悄悄漂回）。
- 提示词守卫：`TestCodeModeWindowExampleRuns` 随示例形状更新并通过。
- `go test ./...`、`go vet ./...`、`go test ./cmd/neo -run Floor -v` 全绿；`qlty check` 退出码 0。

### 文档

`docs/developer/tools.md`（read_file 行）、`docs/performance.md`、`docs/design/token-budget.md`（固定开销数字）、`CHANGELOG.md`（用户可见变更 + 实测数据来源）均已同步。

### 判据复核

1. `read_file({raw:true})` 输出可直接喂 `edit_file` 且成功 —— 由 `TestScriptRawReadFeedsEditFile` 与 CRLF 用例证明。
2. 默认路径逐字节不变 —— 由 `TestReadFile_RawAbsentKeepsTheGutter` 与既有 `TestReadFile_Basic` 等锁住。
3. 合并率作为观察项：本次未做新的 A/B 测量，沿用 `docs/performance.md` 的既有基线，符合 issue 判据中"不作成败判据"的约定。

## 复核：关闭 codemode 后的影响（2026-11-26）

用户提问"关闭 codemode 后这个改动不会影响正常使用吗"，据此补充实测验证。

先说清一个容易混淆的点：neo 有**两个不同**的开关。

- `features.codemode`（`/--codemode=`）切换**工具面**：`codemode`（默认，只有 script）或 `classic`（bash + read_file + write_file + edit_file + grep + glob）。
- `features.script`（`/codemode on|off`）切换**script 工具是否注册**，不切换工具面。

所以 chat 里输入 `/codemode off` 实际改的是 `features.script`（`cmd/neo/main.go:1382` 的 `SetScript`），不是工具面。

### 四种工具面实测（`TestReadFileRawOnEverySurface`）

| 工具面 | 注册的工具 | raw 可达路径 | 结果 |
| --- | --- | --- | --- |
| code-mode / script on | `script` | `tools.read_file({raw:true})` | 通过 |
| code-mode / script off | 无 | 无 read_file，自然不涉及 | 通过（断言无该面） |
| classic / script on | bash, edit_file, glob, grep, read_file, script, write_file | 顶层 `read_file` 的 schema，加上 script 内 | 通过 |
| classic / script off | bash, edit_file, glob, grep, read_file, write_file | 顶层 `read_file` 的 schema | 通过 |

**结论：不影响正常使用。** `raw` 是纯增量参数：

- 不传 `raw` 时输出逐字节不变（`TestReadFile_RawAbsentKeepsTheGutter` 锁住）。
- classic 工具面里 `read_file` 是顶层工具，其 schema 已声明 `raw`，模型按需使用；不用就完全不受影响。
- code-mode 下 `read_file` 只在 script 内可见，故那一句说明写在 `codeModeSpec` 里。
- code-mode + script off 本就是"只有协调工具"的退化配置（`neo run` 对此直接拒绝启动），与本次改动无关。

### 提示词不串味

classic 模式的 chat prompt **不含** code-mode 那句 `raw:true` 说明（`codeModeSpec` 仅在 `Script.CodeMode` 为真时使用，`internal/tools/script.go:134-137`），已用临时测试验证通过。因此关闭 codemode 后，提示词与工具面都和改动前一致，唯一差异是 `read_file` 多了一个可选参数。

### 验证命令

`go test ./...`（exit 0）、`go vet ./...`、`gofmt -l cmd internal`（干净）、`qlty check`（exit 0）全部通过。新增测试 `TestReadFileRawOnEverySurface`（`cmd/neo/readfile_raw_test.go`）把上表四种工具面固化为回归保护。
