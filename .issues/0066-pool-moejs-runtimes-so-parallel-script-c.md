---
id: 66
title: Pool moejs runtimes so parallel script calls stay isolated
status: wontfix
priority: high
labels:
    - feature
created: "2026-11-24"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

第 2 步（原 #58 的一部分）。`moejs.Runtime` 明确要求一次只能被一个 goroutine 使用，但 Neo 的并行调度默认 8 并发。裸用单次 Runtime 在并发下会产生 `ErrForeign` 或数据竞争。

## 目标

加一个 Runtime 池，使多个 `script` 调用并行时各用独立运行时，且脚本之间不共享状态。

## 实现要点

- 实现最小池：`idle chan *moejs.Runtime` + 容量上限；取不到时新建（受上限约束），用完 reset 后归还。
- 归还条件：文档要求 `Interrupt` 返回后才能回池。此步还没有 Interrupt，但**结构上要预留**：归还路径与中断路径分开，避免第 4 步补超时时重写。
- 每次取用前把工具全局重新绑定，确保上一个脚本的改动不残留（运行时是同一全局环境）。
- 并发分类：`Script` 实现 `tools.ParallelTool` 并**恒返回 false**（脚本可读文件、行为不确定，fail closed 到串行）。在注释里写明理由，以及“改判为并发需要先有资源隔离证明”。
- 加 `-race` 测试：8 个并行 `Send`，每个脚本各自读不同文件，结果互不污染。

## 验收标准

- `go test -race ./internal/tools ./internal/agent` 通过。
- 并发测试证明：8 个并行脚本各自返回正确内容，无 `ErrForeign`。
- 测试证明串行分类生效：两个 `script` 调用不会重叠执行（用阻塞桩工具计时）。
- 池在脚本 panic/返回错误后仍能正确归还，不泄漏（用计数断言）。
- 代码注释说明池容量与 Neo `MaxParallelTools` 的关系。

## Closed
ponytail 评审：Script 的 ParallelSafe 恒为 false，运行时池没有第二个消费者；#65 每次 new Runtime 已足够。若将来改判并发，届时再按证据加池
