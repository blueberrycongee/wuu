package tui

import "github.com/blueberrycongee/wuu/internal/providers"

func normalizeChatHistory(msgs []providers.ChatMessage) []providers.ChatMessage {
	return providers.NormalizeMessages(msgs)
}
