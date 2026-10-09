package appserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A dequeue response precedes its durable cancellation receipt. Pause at that
// real boundary so shutdown cannot turn an acknowledged claim cancellation into
// a replayable delivery while the receipt writer is still in flight.
func TestClaimedPluginDeliveryCancellationSurvivesShutdownBeforeReceipt(t *testing.T) {
	out := &blockedCancellationResponse{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(out.release) }) }
	t.Cleanup(release)
	client := &fakeClient{}
	rt := newTestRuntime(t, client)
	rt.PluginHost = pluginhost.New()
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(context.Background(), "messenger", pluginhost.SessionCreateParams{
		RequestID: "target", Visibility: "user", ContextSource: "fresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.WuuHome = t.TempDir()
	advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	cancelDone := make(chan error, 1)
	srv.refreshExtensionsForTest = func(config.Config) error {
		params, err := json.Marshal(TurnDequeueParams{ThreadID: created.SessionID, QueueID: "claimed-reply"})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			cancelDone <- srv.handleTurnDequeue(Request{ID: json.RawMessage(`"cancel"`), Params: params})
		}()
		select {
		case <-out.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("dequeue did not reach its response boundary")
		}
		srv.Close()
		// Stop refresh before its inventory notification: the cancellation
		// response deliberately owns the transport until the assertion below.
		return errServerClosed
	}
	const requestID = "reply"
	entry := queuedTurn{
		id:       "claimed-reply",
		msg:      providers.ChatMessage{Role: "user", Content: "result", ClientID: pluginSessionRequestClientID("messenger", requestID)},
		snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "messenger", RequestID: requestID, QueueID: "claimed-reply"}},
	}
	if !srv.enqueueQueuedUserTurn(created.SessionID, entry) {
		t.Fatal("queue rejected")
	}
	srv.drainQueuedTurns(created.SessionID)
	if receipt, found, err := session.FindPluginTurnLifecycle(rt.SessionDir, "messenger", requestID, created.SessionID, ""); err != nil {
		t.Error(err)
	} else if found {
		t.Errorf("shutdown published a receipt while explicit cancellation was pending: %s", receipt.Payload)
	}
	release()
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dequeue did not finish")
	}
	receipt, found, err := session.FindPluginTurnLifecycle(rt.SessionDir, "messenger", requestID, created.SessionID, "")
	if err != nil || !found {
		t.Fatalf("cancellation receipt: %v %v", found, err)
	}
	var lifecycle pluginhost.AgentTurnLifecycleInput
	if err := json.Unmarshal(receipt.Payload, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle.State != pluginhost.TurnLifecycleDiscarded || lifecycle.Retryable {
		t.Fatalf("explicit cancellation became replayable: %+v", lifecycle)
	}
	assertFakeClientRequestCount(t, client, 0)
}

type blockedCancellationResponse struct {
	entered chan struct{}
	release chan struct{}
}

func (out *blockedCancellationResponse) Write(data []byte) (int, error) {
	var frame struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &frame) == nil && frame.ID == "cancel" {
		close(out.entered)
		<-out.release
	}
	return len(data), nil
}
