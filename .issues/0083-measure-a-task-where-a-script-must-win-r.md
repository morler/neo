---
id: 83
title: 'Measure a task where a script must win: read, branch on content, then edit'
status: closed
priority: high
labels:
    - performance
relations:
    blocks:
        - 84
        - 85
        - 86
        - 90
    related-to:
        - 91
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---

## 背景

`docs/performance.md:109-130` 与 #80 的三任务集证明了一件事：模型只在“统计 TODO”这一种形状上选择 `script`（3/3），而“条件改写”（0/3）和“大文件定位”（0/3）从不选择。但这两个任务本身就是**脚本该赢**的形状：

- 条件改写 = 读一个文件 → 判断内容 → 改写，正是“后一步的参数取决于前一步的结果”。
- 大文件定位 = 读 2,001 行 → 只留一行。

两者都是 0/3 且 `requests` 未变化（4→4、3→3），说明模型在这些任务上选择了 `bash`/`grep`，而脚本能省掉的往返一次也没省。在不知道这个“该赢却没赢”的差距有多大之前，任何提示词改动都无法判断是否有效。

同时 #80 已记录：同一版本内 input 的跨度（8,135–9,498）大于版本间差异，单次采样不可信。本任务也是给后续改动提供噪声底。

## 目标

构造一个脚本有明确收益的任务，取得可复现的基线数字，作为 #84/#86/#87 的对照。

## 实现要点

- 任务集沿用 #75/#76 的 cwd 与文件布局，新增任务 4：
  `Read the line containing the unique needle marker in big.txt, change only the word immediately after it to UPDATED, and write the file back. Do not touch any other line.`
- 必须在意的场景：`read_file` 一次返回 2,001 行（约 40 KB）会留在上下文里并在后续每个请求重发；脚本内 `read_file` + 判断 + `edit_file` 只把一行结果留在对话里。
- 对照组：`features.script: false` 与 `true` 各跑 3 次，取中位数。同一 provider、同一模型、同一 cwd、任务原文逐字相同。
- 记录：`requests`、`usage.input_tokens`、`usage.cache_read_tokens`、`script` 调用次数（取 `NEO_LOG` 的 `"name":"script"`，不从最终回答推断）、以及**大文件内容是否进入上下文**（定性：`read_file` 是否在顶层被调用）。
- 必须先跑本任务再改提示词，否则改动前后的数据不可比。
- 若 `neo run --json` 缺字段，先记录缺失，不临时改 CLI。

## 验收标准

- issue 内保存：命令、cwd、provider/model、构建方式（commit）、任务 4 原文、每组的 3 次原始数字与中位数。
- 明确写出脚本在该任务上是否减少了 `requests` 或 `input_tokens`，以及“大文件内容是否仍在上下文”这一条。
- 报告 `script` 调用率（N / 3）与同一版本内的数值跨度，作为后续判定的噪声底。
- 不修改产品代码。

## 实测结果（2026-11-25）

环境：

- cwd `/tmp/measure/ws4`，fixture：`src/{a,b,c}.txt` 各 1 行 TODO；`big.txt` 2001 行，第 1501 行 `prefix needle-7f3a PLACEHOLDER suffix`，前后各一行 context。
- provider/model：`commandcode` / `deepseek/deepseek-v4.1-flash`（与 #75/#76 同一模型）。
- 二进制：`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=measure-off"`，提交 `f986d64`。**同一个二进制**跑两组，只改 `neo.yaml` 的 `features.script`，因此两组差异只来自工具是否注册。
- 每组跑 3 次；`on` 组因首次结果离散（下面说明），补跑 3 次，共 6 次。
- `script` 调用次数取自 `NEO_LOG` 的 `"msg":"tool call"` 记录中的 `name`，不从最终回答推断。
- 每次运行前用备份恢复 `big.txt`；每次运行后检查第 1–1500 与 1502–2001 行与备份逐字节相同。

任务 4 原文（逐字）：

