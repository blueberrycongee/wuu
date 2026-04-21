# TUI Rendering Stability: Position Jump Fixes

> **Date**: 2026-04-15  
> **Status**: Resolved  
> **Severity**: Visual — content positions shifted during streaming, degrading UX

## Summary

wuu 的 TUI 在流式输出过程中，内容位置会反复跳动。用户看到的表现是：文字和工具卡片之间的间距在输出时很宽，输出完突然变窄；用户发完消息后模型开始回复时也会跳一下。

经过排查，发现了四个独立的渲染问题。

## 问题 1：流式光标 `▌` 独立成行

**触发条件**：模型输出文字后调用工具

**根因**：`▌` 被 `parts = append(parts, "▌")` 作为独立元素加入 parts 数组。`strings.Join(parts, "\n")` 在 text 和 tool card 之间插入了额外空行。流结束后 `▌` 消失，间距突然变窄。

**修复**：`▌` 追加到最后一行文字末尾（`textPart += "▌"`），不再独立成行。

## 问题 2：空 assistant entry 不显示光标

**触发条件**：用户发送消息，等待模型首个 token

**根因**：`renderTextFull` 对空内容 `content == "(empty)"` 直接 return，不渲染任何东西（包括光标）。首个 token 到达后内容从无到有，viewport 跳动。

**修复**：空内容 + `isStreamTarget` 时，预渲染一行 `  ▌` 占位。

## 问题 3：markdown 渲染仅在流结束时执行

**触发条件**：任何流式回复

**根因**：`StreamCollector.Commit()` 返回原始文本（无 markdown 渲染），`Finalize()` 才通过 goldmark 渲染 markdown。流式期间是原始 pipe 字符，流结束后突然变成 box-drawing 表格——行数剧变。

**修复**：`Commit()` 改为每个 100ms tick 都渲染 markdown。流式和最终输出使用同一个渲染管线，内容高度始终一致。对齐 Codex CLI 的做法（在换行边界渐进渲染 markdown）。

## 问题 4：`compositeEntry` 流式期间不使用已渲染的 markdown

**触发条件**：任何流式回复

**根因**：`compositeEntry` 有 `!isStreamTarget` 守卫，阻止流式期间使用 `e.rendered`。即使 `Commit()` 已经渲染了 markdown，显示代码还是走 raw text 路径。

**修复**：去掉 `!isStreamTarget` 守卫。流式期间直接使用 `Commit()` 产出的渲染结果。

## 问题 5：表格显示为原始 pipe 字符

**触发条件**：模型回复包含 markdown 表格

**根因**：TUI 的 `compositeEntry` 对已渲染的 markdown 调用 `wrapText()`，二次换行破坏了 box-drawing 表格的对齐。

**修复**：markdown 渲染器内部处理段落换行（`flushParagraph`），TUI 不再对渲染后的输出做 `wrapText`。

## 问题 6：Tool card 过重

**触发条件**：任何工具调用

**根因**：使用 `lipgloss.RoundedBorder()` box-drawing 边框，视觉过于沉重，不对齐 Codex CLI 的轻量风格。

**修复**：改为 Codex 风格的树形缩进：
```
• Called read_file · main.go
  └ package main...
```

## 修复时间线

| Commit | 修复内容 |
|--------|---------|
| `f2200b2` | 交错文字段按原始流位置渲染（textSegmentOffsets） |
| `f2ded2d` | markdown 表格正确渲染（段落换行移入渲染器） |
| `ef66b0d` | Tool card 对齐 Codex 树形风格 |
| `56a0efa` | 流式期间渐进渲染 markdown（Commit 渲染） |
| `094fa74` | 流式期间使用已渲染的 markdown（去掉 !isStreamTarget） |
| `ce6358a` | 光标追加到行末而非独立成行 |
| `e822923` | 空 assistant entry 预渲染光标占位 |

## 防回归清单

修改 `compositeEntry` 或流式渲染逻辑时：

- [ ] **光标 `▌` 不能独立成行** — 必须追加到文字末尾（`textPart += "▌"`）
- [ ] **空 entry 也要渲染光标** — `isStreamTarget` 时不能因内容为空直接 return
- [ ] **流式期间使用渲染后的 markdown** — 不要有 `!isStreamTarget` 守卫
- [ ] **不要对渲染后的 markdown 做 `wrapText`** — 会破坏表格和代码块
- [ ] **`parts` join 用 `\n`** — 每个 part 不能有多余的尾部 `\n`，否则产生空行

## 已知遗留问题

### 表格渲染仍不完善

**现象**：markdown 表格在 TUI 中显示仍有问题，可能表现为对齐偏差、边框残缺或中文字符宽度计算不准。

**已修复的部分**：
- `wrapText` 不再对渲染后的 markdown 二次换行（之前会拆断 box-drawing 行）
- 段落换行移入渲染器内部（`flushParagraph`），TUI 不再干预

**仍可能存在的问题**：
- 流式期间表格渐进渲染：goldmark 需要完整的表格结构（header + separator + rows）才能识别为表格。流式输出时部分行到达可能导致先显示为纯文本，表格完整后突然切换为 box-drawing
- CJK 双宽字符的列宽计算：`lipgloss.Width` 和 `wordwrap` 对中文字符的宽度度量可能与终端实际显示不一致，导致表格列对不齐
- 表格宽度超出 viewport 时的截断/换行行为未定义

**参考**：Codex CLI 完全不渲染表格（goldmark 解析后跳过 `Tag::Table`），直接显示原始 pipe 字符。如果 box-drawing 表格持续有问题，可以考虑退化到 Codex 的做法。

## 相关文件

- `internal/tui/model.go` — `compositeEntry`、`renderTextSegment`、`renderTextFull`
- `internal/tui/render_toolcard.go` — tool card 渲染
- `internal/markdown/stream.go` — `StreamCollector`（Commit/Finalize）
- `internal/markdown/render.go` — `Render`、`flushParagraph`
