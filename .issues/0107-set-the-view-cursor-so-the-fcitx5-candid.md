---
id: 107
title: Set the view cursor so the fcitx5 candidate window follows the input point
status: closed
priority: medium
labels:
    - bug
    - tui
created: "2026-11-25"
updated: "2026-11-25"
closed: "2026-11-25"
---


## 现象

在 Neo 的交互界面里用 fcitx5 输入中文时，候选框不在光标处，而是固定在终端右下角。英文输入正常，说明不是键盘或编码问题，而是**输入法无法得知文本光标位置**。

## 根因（已核对代码与依赖源码）

Neo 使用 bubbletea v2（charm.land/bubbletea/v2 v2.1.1）。v2 改变了光标契约：不再像 v1 那样由程序直接输出光标定位转义序列，而是由 View 在 tea.View 里声明 Cursor 字段，框架据此把光标放到正确位置。

internal/tui/model.go:589 的 makeView 只设置了这些字段：

    func makeView(content string) tea.View {
        v := tea.NewView(content)
        v.AltScreen = true
        v.MouseMode = tea.MouseModeCellMotion
        v.KeyboardEnhancements.ReportAlternateKeys = true
        return v
    }

**从未设置 v.Cursor**。bubbletea v2 的 View.Cursor 注释写明："When not nil, the cursor will be shown at the specified position"（tea.go:128-129）。为 nil 时框架不定位光标，终端只能把它留在最后一帧绘制结束的位置——即整个视图的右下角，正是 fcitx5 候选框出现的地方。

全仓库搜索确认：internal/tui/ 里没有任何地方给 View.Cursor 赋值（grep 'tea.Cursor\|Cursor{' 无命中），也没有调用 SetCursor 类 API。

## 影响

- 中文（以及日文、韩文等所有需要输入法候选框的语言）输入时，候选框远离输入点，用户必须在"编辑器光标"和"候选框"之间来回看，且候选框可能遮挡 footer/状态行。
- 这是一个纯显示/输入体验缺陷，不影响已提交的文本内容，因此不会体现在测试里。
- 项目已专门修过 CJK 相关缺陷（internal/tui/composer_cjk_test.go 记录了宽字符换行导致丢字符的 bug），说明 CJK 输入是明确支持场景，此缺陷属于同一类遗留缺口。

## 目标

让终端文本光标始终位于输入框的真实插入点，使 fcitx5（及其他 IME）把候选框显示在光标附近。

## 实现要点

- 在 makeView 里设置 v.Cursor。位置需是输入框内插入点的屏幕绝对坐标（相对整个 View 内容，0 基）。
- 坐标来源：bottom 是 styInputBar.Width(m.width).Render(m.input.View()) 渲染出的输入栏，位于 viewport、空行、status、空行之后。需要把"输入框内光标行列"换算成"整个 content 内的光标行列"，并计入 styInputBar 的边框/内边距偏移。
- bubbles v2 的 textarea 提供 VirtualCursor() 与光标位置相关的 API（charm.land/bubbles/v2@v2.1.0/textarea/textarea.go:448），需要确认它的行列是否已计入自身边框，再用 lipgloss 的尺寸计算换算。
- 分支情况：approval 待决时输入框被 approvalBarView 替换，此时不应定位到输入栏；picker 可见、quitting、width == 0 时同理。这些场景应让 Cursor 保持 nil（或定位到合理位置），避免光标飘到错误行。
- 不要为定位而改变既有布局或换行逻辑；这是纯坐标计算。
- 注意宽字符：光标列需按显示宽度（ANSI/lipgloss 列）而非 rune 数计算，否则输入中文时光标会偏左。

## 验收标准

