# wuu Full Rebuild Design

Date: 2026-04-06
Status: Approved

## Goal

Rebuild wuu into a production-quality Coding Agent CLI that matches or exceeds
Claude Code's interaction quality. Go + Bubbletea stack, real SSE streaming,
polished TUI, extensible skill/hook system.

## Reference Implementations

Priority order for learning:
1. Claude Code (`thirdparty/claude-code-sourcemap/`) — best UX, most polished
2. Crush (`thirdparty/crush/`) — same Go/Bubbletea stack, directly reusable patterns
3. Codex (`thirdparty/codex/`) — Rust/Ratatui, good architectural ideas

## Architecture

```
cmd/wuu/main.go              # CLI entry, fast startup path
internal/
├── config/                   # Config loading & validation (extend existing)
├── providers/                # Provider abstraction layer
│   ├── types.go              # Unified interface: Chat + StreamChat
│   ├── openai/client.go      # OpenAI-compatible SSE streaming
│   ├── anthropic/client.go   # Anthropic SSE streaming
│   └── factory.go            # Provider factory
├── agent/                    # Agent loop (core)
│   ├── runner.go             # Main loop: streaming-first, tools execute as they arrive
│   ├── executor.go           # Concurrent tool executor
│   └── context.go            # Context management (token counting, compaction)
├── tools/                    # Tool system (extend existing)
│   ├── toolkit.go            # Tool registry & execution
│   └── ...                   # Individual tool implementations
├── tui/                      # TUI layer (full rewrite)
│   ├── app.go                # Bubbletea entry
│   ├── model.go              # Main state machine
│   ├── layout.go             # Responsive layout system
│   ├── chat.go               # Message rendering (polymorphic message items)
│   ├── input.go              # Input area (dynamic height, mouse support)
│   ├── markdown.go           # Markdown rendering
│   ├── commands.go           # Slash command system
│   └── footer.go             # Status bar + clock
├── hooks/                    # Hook lifecycle system
├── skills/                   # Skill loading & execution
├── memory/                   # Session persistence & memory
└── compact/                  # Context compaction strategies
```

### Design Principles (learned from Claude Code)

1. **Streaming-first** — tools execute as they arrive in the stream, not after full response
2. **Concurrency control** — safe tools run in parallel, exclusive tools serialize
3. **Context-aware** — token counting + auto-compaction
4. **Fast startup** — heavy modules lazy-loaded

## SSE Streaming

### Provider Interface Extension

```go
type StreamEvent struct {
    Type     string     // "content_delta", "tool_use_start", "tool_use_delta",
                        // "tool_use_end", "done", "error"
    Content  string     // Text delta
    ToolCall *ToolCall  // Tool call info (on start/end)
    Error    error
    Usage    *TokenUsage
}

type Client interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
}
```

### SSE Parsing

**OpenAI format:** `data: {"choices":[{"delta":{"content":"..."}}]}\n\n`
- Parse `data:` lines, JSON deserialize
- Handle `[DONE]` terminator
- Tool calls arrive incrementally via `delta.tool_calls`

**Anthropic format:** `event: content_block_delta\ndata: {"delta":{"text":"..."}}\n\n`
- Parse `event:` + `data:` lines
- Event types: `message_start`, `content_block_start`, `content_block_delta`,
  `content_block_stop`, `message_stop`
- Tool calls arrive via `tool_use` content blocks

### Agent Loop Integration

```
User input → Build request → StreamChat() → channel
    ↓
[Event consumption loop]
    ├─ content_delta → Push to TUI for real-time rendering
    ├─ tool_use_start → Prepare tool execution
    ├─ tool_use_end → Execute tool immediately (don't wait for response end)
    ├─ done → Collect results, decide whether to continue loop
    └─ error → Retry logic
```

## TUI Rewrite

### Layout

```
┌─────────────────────────────────────────┐
│ wuu · provider/model · context tokens   │  ← Header (1 line)
├─────────────────────────────────────────┤
│                                         │
│  [user] Your prompt...                  │
│                                         │
│  [assistant] Streaming markdown...      │  ← Chat area (viewport, scrollable)
│  ├─ [tool] read_file: src/main.go       │
│  └─ [tool] write_file: src/utils.go     │
│                                         │
│                          ▼ Jump to bottom│
├─────────────────────────────────────────┤
│ > Type your prompt...                   │  ← Input (dynamic height 3-15, fixed bottom)
│ :::                                     │
├─────────────────────────────────────────┤
│ ● streaming · 1.2s · 15:42:30          │  ← Footer (status + clock, fixed bottom)
└─────────────────────────────────────────┘
```

### Key Improvements

1. **Responsive layout** — compact mode when terminal width < 80
2. **Polymorphic message items** — UserItem / AssistantItem / ToolItem / SystemItem
3. **Dynamic input** — min 3 lines, grows to max 15 lines (ref: crush)
4. **Markdown rendering** — Glamour, CommonMark + GFM core subset
5. **Real streaming** — per-token rendering from SSE, not local chunking
6. **Mouse support** — click to focus, scroll wheel, click jump-to-bottom
7. **Tool call display** — collapsible, show tool name + summary

