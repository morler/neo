---
id: 106
title: Make a zero-tool registry unreachable by construction, not by caller convention
status: closed
priority: medium
labels:
    - config
relations:
    related-to:
        - 102
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---


## 背景

#102 的零工具守卫只加在 headless 入口（cmd/neo/main.go 的 headlessAgent，len(reg.Names()) == 0 时拒绝启动）。交互面没有同样的检查。

实测（临时探针，已删除）：

    interactive, script off, NO extra tools: []        (count=0)
    interactive, script off, WITH coordinators: [stub]

现状评估：真实交互路径会通过 chatExtraTools 传入 agent/goal/workflow，所以当前**不可达**空工具面。但这是靠调用方约定维持的不变式，registryFor 本身在 code mode + script:false 时返回空注册表：

    func registryFor(...) {
        ...
        if !scripted {
            base = nil   // 只剩 extra
        }
        return tools.NewRegistry(append(base, extra...)...)
    }

注释里写着"headlessAgent refuses that combination"，把安全性交给了某个特定调用方。任何新增调用点（或 chatRuntime 的 /codemode off 在 extra 为空时）都会静默得到空工具面，与 #102 修的是同一类缺陷。

## 影响

目前无实际故障，属于不变式没有在正确层次表达的风险。#102 已经证明了这类"静默无工具"是最坏的一类失败：会话正常回话、文件没变、没有错误。

## 目标

让"零工具会话"不可能被静默构造出来，且不影响交互面与 classic 面。

## 实现要点

候选方案，择一：

- A：在 registryFor 内部，当 code mode 且 scripted 为假且 extra 为空时直接 panic 或返回带错误的注册表。缺点：registryFor 现在没有错误返回，改签名会波及全部调用点与测试。
- B：新增一个显式构造器（如 registryForChecked）返回 (*tools.Registry, error)，headless 与 buildChatRuntime 都走它，registryFor 保留给测试。
- C：在 agent.New 或 chatRuntime 初始化处断言工具数 > 0，把不变式放在最靠近使用点的地方。

倾向 B 或 C：A 的成本与收益不匹配，且 panic 在库代码里不合适。

- 交互面行为必须保持不变：有 agent/goal/workflow 时照常启动。
- 不要削弱 #102 已有的 headless 拒绝路径与它的测试。

## 验收标准

- 单测：code mode + script:false + 无协调工具时，所选的入口返回明确错误，而不是空工具面。
- 单测：code mode + script:false + 有协调工具时照常返回工具面。
- #102 的 TestHeadlessRunRefusesSessionWithNoTools 与 TestRegistryForNoScriptSurface 继续通过。
- 若采用方案 B/C，说明为何不改 registryFor 的签名。
- go test ./...、go vet ./...、qlty check 通过。

## 修复记录（2026-11-25）

先核实了实际可达性：chatExtraTools 恒定返回 workflow 与 goal 两个工具（cmd/neo/main.go），因此交互面在 code mode + script:false 下不会得到空工具面。空面目前不可达，所以没有采用会引入永不触发错误的方案 A/B，改为"把不变式写在能失败的地方"：

- 重写 registryFor 里 base = nil 的注释，写明两条调用方的不同依赖：交互面由 chatExtraTools 保证非空，headless 无 extra 因而由 headlessAgent 拒绝。
- 新增 TestInteractiveSurfaceIsNeverEmpty，覆盖 codemode/classic × script=true/false 四种组合，用与 /codemode 相同的重建路径断言工具数非零。

为什么不做方案 A/B：registryFor 改签名会波及全部调用点与测试，而当前没有任何可达路径能构造空面；用测试固定不变式成本更低，且在它被打破时会真的失败。若将来交互面允许 extra 为空，该测试会先失败，届时应升级为构造器级校验。

验收核对：

- 新测试四种组合全部通过。
- #102 的 TestHeadlessRunRefusesSessionWithNoTools 与 TestRegistryForNoScriptSurface 继续通过。
- 交互面行为未变（不拒绝启动，工具面仍为 agent/goal/script/workflow）。
