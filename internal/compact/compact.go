package compact

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const defaultCompactTimeout = 15 * time.Second
const toolResultPruneThresholdChars = 400

// maxCompactOutputChars caps the summarization output to approximately
// 20K tokens (~4 chars per token). Aligned with Claude Code's
// MAX_OUTPUT_TOKENS_FOR_SUMMARY and Codex's COMPACT_USER_MESSAGE_MAX_TOKENS.
// Without this cap, the summary itself can consume a large portion of
// the context window, defeating the purpose of compaction.
const maxCompactOutputChars = 80_000

func compactTimeout() time.Duration {
	if v := os.Getenv("WUU_COMPACT_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return defaultCompactTimeout
}

func withCompactTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := compactTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining <= timeout {
			return ctx, func() {}
		}
	}
	return context.WithTimeout(ctx, timeout)
}

// EstimateTokens provides a rough token count estimate.
// English: ~4 chars per token. CJK: ~2 chars per token.
// This is for display only; API returns precise counts.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	cjkCount := 0
	totalChars := utf8.RuneCountInString(text)
	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		}
	}

	nonCJK := totalChars - cjkCount
	return (nonCJK / 4) + (cjkCount / 2) + 1
}

// EstimateMessagesTokens estimates total tokens for a message list.
func EstimateMessagesTokens(messages []providers.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg.Content)
		total += 4 // per-message overhead (role, separators)
		for _, tc := range msg.ToolCalls {
			total += EstimateTokens(tc.Name)
			total += EstimateTokens(tc.Arguments)
		}
	}
	return total
}

// ShouldCompact returns true if messages exceed the threshold.
func ShouldCompact(messages []providers.ChatMessage, maxContextTokens int) bool {
	if maxContextTokens <= 0 {
		return false
	}
	estimated := EstimateMessagesTokens(messages)
	threshold := int(float64(maxContextTokens) * 0.8)
	return estimated > threshold
}

// maxCompactRetries caps how many times Compact will defensively trim
// the oldest message and re-issue the summarization request after
// hitting a context-overflow on the compact request itself. Aligned
// with Codex CLI's safeguard.
const maxCompactRetries = 3

// Compact compresses older messages into a summary. It finds an
// appropriate boundary near the end of the conversation, summarizes
// everything before it, and returns the compacted message list.
//
// Defensive trimming: if the summarization request itself overflows
// the model's context window (because the conversation being
// compacted is itself enormous), Compact drops the oldest entry from
// the to-be-summarized slice and retries up to maxCompactRetries
// times. This prevents the "compact → overflow → compact again →
// overflow again" deadlock the simple form is vulnerable to.
func Compact(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string) ([]providers.ChatMessage, error) {
	if len(messages) <= 2 {
		return messages, nil // nothing to compact
	}
	ctx, cancel := withCompactTimeout(ctx)
	defer cancel()

	// Find compaction boundary: keep the last 2 exchanges (4 messages)
	keepCount := 4
	keepStart := compactKeepStart(messages, keepCount)
	if keepStart <= 0 {
		return messages, nil
	}

	toSummarize := pruneOldToolResults(messages[:keepStart])
	toKeep := messages[keepStart:]

	for attempt := 0; ; attempt++ {
		summaryInput := buildSummaryPrompt(toSummarize)
		summaryReq := providers.ChatRequest{
			Model: model,
			Messages: []providers.ChatMessage{
				{Role: "user", Content: summaryInput},
			},
			Temperature: 0.3,
		}

		resp, err := client.Chat(ctx, summaryReq)
		if err != nil {
			// If the summary request itself overflowed the model's
			// context window, drop the oldest message from the slice
			// being summarized and try again. This is the "compact-
			// of-compact" backstop borrowed from Codex CLI.
			if providers.IsContextOverflow(err) && attempt < maxCompactRetries && len(toSummarize) > 1 {
				toSummarize = toSummarize[1:]
				continue
			}
			return messages, fmt.Errorf("compact summary failed: %w", err)
		}

		summary := strings.TrimSpace(resp.Content)
		if len(summary) > maxCompactOutputChars {
			cut := maxCompactOutputChars
			for cut > 0 && summary[cut-1]&0xC0 == 0x80 {
				cut--
			}
			summary = summary[:cut]
		}
		if summary == "" {
			return messages, nil
		}

		compacted := []providers.ChatMessage{
			{Role: "system", Content: fmt.Sprintf("[Conversation summary]\n%s", summary)},
		}
		compacted = append(compacted, toKeep...)
		return compacted, nil
	}
}