- 手动验证：在 fcitx5 下输入中文，候选框出现在输入点附近，且在输入框任意位置（含换行后的第二行）都正确。
- 单测：新增一个计算函数（例如 viewCursor(content, bottom, ...)）的纯函数测试，覆盖输入框首行/末行、approval 替换输入栏、picker 可见三种情形，断言返回的坐标落在输入框范围内。
- 不能破坏现有 TUI 测试：internal/tui/composer_cjk_test.go、model_view_test.go、paste_test.go 等继续通过。
- go test ./...、go vet ./...、qlty check 通过。
- 若确认需要在特定终端下才可复现（如仅 Kitty 协议生效），在 issue 里记录实测终端与版本。

## 备注

- 复核时请顺带确认 v.KeyboardEnhancements.ReportAlternateKeys 与 IME 无冲突：Kitty 键盘协议开启时部分终端会改变输入法的组合语义，若开启光标后又出现新的 IME 异常，应记录在本 issue 而不是另开。
- 该缺陷与 #98-#102 那批工具面问题无关，属 TUI 层，故不设 depends-on。

## 修复记录（2026-11-25）

核实根因齐全：makeView 从未设置 tea.View.Cursor，且 newComposer 的 textarea 默认 useVirtualCursor = true，导致 textarea.Cursor() 恒为 nil。两处都必须改，只加 View.Cursor 仍拿不到位置。

改动：

- internal/tui/model.go 的 newComposer：调用 SetVirtualCursor(false)，让 textarea 绘制真实终端光标并对外报告位置。
- View()：把 composer 之上的内容（viewport、空行、status、空行）算成 above 高度，交给新增的 composerAnchor。
- composerAnchor：把 textarea 的相对坐标换算成帧坐标，加上 styInputBar 的 padding 偏移；在 approval 待决、浏览器/选择器可见、width==0、quitting 或 textarea 未聚焦时返回 nil。

实测坐标（80x24 测试模型，内容 "x"）：viewport 20 行 + status 在第 21 行 + 空行 + 输入栏文本行在第 24 行，光标 Y=24、X=4，正落在 " › x" 行，符合预期。

新增测试（internal/tui/cursor_anchor_test.go）：

- TestViewCursorFollowsTheComposer：断言有 cursor、落在含 "hello" 的行、列在 prompt 之后，且继续输入后列号前进。
- TestViewCursorIsAbsentWhenTheComposerIsNotFocused：blur 与 width==0 时 cursor 为 nil。
- TestComposerCursorSitsInsideTheInputBar：对渲染结果断言光标在该行内、在左内边距之后、不超过行宽。

夹具修正：helpers_test.go 的 makeTestModel 原先用裸 textarea.New()（虚拟光标开），与生产的 newComposer 不一致，会掩盖此缺陷；改为复用 newComposer。

已回退验证：注释掉 view.Cursor 赋值后，前两个测试失败（the view declares no cursor / no cursor），恢复后通过。

CHANGELOG 在新增的 Fixed 段记录。未改布局与换行逻辑，仅坐标换算。

未覆盖项（明确说明）：无图形终端，无法实测 fcitx5 候选框是否真的跟随光标；本修复基于框架契约与坐标断言，需在实际终端复验一次。

## 真机确认（2026-11-25）

用户在自己的图形环境下用 fcitx5 实测：候选框已跟随输入点，不再固定在右下角。此前记录里"无图形终端、无法实测候选框是否真的跟随"这一保留意见**就此撤销**。

验证链条现已完整：

| 层级 | 证据 |
| --- | --- |
| 单元 | TestViewCursorFollowsTheComposer / TestViewCursorIsAbsentWhenTheComposerIsNotFocused / TestComposerCursorSitsInsideTheInputBar |
| 协议 | 真实 pty 中 A/B 对比修复前后二进制：修复前 [?25l 且无定位；修复后 [?25h + [20;4H，落在输入框 "› " 之后 |
| 终端 | fcitx5 候选框跟随光标（用户实测） |

结论：根因判断正确（makeView 未声明 tea.View.Cursor，叠加 textarea 虚拟光标默认开启使 Cursor() 恒为 nil），两处改动缺一不可。
