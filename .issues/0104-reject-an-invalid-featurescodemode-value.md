---
id: 104
title: Reject an invalid features.codemode value instead of silently falling back
status: open
priority: high
labels:
    - config
    - cli
relations:
    related-to:
        - 103
created: "2026-11-25"
updated: "2026-11-25"
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
