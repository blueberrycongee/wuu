package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// A callback may be canceled before its independently retained turn owner.
// The callback's host-authored cause must cross the gateway unchanged.
func TestDriverGatewayCallbackCancellationCause(t *testing.T) {
	for _, cause := range []error{errors.New("host shutdown proof"), context.Canceled} {
		gateway := newStreamTurnGateway(context.Background(), &StreamRunner{}, loopdriver.PersistedInput{}, nil)
		caller, cancel := context.WithCancelCause(context.Background())
		scoped, release, err := gateway.acquire(caller)
		if err != nil {
			t.Fatal(err)
		}
		cancel(cause)
		select {
		case <-scoped.Done():
		case <-time.After(time.Second):
			t.Fatal("callback cancellation not propagated")
		}
		if context.Cause(scoped) != cause {
			t.Fatalf("got cause %v, want %v", context.Cause(scoped), cause)
		}
		release()
		gateway.close()
	}
}