// compactKeepStart returns the index where the un-compacted tail should begin.
// The boundary must not split an assistant tool_call block from its tool
// results, or the resulting history becomes invalid for chat-completions APIs.
func compactKeepStart(messages []providers.ChatMessage, keepCount int) int {
	if keepCount >= len(messages) {
		return 0
	}

	start := len(messages) - keepCount
	if messages[start].Role != "tool" {
		return start
	}

	// Boundary landed inside a tool-result block. Shift left to include every
	// contiguous tool result and the assistant tool_calls turn that started it.
	for start > 0 && messages[start-1].Role == "tool" {
		start--
	}
	if start > 0 && messages[start-1].Role == "assistant" && len(messages[start-1].ToolCalls) > 0 {
		start--
	}
	return start
}

func pruneOldToolResults(messages []providers.ChatMessage) []providers.ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	pruned := make([]providers.ChatMessage, len(messages))
	copy(pruned, messages)

	for i := range pruned {
		if pruned[i].Role != "tool" {
			continue
		}
		if len(pruned[i].Content) < toolResultPruneThresholdChars {
			continue
		}
		pruned[i].Content = summarizePrunedToolResult(pruned[i])
	}

	return pruned
}

func summarizePrunedToolResult(msg providers.ChatMessage) string {
	name := strings.TrimSpace(msg.Name)
	if name == "" {
		name = "unknown tool"
	}
	return fmt.Sprintf("[Old %s result omitted during compact to save context. Original output was %d characters. Tool call ID: %s]", name, len(msg.Content), toolCallLabel(msg.ToolCallID))
}

func toolCallLabel(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unknown"
	}
	return id
}

// compactInstructionPrompt is the framing wuu wraps every
// summarization request in. It keeps the existing single-user-message
// compact flow, but tightens the handoff discipline so the generated
// summary can safely serve as the only continuation context.
//
// The load-bearing requirements are:
//   - no tool calls at all
//   - the response must start with <analysis> and then <summary>
//   - the summary must preserve enough detail to continue the work
//     without access to the pre-compact conversation
const compactInstructionPrompt = `You are summarizing a coding-agent conversation to preserve context for continuing the work later.

CRITICAL: This summary will be the ONLY context available when the conversation resumes. Assume every previous message is about to be deleted. Be thorough — losing a detail here means the next agent will have to ask the user (or guess) to recover it.

CRITICAL: Respond with text only. Do NOT call any tools. Do NOT use read_file, grep, glob, run_shell, or any other tool. Tool calls will fail this task.

Your response must contain exactly two top-level blocks, in this order:
1. <analysis>...</analysis>
2. <summary>...</summary>

Use the <analysis> block to think through the conversation chronologically and make sure you did not miss anything load-bearing.

In the <summary> block, cover at least these sections:

## User Intent
- The user's exact request, constraints, preferences, and success criteria
- Any course corrections or explicit feedback from the user

## Technical Concepts
- Important technical concepts, design decisions, libraries, frameworks, and conventions

## Files and Code
- Files modified, with a one-line description of each change
- Files read or analyzed and why they mattered
- Important code snippets, function signatures, data shapes, and exact paths the next agent should inspect
- File paths and line numbers for code locations the next agent should jump to

## Errors and Fixes
- Errors encountered, what caused them, and how they were fixed or investigated
- Commands that failed and what the failure looked like
- Commands that worked and what they verified

## All User Messages
- Every user message that is not just a tool result, including short clarifications and corrections

## Unfinished Work
- Pending tasks, open questions, blockers, and assumptions that still matter

## Current Work
- What was being worked on immediately before this summary request
- How far along it is

## Next Step
- The next concrete step that should happen, written so another agent can continue directly

Tone: brief a teammate taking over mid-task. Include enough detail that they can continue without asking the user to repeat anything. No filler. No emojis.

--- Conversation to summarize ---

`

// buildSummaryPrompt is the inner formatting helper extracted so the
// retry loop above doesn't have to duplicate the string-builder code.
func buildSummaryPrompt(toSummarize []providers.ChatMessage) string {
	var b strings.Builder
	b.WriteString(compactInstructionPrompt)
	for _, msg := range toSummarize {
		fmt.Fprintf(&b, "[%s]: %s\n", msg.Role, truncate(msg.Content, 500))
		for _, tc := range msg.ToolCalls {
			fmt.Fprintf(&b, "  -> tool_call: %s(%s)\n", tc.Name, truncate(tc.Arguments, 200))
		}
		if msg.ToolCallID != "" {
			fmt.Fprintf(&b, "  (result for tool call %s)\n", msg.ToolCallID)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
