package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Fall back once, without re-entering the note provider. Cancellation is not a
// recovery request, and failed or unchanged output must never replace history.
func fallbackAfterNoteFailure(ctx context.Context, messages []providers.ChatMessage, cause error, fallback CompactFn) ([]providers.ChatMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fallback == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return nil, cause
	}
	replacement, err := fallback(ctx, providers.CloneChatMessages(messages))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil {
		err = providers.ValidateToolCallHistory(replacement)
	}
	if err == nil && (len(replacement) == 0 || estimateFreshContextMessages(replacement) >= estimateFreshContextMessages(messages)) {
		err = errors.New("traditional compaction did not produce a smaller nonempty context")
	}
	if err != nil {
		return nil, errors.Join(cause, fmt.Errorf("traditional compaction fallback: %w", err))
	}
	return replacement, nil
}