```
Read the line containing the unique needle marker in big.txt, change only the word immediately after it to UPDATED, and write the file back. Do not touch any other line.
```

### 原始数据

| 版本 | 运行 | requests | input_tokens | cache_read | script 调用 | read_file 调用 | 其余行未变 | 第 1501 行 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- | --- |
| off | r1 | 6 | 15,088 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| off | r2 | 5 | 11,217 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| off | r3 | 5 | 11,331 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| on | r1 | 6 | 16,243 | 0 | 2 | 0 | 是 | `… UPDATED suffix` |
| on | r2 | 6 | 16,745 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| on | r3 | 5 | 13,768 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| on | r4 | 8 | 26,813 | 0 | 4 | 0 | 是 | `… UPDATED suffix` |
| on | r5 | 6 | 17,421 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |
| on | r6 | 6 | 16,792 | 0 | 0 | 0 | 是 | `… UPDATED suffix` |

### 中位数

| 指标 | off（n=3） | on（n=6） | 变化 |
| --- | ---: | ---: | --- |
| requests | 5 | 6 | **+1** |
| input_tokens | 11,331 | 16,769 | **+48%** |
| 选择 `script` 的运行 | 0 / 3 | **2 / 6** | — |
| 顶层 `read_file` 调用 | 0 | 0 | — |

### 结论：本任务上脚本没有赢，而且原因不在提示词

三条观察，按重要性排序：

1. **模型从不用 `read_file` 读大文件。** 九个运行里 `read_file` 调用次数全为 0。它用 `sed -n '1499,1503p'`、`grep -n`、`sed -i '1501s/…/…/'` 这类命令把窗口自己收窄。`docs/design/js-batch-tool.md` §1 的成本模型假设“读者把整份文件送进上下文再判断”，而一个会用 `sed` 收窄窗口的模型根本不会触发这个成本。**脚本要竞争的对象不是“把 2001 行读回来”，而是“一条已经收窄的 bash 命令”。** 这是本任务最重要的发现，也解释了 #76/#80 里 `requests` 为何从不下降。

2. **依赖形状确实提高了选用率，但幅度不足以抵消成本。** `on` 组 6 次里 2 次写了脚本（33%），而 #80 的条件改写任务是 0/3。这与 #84 的假设方向一致，但其中一次用了 4 次脚本调用，把 `requests` 推到 8，input 推到 26,813。

3. **被选中的脚本只做了 `grep`。** `on` r1 的脚本是 `tools.grep({pattern:"NEEDLE|needle", path:"big.txt", max_matches:20})`，随后仍在顶层调 `edit_file`，读-判断-写没有合并进一个脚本。也就是说：模型用脚本替代了一条 `grep`，而不是替代一次往返。这直接支持 #85（失败/约定可发现性）与 #87（决策点提示），而不支持继续加大系统提示里的篇幅。

### 对后续任务的影响

- **#84 仍然值得做**，但理由要改：不是为了“提高选用率”，而是删掉一条已被证明对脚本不利的判据（“run the same check across many files”）。本任务没有测到该形状，但 `hostTool` 是同步宿主调用这一点没有变。
- **#86 的判据需要修正**：本任务的噪声底是 `on` 组 requests 5–8、input 13,768–26,813，跨度远大于任何文案改动能带来的差异。在 `deepseek-v4.1-flash` 上以 n=3 复测无法判定“保留/收窄”。#86 要么提高采样次数，要么换一个不靠随机性的指标（例如“被选中的脚本是否合并了读-判断-写”这一布尔量）。
- **未验证**：一个真正无法用 bash 一条命令完成的任务（例如逐文件按内容分支后批量改写 N 个文件）。本任务的 fixture 太小，`sed` 一次就解决。

### 顺带记录的一处文档错误

#80 写的“固定成本（1,455 B ≈ 2,000 tokens）”把字节当成了 token。1,455 字节按 4 字符/token 约 364 token。这不影响 #80 的定性结论，但后续预算换算不应沿用该数字。
