---
id: 133
title: Measure Go source outlines before adding an index capability
status: in-progress
priority: low
labels:
    - performance
    - tools
created: "2026-11-27"
updated: "2026-11-28"
---

## 背景

Maki 的 `plugins/index/` 用 tree-sitter 返回源码骨架和精确行号，让模型先定位再窄读正文。Maki README 自报的净节省 165 token/轮不是 Neo 的基线。Neo 的 `grep` 加 `read_file` 已能高效处理已知符号；只有大型 Go 文件的未知落点导航可能有增益。

## 实验范围

用 Go 标准库 `go/parser` / `go/token` 做一个只读实验，输出函数、方法和类型的签名或名称及起止行号，不返回实现正文。通过现有 script 路径或离线测试工具验证，先不注册常驻顶层工具，不引入多语言 tree-sitter 或新运行时，保持 CGO_ENABLED=0 的构建约束。可复用 #132 的度量方法。

## 验收标准

- 用可复现 fixture 验证声明的起止行号能用于 `read_file(offset, limit)`，包括多行签名和方法；解析失败时明确报错或回退，不把不完整结果当完整索引。
- 并列比较骨架加窄读与现有 grep 加窄读：至少覆盖未知落点和已知符号两种任务。报告正确率、工具结果体积、请求总数和模型总输入；计入获取骨架的一轮及额外提示开销。
- 只有在目标任务保持正确率且端到端用量下降时才建议引入；没有明显增益则记录结论，不新增默认能力。


## 结果记录（2026-11-28）

完整记录、fixture、骨架工具与逐次原始结果：`docs/measurements/133-go-source-outlines/`，摘要已并入 `docs/performance.md`。

**环境**：`provider: commandcode` / `deepseek/deepseek-v4.1-flash` / `reasoning_effort: low`，code mode + `features.script: true`，二进制 `nightly-20261127-da511a0-2-gcd84837`。2 个 fixture × 2 臂 × 5 次 = 20 次运行。**该后端不报告缓存分项**。

**骨架工具**：独立模块 `outline/`，只用 `go/parser` + `go/token`，输出 `path:start-end<TAB>kind<TAB>name<TAB>signature`，不含函数体。未注册为 Neo 工具，无新依赖，CGO_ENABLED=0。对 670 行的 `processor.go` 输出 2,803 字节 / 28 个声明；整读同一文件是 28,345 字节。

**行号可用性（第一条验收标准）**：`range_test.go` 把每个声明的 `start-end` 切出、重新解析、要求得到同一声明。对仓库自身 1,327 个声明全部通过，含多行签名与方法；解析失败返回错误，绝不返回不完整索引。

**两组唯一变更**：只有提示是否告知 `./fixture/outline` 存在（260 字节，计入成本），任务与判分完全一致。

| fixture | 臂 | 成功率 | requests | 工具结果字节 | 总输入 |
| --- | --- | ---: | ---: | ---: | ---: |
| 已知符号 | grep | 5/5 | 4 (3–6) | 5,340 (1,187–27,025) | 14,302 (6,631–19,804) |
| 已知符号 | outline | 5/5 | 4 (3–7) | **3,543** (2,231–5,518) | **12,474** (8,297–19,474) |
| 未知落点 | grep | 5/5 | **5** (4–6) | 30,957 (5,002–64,988) | **20,217** (12,710–57,312) |
| 未知落点 | outline | 5/5 | 7 (5–8) | **10,304** (8,245–13,068) | 25,081 (15,798–34,490) |

判分 20/20，无任务失败。`unknown:grep` 记录 10 次工具错误（多为整读撞上限），`unknown:outline` 4 次；这是两臂方差的来源之一，不是任务失败。

### 结论：不引入

预注册判据要求"正确率保持 **且** 端到端用量下降"。正确率保持，用量未下降：

- **已知符号**：工具结果字节 −34%、总输入 −13%，但 **requests 中位数不变**（4 vs 4）。只买到字节，没买到往返。
- **未知落点**：requests 中位 5→7、总输入 20,217→25,081，**反向且幅度大于噪声**。骨架需要先 glob 拿文件列表，多出的一轮超过它省下的读取。

索引能力是长期成本：第二条解析路径、默认面上的一个工具、每次请求都要付的提示字节。数据只支持"符号已命名且模型否则会整读文件"这一窄形态，而 grep 已能覆盖，只是不够稳定。

测量确实支持的一点，也是后续可复用的：**行号可信（1,327/1,327 回读通过）**，且收益体现在**稳定性**（8–13 KB 而非 1–65 KB）而非中位数。若将来有"必须先翻过多个大文件才知道哪个相关"的 fixture，骨架是值得重试的形态。

### 复跑

报告写完后另跑 1 次 `verify`（`results/known.outline.verify`，不计入 5 次样本），确认提交后的 harness 从干净状态仍可运行：判分 1、outline 被调用 1 次。该次 8 requests / 74,485 输入，明显高于样本内同臂，再次说明该模型的方差宽，故本记录只声明幅度最大的一项。

### 其他

- fixture 的 Go 源以 `.go.txt` 存放，`run.sh` staging 时改回 `.go`，避免仓库根 `go test ./...` 把它们当 Neo 的包编译。
- `run()` 子调用日志不含 argv，故 `fixture/outline` 是包装脚本，先记 `outline-invocations.log` 再 exec `.outline-bin`；归约器从该文件计数，而不是从答案推断。
