---
id: 104
title: Reject an invalid features.codemode value instead of silently falling back
status: closed
priority: high
labels:
    - config
    - cli
relations:
    related-to:
        - 103
        - 108
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---


## 背景

--codemode= 命令行参数有严格校验（cmd/neo/main.go:336-342，非法值直接报错），但配置文件这条路没有任何校验。

实测（临时探针，已删除）：把 features.codemode 依次设为下列值并经过 config.Load() 与 registryFor()：

    mode="codemode"  accepted -> tools=[script]
    mode="classicc"  accepted -> tools=[script]   <- 拼写错误，静默回退
    mode="solo"      accepted -> tools=[script]   <- 已删除的模式，静默回退
    mode=""          accepted -> tools=[script]

根因：internal/config/config.go:148-150 的 ClassicCodeMode() 只做 strings.EqualFold(..., "classic")，任何其它值都被当作默认 codemode，config.Load() 不做校验。

## 影响

这直接造成了 #97 的测量错误：配置里写 solo 的会话静默跑成了 codemode，而使用者无从察觉。对真实用户同理：写错 classic 会拿到与预期完全不同的工具面，且没有任何提示。这与 MISSION 的 "safe local execution with explicit authority and useful failure messages" 冲突。

## 目标

配置文件里非法的 codemode 值必须显式失败，而不是静默回退。

## 实现要点

- 在 internal/config 增加校验：当 Features.CodeMode 非空且不匹配任一已知值时返回错误，消息列出合法值。
- 空值保持现状（未设置即默认），这是合法状态。
- 允许大小写与首尾空白（与现有 EqualFold + TrimSpace 行为一致）。
- 复用 config.CodeModeCodemode / config.CodeModeClassic 常量，不要新造字符串。
- 与 takeCodeModeFlag 的错误措辞保持一致：--codemode must be "codemode" or "classic", got %q。
- 这是用户可见的行为变化：以前静默可用的错误配置现在会启动失败。需在 CHANGELOG.md 的 Changed 段记录，并在 docs/developer/config.md 的 codemode 行说明非法值会被拒绝。

## 验收标准

- 单测：codemode 写成 classicc 或 solo 时 config.Load() 返回错误，且消息含合法值。
- 单测：codemode 缺省、codemode、classic、" Classic " 均正常加载。
- neo run 与交互面在该配置下给出同一条错误，不进入无工具状态。
- docs/developer/config.md 与 CHANGELOG.md 已更新。
- go test ./...、go vet ./...、qlty check 通过。

## 修复记录（2026-11-25）

在 internal/config/config.go 增加 validateCodeMode，于 parseConfig 中与 validateOpenAIAuth 并列调用：

- 空值视为未设置，保持合法（默认面）。
- codemode 与 classic 忽略大小写与首尾空白后合法。
- 其余一律报错，消息列出合法值。

措辞与 takeCodeModeFlag 保持一致，复用 config.CodeModeCodemode / CodeModeClassic 常量。

一处有意的取舍：错误消息原本想写"或传 --codemode="，但实测无效——loadConfig 先调 config.Load()，配置坏了就走不到覆盖分支，所以改写成"修改该文件"。宁可消息诚实，也不给一条走不通的路。

新增测试：

- TestLoad_RejectsAnUnknownCodeMode：solo / classicc / codemodee / 带尾空白的 solo 全部被拒，且消息含合法值。
- TestLoad_AcceptsEveryValidCodeMode：空值、codemode、classic、" Classic "、CODEmode 五种状态，并断言 ClassicCodeMode() 的结果。

已回退校验确认这两个测试在修复前失败（错误为 ... was accepted）。

文档：docs/developer/config.md 的 codemode 行补了一句"其它值启动时被拒绝，拼写错误不会静默改变工具面"；CHANGELOG 在 Changed 段记录该行为变化。
