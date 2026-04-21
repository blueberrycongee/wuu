# Proxy Compatibility: Message Structure Requirements

> **Date**: 2026-04-15  
> **Status**: Resolved  
> **Severity**: Critical — caused deterministic reconnect loops on all proxy-based deployments

## Summary

wuu 在通过中转站（如 claude-code.club）访问 Anthropic API 时，会在 tool 调用后进入无限 503 reconnect 循环。Claude Code 使用同一中转站完全正常。

根因是 wuu 生成了中转站不接受的消息结构。经过逐字段对比 CC 和 wuu 的请求，发现了三个独立的问题。

## 根因分析

### 问题 1：空 assistant 消息（最终根因）

**触发条件**：模型处理 tool_result 后返回空内容（`Content: ""`）

**错误的修复尝试**：在 `shouldPersistAssistantMessage` 中强制持久化空 assistant，或在 `buildAnthropicRequest` 中插入合成的空 assistant 分隔符 `{"type":"text","text":" "}`。

**中转站行为**：拒绝 `{"role":"assistant","content":[{"type":"text","text":" "}]}`，返回 503。

**验证方法**：
```
msgs[0:3] (user + assistant + tool_result) → OK
msgs[0:4] (+ 空 assistant)                 → 503  ← 100% 可复现
```

### 问题 2：`text` 字段 omitempty

**触发条件**：`anthropicBlock.Text` 使用 `json:"text,omitempty"` tag

**效果**：空 assistant 消息序列化为 `{"type":"text"}` 缺少 `text` 字段（应为 `{"type":"text","text":""}`）

**修复**：确保 text block 始终包含 `text` 字段（用空格代替空字符串）。但此修复与问题 1 冲突——带空格的 text block 也被拒绝。

### 问题 3：去掉 omitempty 后 tool_use/tool_result 多了 `"text":""`

**触发条件**：移除 `omitempty` 后，所有 block 类型都带上了 `"text":""`

**效果**：`tool_use` 和 `tool_result` blocks 不应有 `text` 字段，中转站可能因此拒绝。

## 最终修复

**回到最简方案**：

1. **不插入合成的空 assistant 消息** — 中转站拒绝它
2. **不持久化空 assistant 消息** — 没有内容就不存
3. **简单合并连续同 role 消息** — `tool_result` 和 `text` blocks 允许共存于同一条 user 消息（Anthropic 官方 API 接受，CC 也产生这种结构）
4. **保留 `omitempty`** — 非 text block 不应有 `text` 字段

```go
// buildAnthropicRequest 中的合并逻辑
if n := len(payload.Messages); n > 0 && payload.Messages[n-1].Role == mapped.Role {
    payload.Messages[n-1].Content = append(payload.Messages[n-1].Content, mapped.Content...)
} else {
    payload.Messages = append(payload.Messages, mapped)
}
```

## 为什么 Claude Code 不会触发

CC 的 Agent tool 是**同步阻塞**的：

```
user → assistant(tool_use) → user(tool_result) → assistant("真实回复") → user
```

模型一定会在 tool_result 后生成真实回复。不存在空 assistant 的场景。

wuu 的 `spawn_agent` 是异步的，模型可能在 tool_result 后直接 stop，然后 worker 完成时注入 user message，导致 tool_result 和 text 紧挨。

## 中转站拒绝规则（实测总结）

| 结构 | 中转站行为 | Anthropic 官方 API |
|------|-----------|-------------------|
| 连续 user 消息（未合并） | **拒绝 503** | 接受 |
| user 消息混合 tool_result + text blocks | **接受** | 接受 |
| 空 assistant `{"text":""}` | **拒绝 503** | 可能接受 |
| 空 assistant `{"text":" "}` | **拒绝 503** | 接受 |
| tool_use block 带 `"text":""` 字段 | **可能拒绝** | 忽略 |
| `{"type":"text"}` 缺少 text 字段 | **拒绝** | 拒绝 |
| thinking 启用但 assistant 缺少 thinking blocks | **拒绝 503** | 拒绝 |
| assistant 有 thinking+tool_use 但多了空格 text | **拒绝 503** | 可能接受 |

