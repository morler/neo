---
id: 35
title: 'Make supercov usable in this repo: fix the endpoint env var and document the workflow'
status: closed
priority: medium
labels:
    - tooling
created: "2026-11-23"
updated: "2026-11-24"
closed: "2026-11-24"
---

## 背景

`supercov` 在当前 shell 里**完全跑不通**：`supercov quality` 对 75 个文件全部返回

    openrouter.ai HTTP 404: the request failed

且汇总结论是误导性的 `Quality not scored: no file was assessed.`。

## 根因

supercov 请求 `<TYPESAFE_BASE_URL>/v1/systemone`。当前环境的变量是：

    TYPESAFE_BASE_URL=https://openrouter.ai/api/alpha/decisions
    JEV_BASE_URL=https://openrouter.ai/api/alpha/decisions
    TYPESAFE_DEFAULT_MODEL=liquid/d1.2

拼出的 URL 是 `https://openrouter.ai/api/alpha/decisions/v1/systemone` → **404**（已用 curl 直接验证 404）。

把 base 改成 `https://openrouter.ai/api` 后拼成 `https://openrouter.ai/api/v1/systemone` → 可用：

    $ TYPESAFE_BASE_URL=https://openrouter.ai/api TYPESAFE_DEFAULT_MODEL=liquid/d1.2 supercov quality
    Quality fair (7.1/10) over 75 files.
    39 good, 36 fair, 0 weak.

模型 `liquid/d1.2` 本身没问题，全程未改。注意基线不能写成 `https://openrouter.ai/api/v1`，那会拼成 `/api/v1/v1/systemone`。

另外确认：OpenRouter 的模型列表里**不存在** `liquid/d1.2` 这个 id（`GET /api/v1/models` 共 444 个模型，含 `liquid/` 的只有 `liquid/lfm-3-1.7b`、`liquid/lfm-2.5-24b-a2b`，Jev 相关只有 `typesafe/jev-router`）。但 `liquid/d1.2` 经 `/api/v1/systemone` 这个 Jev 网关是能工作的——该端点是转发层而非模型目录，故不受此影响。

## 目标

1. 让贡献者能一次跑通 `supercov`，不必试错。
2. 不把个人环境的 key 或路径写进仓库。

## 实现要点

- 在 `docs/developer/` 下新增一节（或在既有开发文档里加小节），写明：
  - 需要的三个变量：`TYPESAFE_API_KEY`、`TYPESAFE_BASE_URL=https://openrouter.ai/api`、`TYPESAFE_DEFAULT_MODEL`。
  - base 的拼接规则是 `<base>/v1/systemone`，所以 base 不能带 `/v1`。
  - 一条冒烟命令：`supercov quality cmd/neo/doctor.go`，正常输出形如 `Quality fair (7.8/10) over 1 file.`
- 文档里不要出现具体 key；用 `<your-key>` 占位。
- 可选：在 `justfile` 增加 `just quality` 目标，包装那条冒烟命令，便于本地复现。若要加，必须保证缺少 `TYPESAFE_API_KEY` 时给出清晰提示而不是 404 噪音。
- `.supercov/` 已被其自带 `.gitignore`（内容为 `*`）忽略，无需改仓库 `.gitignore`；已用 `git check-ignore -v .supercov/` 确认。

## 验收

- 按文档从干净 shell 走一遍，`supercov quality <单文件>` 成功。
- 文档检查：`grep -rn "sk-or-v1" docs/` 无输出。
- 若加了 `just` 目标，`just --list` 能看到它。
