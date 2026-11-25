---
id: 103
title: 'Correct the #97 measurement record: the solo arm silently fell back to codemode'
status: open
priority: high
labels:
    - performance
    - docs
relations:
    related-to:
        - 97
        - 101
        - 104
        - 105
created: "2026-11-25"
updated: "2026-11-25"
---


## 背景

2026-11-25 全盘评审发现，docs/performance.md 的 "A task set where one bash command cannot finish" 小节（#97）记录的对照是错的，且错误由本次测量本身引入。

该小节声称 "Same provider, model and binary, prompt and fixture fixed, only features.codemode changed: codemode (n=3, the default surface) and solo (n=5, the single-tool surface)"。

但 solo 在 ad63f36 已经不存在了。提交信息原文："Solo is gone as a name and as a separate implementation." 合法值只有 codemode 与 classic（internal/config/config.go:142-143）。

实测（临时探针，已删除）：

    mode=codemode  classic=false tools=[script]
    mode=solo      classic=false tools=[script]
    mode=classic   classic=true  tools=[bash edit_file glob grep read_file script write_file]

ClassicCodeMode() 只判断是否等于 classic，其余一律按默认处理，因此 solo 静默回退成 codemode。也就是说那次测量的两个"臂"是同一个工具面，solo 一臂只是同一配置下的 5 次重复采样。

## 影响

- 表中 "codemode (n=3)" 与 "solo (n=5)" 被当作两种工具面的对照，实际是一种。
- 结论句中 "This is the first non-zero merge rate... 1 of 3 in code mode and 2 of 5 in solo" 的对比不成立。
- "only features.codemode changed" 的表述不成立。
- 原始数据本身仍然有效：8 次运行、3 次合并，只是不能表述为两种模式之间的对照。

## 目标

让该小节如实反映实际测到的条件，不改变原始计数。

## 实现要点

- 把表合并为单一工具面的 8 次采样（或保留两行但明确标注二者是同一 surface、分别 n=3 与 n=5 的重复采样，并说明为何保留）。
- 删除 "only features.codemode changed" 与 "solo (n=5, the single-tool surface)" 的表述。
- 合并率改写为 8 次运行中 3 次（3/8），保留"每个脚本调用 3-4 次、单次调用内 4-10 个子调用"的形态描述。
- 在 #97 与 #101 的 issue 正文追加更正说明，指明原结论的这一处错误。
- 结论（不支持改提示词）仍可成立，但要重新表述为：它基于单一工具面的 8 次采样，不再是两模式对照。

## 验收标准

- docs/performance.md 中不再把 solo 当作可配置值表述（grep -n solo docs/performance.md 的剩余命中必须是明确标注"已移除"的历史叙述）。
- 表与正文的样本数与合并计数自洽：总计 8 次运行、3 次合并。
- 不新增任何产品代码改动。
