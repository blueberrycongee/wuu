package appserver

import (
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/titleservice"
)

// Existing native-policy regressions now exercise the default provider code.
const titleMaxRuneLength = titleservice.MaxRuneLength

func recommendedTitleTemperature(model string) (float64, bool) {
	return titleservice.RecommendedTemperature(model)
}
func cleanGeneratedThreadTitle(text string) string { return titleservice.Clean(text) }
func firstUserMessageForTitle(history []providers.ChatMessage, force bool) (string, bool) {
	var messages []string
	for _, message := range history {
		if isThreadTitleUserMessage(message) {
			messages = append(messages, chatMessageDisplayContent(message))
		}
	}
	return titleservice.FirstHumanMessage(messages, force)
}
