package compact

import (
	"strconv"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	legacyContextAnchorMessageName = "wuu_context_anchor"
	legacyContextSummaryName       = "wuu_context_continuation"
	legacyContextAnchorPrefix      = "[Wuu context checkpoint]"
	legacyContextSummaryPrefix     = "[Wuu context continuation]"
)

// IsInternalContextMessage recognizes persisted artifacts from the retired
// context-rewrite path. It exists only so old sessions can be loaded without
// surfacing hidden bookkeeping as user conversation. It does not interpret
// tool results or provide any history-rewrite behavior.
func IsInternalContextMessage(msg providers.ChatMessage) bool {
	name := strings.TrimSpace(msg.Name)
	return name == legacyContextAnchorMessageName ||
		name == legacyContextSummaryName ||
		isLegacyContextAnchorContent(msg.Content) ||
		strings.HasPrefix(unwrapLegacyInternalContextContent(msg.Content), legacyContextSummaryPrefix)
}

func isLegacyContextAnchorContent(content string) bool {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "<system>") && strings.HasSuffix(content, "</system>") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "<system>"), "</system>"))
		fields := strings.Fields(inner)
		if len(fields) == 2 && strings.EqualFold(fields[0], "CHECKPOINT") {
			id, err := strconv.Atoi(fields[1])
			return err == nil && id >= 0
		}
	}

	lines := strings.Split(unwrapLegacyInternalContextContent(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != legacyContextAnchorPrefix {
		return false
	}
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(key) != "anchor_id" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(value))
		return err == nil && id >= 0
	}
	return false
}

func unwrapLegacyInternalContextContent(content string) string {
	content = strings.TrimSpace(content)
	const open = "<system-reminder>"
	const close = "</system-reminder>"
	if strings.HasPrefix(content, open) && strings.HasSuffix(content, close) {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, open), close))
	}
	return strings.TrimSpace(content)
}
