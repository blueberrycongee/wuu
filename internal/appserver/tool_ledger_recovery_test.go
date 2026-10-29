package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

func TestThreadRuntimeOnlyReconcilesToolLedgerAfterExecutionAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := rt.Sessions.CreateWithMetadata("ledger-admission", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(rt.Documents, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	batchID, err := ledger.BeginBatch(ctx, "operation-live", 1)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := ledger.Prepare(ctx, batchID, providers.ToolCall{
		ID: "call-live", Name: "write_file", Arguments: `{"path":"important.go"}`,
	}, toolledger.ReplayAtMostOnce)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.FinalizeBatch(ctx, batchID); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Start(ctx, invocation.ID); err != nil {
		t.Fatal(err)
	}

	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ensureThreadRuntime(th); err != nil {
		t.Fatal(err)
	}
	assertToolInvocationState(t, rt.Documents, invocation.ID, toolledger.InvocationRunning)

	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	th.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("thread execution lease was not acquired")
	}
	defer func() {
		th.mu.Lock()
		th.releaseThreadExecutionLeaseLocked()
		th.mu.Unlock()
	}()
	if _, err := srv.ensureThreadRuntimeAfterAdmission(context.Background(), th); err != nil {
		t.Fatal(err)
	}
	assertToolInvocationState(t, rt.Documents, invocation.ID, toolledger.InvocationInterruptedUnknown)
}

func assertToolInvocationState(t *testing.T, documents persistence.Documents, invocationID string, want toolledger.InvocationState) {
	t.Helper()
	var got toolstate.Invocation
	err := persistence.Update(context.Background(), documents, func(tx *persistence.Tx) error {
		found, err := tx.Get(toolstate.Invocations, invocationID, &got)
		if err == nil && !found {
			t.Fatalf("missing tool invocation %s", invocationID)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(want) {
		t.Fatalf("tool invocation state = %q, want %q", got.State, want)
	}
}

type lateTurnClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *lateTurnClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}

func (c *lateTurnClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, 2)
	close(c.started)
	go func() {
		defer close(ch)
		<-c.release
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "stale output"}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

func TestForceReleasedTurnCannotPersistLateCompletion(t *testing.T) {
	client := &lateTurnClient{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	attachTestModelGeneration(t, rt)
	sess, err := rt.Sessions.CreateWithMetadata("late-turn", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	turnID := session.NewID()
	userMessage := providers.ChatMessage{Role: "user", Content: "start"}
	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	if err == nil && acquired {
		th.startTurnLocked(turnID, userMessage, time.Now().UTC())
	}
	baseline := len(th.History)
	th.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("thread execution lease was not acquired")
	}

	threadRuntime, err := srv.ensureThreadRuntimeAfterAdmission(context.Background(), th)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { close(client.release) })
	t.Cleanup(release)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.runTurn(th.state, context.Background(), th, threadRuntime, turnID, turnRuntimeSnapshot{
			ProviderName: rt.ProviderName,
			Model:        rt.Model,
		}, []providers.ChatMessage{userMessage})
	}()
	select {
	case <-client.started:
	case <-done:
		t.Fatal("turn finished before reaching the model")
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}

	forceReleaseAbandonedThreadExecutions([]*threadState{th})
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("late turn did not finish")
	}

	th.mu.Lock()
	defer th.mu.Unlock()
	if len(th.History) != baseline {
		t.Fatalf("force-released turn changed history: %+v", th.History)
	}
	if th.currentTurn != "" || th.executionLease != nil {
		t.Fatalf("force-released turn reclaimed ownership: current=%q lease=%p", th.currentTurn, th.executionLease)
	}
}
