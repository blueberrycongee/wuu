package tools

import (
	"context"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// executeEnvelope runs a tool and returns its producer payload (the JSON
// envelope clients and durable records consume), not the settled model view.
func executeEnvelope(kit *Toolkit, ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := kit.ExecuteResult(ctx, call)
	return producerText(result), err
}

func lineCount(s string) int {
	if strings.TrimRight(s, "\n") == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}
