package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type observedTurnCompletion struct {
	input  pluginhost.AgentTurnCompletedInput
	output string
}

type turnCompletionObserver struct {
	isolatedFailureClient
	out   *lockedBuffer
	calls chan observedTurnCompletion
}

func (c *turnCompletionObserver) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.AgentTurnCompletedInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	c.calls <- observedTurnCompletion{input: input, output: c.out.String()}
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

func TestTurnCompletionObserverUsesSettledOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		failPersist bool
		interrupt   bool
		status      TurnStatus
	}{
		{name: "completed", status: TurnStatusCompleted},
		{name: "interrupted", interrupt: true, status: TurnStatusInterrupted},
		{name: "terminal persistence failure", failPersist: true, status: TurnStatusFailed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := &turnContextClient{started: make(chan context.Context, 2), release: make(chan struct{})}
			rt := newTestRuntime(t, &fakeClient{})
			fault := &archiveSettlementFaultDocuments{Documents: rt.Documents, terminal: true}
			rt.Documents, rt.Sessions = fault, session.NewStore(context.Background(), fault)
			rt.StreamRunner.Client = providers.AdaptStreamClient(client)
			out := &lockedBuffer{}
			observer := &turnCompletionObserver{isolatedFailureClient: isolatedFailureClient{id: "outcome-observer"}, out: out, calls: make(chan observedTurnCompletion, 4)}
			attachTestModelGeneration(t, rt, observer)
			srv := New(rt, out)
			t.Cleanup(func() {
				select {
				case <-client.release:
				default:
					close(client.release)
				}
				srv.Close()
			})
			dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
			id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
			dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "work"})
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not reach provider")
			}
			if scenario.failPersist {
				dispatchPayload(t, srv, "queue", MethodTurnQueue, TurnQueueParams{ThreadID: id, Prompt: "wait for durable settlement", ClientID: "held-successor"})
				if response := responseByID(t, parseOutput(t, out.String()), "queue"); response["error"] != nil {
					t.Fatalf("queue failed: %+v", response)
				}
				fault.armed.Store(true)
			}
			if scenario.interrupt {
				dispatchPayload(t, srv, "interrupt", MethodTurnInterrupt, TurnInterruptParams{ThreadID: id})
			}
			close(client.release)
			var observation observedTurnCompletion
			select {
			case observation = <-observer.calls:
			case <-time.After(5 * time.Second):
				t.Fatalf("settled observation missing: %s", out.String())
			}
			if observation.input.ThreadID != id || observation.input.TurnID == "" || observation.input.Succeeded != (scenario.status == TurnStatusCompleted) {
				t.Fatalf("observer outcome = %+v, want %s", observation.input, scenario.status)
			}
			terminalSeen := false
			for _, message := range parseOutput(t, observation.output) {
				if message["method"] != NotificationTurnCompleted && message["method"] != NotificationTurnError {
					continue
				}
				params := remarshal[struct{ Turn Turn }](t, message["params"])
				if params.Turn.ID == observation.input.TurnID && params.Turn.Status == scenario.status {
					terminalSeen = true
				}
			}
			if !terminalSeen {
				t.Fatalf("observer preceded the matching terminal notification: %s", observation.output)
			}
			if scenario.failPersist {
				held, err := rt.Sessions.LoadHeldUserWork(id)
				if err != nil || len(held) != 1 || held[0].ID != "held-successor" {
					t.Fatalf("failed settlement lost queued input: %+v, %v", held, err)
				}
			}
			srv.Close()
			select {
			case extra := <-observer.calls:
				t.Fatalf("duplicate or premature successor observation: %+v", extra.input)
			default:
			}
			select {
			case <-client.started:
				t.Fatal("queued input ran after failed settlement")
			default:
			}
		})
	}
}
