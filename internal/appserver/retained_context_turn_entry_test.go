package appserver

import (
	"context"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// recordingStreamClient is a minimal StreamClient that answers every request
// with a single content chunk, so a turn completes in one round.
type recordingStreamClient struct {
	requests []providers.ChatRequest
}

func (c *recordingStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.requests = append(c.requests, req)
	return providers.ChatResponse{Content: "ok"}, nil
}

func (c *recordingStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.requests = append(c.requests, req)
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "ok"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

// TestEnsureThreadRuntimeAfterAdmission_PreservesRetainedContext locks in the
// desktop turn-entry wiring: ensureThreadRuntimeAfterAdmission re-seeds the
// conversation usage baseline on every turn, and that reseed must not discard
// the cross-turn request-context state the previous turn produced — otherwise
// prompt-cache continuity breaks on every new turn (the 0.3.2 regression).
func TestEnsureThreadRuntimeAfterAdmission_PreservesRetainedContext(t *testing.T) {
	client := &recordingStreamClient{}
	runner := &agent.StreamRunner{
		LoopDriver: agent.DefaultDriver{},

		Client: client,
		Model:  "m",
		BeforeRequestContext: func() []agent.ContextSegment {
			return agent.RequestOnlyContextBlocks([]wuucontext.Block{{
				Kind:    wuucontext.BlockActiveFiles,
				Title:   "Active files",
				Source:  "runtime.active_files",
				Content: "files:\n- go.mod",
			}})
		},
	}

	// Run one real turn so the runner holds retained request-context state.
	history := []providers.ChatMessage{{Role: "user", Content: "first ask"}}
	res, err := runner.RunWithCallback(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	turn1Last := client.requests[len(client.requests)-1].Messages

	// Build the thread the way the server holds it between turns, with the
	// persistent runtime carrying the same runner.
	th := &threadState{
		ID:          "thread-1",
		History:     append(append([]providers.ChatMessage(nil), history...), res.NewMessages...),
		execRuntime: &runtime.ThreadRuntime{StreamRunner: runner},
	}
	srv := &Server{rt: newTestRuntime(t, &fakeClient{})}
	th.state = testRuntimeState(t, srv.rt)

	if _, err := srv.ensureThreadRuntimeAfterAdmission(context.Background(), th); err != nil {
		t.Fatalf("ensureThreadRuntimeAfterAdmission: %v", err)
	}

	history2 := append(cloneHistory(th.History), providers.ChatMessage{Role: "user", Content: "second ask"})
	if _, err := runner.RunWithCallback(context.Background(), history2, nil); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	turn2First := client.requests[len(client.requests)-1].Messages
	if len(turn2First) < len(turn1Last) || !reflect.DeepEqual(turn1Last, turn2First[:len(turn1Last)]) {
		t.Fatalf("app-server turn entry broke retained request prefix:\nturn1=%+v\nturn2=%+v", turn1Last, turn2First)
	}
}
