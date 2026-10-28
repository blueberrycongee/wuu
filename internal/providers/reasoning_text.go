package providers

import "strings"

// StripThinkingText removes inline <think>…</think> reasoning fences emitted by
// models that surface chain-of-thought as plain text instead of a structured
// reasoning channel.
func StripThinkingText(text string) string {
	for {
		lower := strings.ToLower(text)
		start := strings.Index(lower, "<think>")
		if start < 0 {
			return text
		}
		endRel := strings.Index(lower[start:], "</think>")
		if endRel < 0 {
			return text[:start]
		}
		end := start + endRel + len("</think>")
		text = text[:start] + text[end:]
	}
}
