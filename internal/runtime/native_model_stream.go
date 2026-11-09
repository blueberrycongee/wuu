package runtime

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// The adapter callback owns its receipt handle until all transport events and
// cleanup finish. Unlike a logical text consumer, it must drain after a terminal
// event or cancellation before returning and revoking that physical authority.
func collectNativeModelStream(ctx context.Context, stream <-chan providers.StreamEvent, model string) (providers.ChatResponse, error) {
	accumulated := modelResponseAccumulator{model: model}
	var resultErr error
	done := false
	for event := range stream {
		if done || resultErr != nil {
			continue
		}
		accumulated.add(event)
		switch event.Type {
		case providers.EventError:
			resultErr = event.Error
			if resultErr == nil {
				resultErr = errors.New("model stream failed")
			}
		case providers.EventDone:
			done = true
		}
	}
	if err := ctx.Err(); err != nil {
		return accumulated.response, err
	}
	if resultErr != nil {
		return accumulated.response, resultErr
	}
	if !done {
		return accumulated.response, providers.NewIncompleteStreamError("model stream ended before completion")
	}
	return accumulated.response, nil
}