### State Management

- Centralized Model (ref: Claude Code's AppState)
- Focus management: Chat vs Input, keyboard routing follows focus
- States: idle / streaming / tool_executing / waiting_input

## Slash Commands

### Command Type

```go
type Command struct {
    Name        string
    Aliases     []string
    Description string
    ArgHint     string
    InlineArgs  bool
    DuringTask  bool
    Hidden      bool
    Type        string // "local" | "prompt" | "skill"
    Execute     func(args string, ctx *CommandContext) error
    Prompt      func(args string, ctx *CommandContext) string
}
```

### Built-in Commands

| Command | Type | Function | Reference |
|---------|------|----------|-----------|
| `/compact` | local | Compress context | Claude Code + Codex |
| `/model` | local | Switch model/provider | Claude Code + Codex |
| `/resume` | local | Resume previous session | All three |
| `/fork` | local | Fork current session | Claude Code + Codex |
| `/new` | local | Start new conversation | Codex |
| `/clear` | local | Clear screen | Claude Code + Codex |
| `/diff` | local | Show git diff | Claude Code + Codex |
| `/status` | local | Show session config + token usage | Claude Code + Codex |
| `/copy` | local | Copy last output | Claude Code + Codex |
| `/worktree` | local | Create/switch git worktree | Claude Code |
| `/skills` | local | List available skills | All three |
| `/insight` | local | Session stats & diagnostics | wuu original |
| `/help` | local | Show help | Universal |
| `/exit` | local | Exit | Universal |

### Skill Commands

- Scan `.wuu/skills/` and `~/.config/wuu/skills/`
- Markdown + YAML frontmatter format
- Type "prompt", content injected into agent loop
- User-level overrides project-level same-name skills

### Command Completion

- Show command list popup on `/` input (ref: Codex)
- Fuzzy matching, sorted by usage frequency
- Display description and argument hints

## Skills System

### Skill Format (ref: Claude Code)

```yaml
---
name: /commit
description: Create a git commit
---
# Instructions...
```

### Discovery

- Project: `.wuu/skills/`
- User: `~/.config/wuu/skills/`
- Recursive walk, parse YAML frontmatter
- User-level overrides project-level duplicates

### Execution

- Inject skill content as system prompt addition
- Execute through normal agent loop
- Skill can specify allowed tools

## Hooks System

### Configuration (in `.wuu.json`)

```json
{
  "hooks": {
    "pre_prompt": [{"command": "echo $WUU_PROMPT"}],
    "post_prompt": [{"command": "echo $WUU_ANSWER"}],
    "pre_tool": [{"tool": "run_shell", "command": "..."}],
    "post_tool": [{"tool": "*", "command": "..."}]
  }
}
```

### Exit Code Semantics (ref: Claude Code)

- 0 = success
- 2 = block operation
- Other = show error to user

## Context Management

### Compaction Strategies

1. **Auto-compact** — triggered when tokens exceed 80% of context window
   - Find oldest assistant message as compaction boundary
   - Generate summary to replace old messages
   - Preserve tool call trajectory integrity

2. **Manual `/compact`** — user-triggered

### Token Counting

- OpenAI: estimate via `tiktoken-go`
- Anthropic: precise count from API `usage` field
- Display used/total in footer real-time

## Error Recovery

```
API call failure
    ├─ 429 Rate Limit → exponential backoff (1s, 2s, 4s...), parse retry-after
    ├─ 529 Overloaded → same as above
    ├─ 500/502/503 → retry up to 3 times
    ├─ 401 Auth Error → stop, prompt user to check API key
    ├─ Context Window Exceeded → auto-trigger compact, then retry
    └─ Network Error → retry up to 3 times with backoff
```

## Model Compatibility

Already supported via existing provider layer:
- Anthropic official (Claude series)
- OpenAI official (GPT series)
- OpenAI-compatible relays (OpenRouter, One-API, New API, etc.)
- Chinese model gateways (Qwen, Wenxin, Moonshot — via OpenAI-compatible protocol)

Core change: add `StreamChat` to both providers. No new provider types needed.

## Implementation Rounds

| Round | Goal | Acceptance Criteria |
|-------|------|---------------------|
| R1 | Provider SSE streaming | OpenAI + Anthropic both return real SSE streams |
| R2 | Agent loop streaming integration | Streaming tokens reach TUI in real-time, tools execute mid-stream |
| R3 | TUI layout rewrite | Responsive layout, dynamic input, fixed bottom |
| R4 | Markdown rendering + streaming render | CommonMark + GFM core subset, per-token rendering |
| R5 | Mouse support + interaction polish | Click focus, scroll wheel, jump-to-bottom, drag select |
| R6 | Slash commands | All 14 built-in commands working |
| R7 | Skills system | Discovery + execution, markdown frontmatter format |
| R8 | Hooks + context management | Lifecycle hooks, auto-compact, token counting |
| R9 | Error recovery + stability | Retry logic, disconnect recovery, edge cases |
| R10 | UI polish + performance | Startup speed, render performance, visual detail alignment |