## 排查方法论

### 1. 启用 debug log

wuu 自动写 `.wuu/debug.log`，记录：
- 每个请求的 role sequence
- SSE error event 的 code + message
- HTTP 错误的完整 body

### 2. dump 请求

```bash
WUU_DUMP_REQUEST=/tmp/req.json wuu
```

对话后检查 `/tmp/req.json` 的完整结构。

### 3. 逐消息二分

```python
# 从完整消息列表二分，找到哪条消息触发 503
test('msgs[0:2]', msgs[:2])  # OK?
test('msgs[0:3]', msgs[:3])  # OK?
test('msgs[0:4]', msgs[:4])  # 503? ← 这条消息是问题
```

### 4. 对比 CC 请求

CC 的请求格式是标准答案。任何 wuu 发出但 CC 不发出的字段/结构都是潜在风险。

## 问题 4：Thinking blocks 未回传（2026-04-15 发现）

**触发条件**：使用 `effort` 设置（触发 `thinking: {type: "adaptive"}`）后，模型响应包含 thinking blocks。

**根因**：`mapMessage` 只处理 `Content` 和 `ToolCalls`，完全忽略 `ReasoningContent` 字段。Anthropic API 的 `interleaved-thinking` beta 要求前序 assistant 消息中的 thinking blocks 必须在后续请求中回传。

**触发链**：
1. 模型响应包含 thinking blocks → 存入 `ChatMessage.ReasoningContent`
2. 下一轮请求：`mapMessage` 丢弃 `ReasoningContent` → 无 `{"type":"thinking"}` block
3. API/proxy 拒绝缺少 thinking blocks 的请求 → 503
4. 503 可重试 → 同样的错误请求重连 → 死循环

**特别场景 — worker 完成后**：
- Worker 完成 → 注入 user 消息 → auto-resume 触发新一轮请求
- 请求包含前序所有 assistant 消息（含 thinking 的）→ thinking 全丢 → 503 死循环
- 这就是为什么 "worker complete 后 reconnecting" 的表现

**修复**：
1. `anthropicBlock` 增加 `Thinking` 字段（`json:"thinking,omitempty"`）
2. `mapMessage` 对 assistant 消息生成 thinking block（放在 text/tool_use 之前）
3. 当 assistant 有 thinking 或 tool_use 时，不强制注入空格 text block

**同时修复的空 text block 问题**：
- 旧逻辑：所有消息都强制 `{"type":"text","text":" "}`
- 问题：assistant 只有 thinking + tool_use 时，空格 text block 被 proxy 拒绝
- 新逻辑：仅在消息无其他 block 时才回退到空格 text block

## 防回归清单

修改 `buildAnthropicRequest` 或 `mapMessage` 时必须检查：

- [ ] **不要插入合成的 assistant 消息** — 任何 `Content` 为空/空格/占位符的 assistant 都会被中转站拒绝
- [ ] **text block 的 `text` 字段必须存在** — `omitempty` 会吞掉空字符串
- [ ] **非 text block 不能有 `text` 字段** — 不要去掉 `omitempty`，改用其他方式确保 text block 有值
- [ ] **连续同 role 消息必须合并** — 简单 append content blocks
- [ ] **tool_result + text 混合是安全的** — Anthropic 官方 API 和 CC 都接受
- [ ] **assistant 消息必须回传 thinking blocks** — `ReasoningContent` 非空时生成 `{"type":"thinking"}` block，放在 text/tool_use 之前
- [ ] **有其他 block 时不强制空 text block** — 只在消息无 thinking/tool_use/image 时才回退到 `{"type":"text","text":" "}`

## 相关文件

- `internal/providers/anthropic/client.go` — `buildAnthropicRequest`, `mapMessage`
- `internal/agent/loop.go` — `shouldPersistAssistantMessage`
- `internal/providers/anthropic/worker_result_test.go` — 回归测试
