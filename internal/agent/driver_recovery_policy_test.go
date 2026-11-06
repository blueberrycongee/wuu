package agent

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// An optional durable-loop retry owner must shrink the host's operation budget,
// not multiply two retry loops, change native defaults, or admit a caller-forged
// operation identity. Exercise the real gateway and selected stream client.
func TestDriverModelSingleAttemptKeepsHostIdentityAndDefault(t *testing.T) {
	for _, single := range []bool{false, true} {
		name := "default"
		if single {
			name = "single-attempt"
		}
		t.Run(name, func(t *testing.T) {
			client := &mockStreamClient{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "Finished."},
				{Type: providers.EventDone, FinishReason: providers.FinishReasonStop},
			}}
			runner := &StreamRunner{Client: client, Model: "controlled"}
			gateway := newStreamTurnGateway(context.Background(), runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "one response"}}}, nil)
			defer gateway.close()
			request, err := gateway.ReadTurn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			request.Operation = providers.InferenceOperation{ID: "caller-forged", AttemptLimit: 999}
			ctx := context.Background()
			if single {
				ctx = loopdriver.WithModelSingleAttempt(ctx)
			}
			if _, err = gateway.CallModel(ctx, request); err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 1 {
				t.Fatalf("model request count = %d", len(client.requests))
			}
			operation := client.requests[0].Operation
			want := providers.RetryConfigForProfile(providers.InferenceProfileInteractive).MaxRetries + 1
			if single {
				want = 1
			}
			if operation.ID == "" || operation.ID == "caller-forged" || operation.AttemptLimit != want {
				t.Fatalf("host operation identity/budget changed: %+v; want attempts=%d", operation, want)
			}
		})
	}
}
