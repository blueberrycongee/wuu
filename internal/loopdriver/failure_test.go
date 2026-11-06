package loopdriver

import (
	"context"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"testing"
)

// Wire round trips must preserve the no-replay outcome, even with cancellation.
func TestFailureWirePreservesUnknownToolEffect(t *testing.T) {
	wire := FailureToWire(fmt.Errorf("execute: %w", toolerrors.Unknown(context.Canceled)))
	if wire.Code != toolerrors.UncertainEffect {
		t.Fatalf("lost stable code: %+v", wire)
	}
	decoded := &FailureError{Failure: *wire}
	if toolerrors.Kind(decoded) != toolerrors.UncertainEffect || !errors.Is(decoded, context.Canceled) {
		t.Fatalf("lost unknown cancellation: %v", decoded)
	}
}
