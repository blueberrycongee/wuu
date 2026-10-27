package appserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// blockedPeersRegressionClient holds a capability until released or revoked,
// making publication overlap an accepted callback without scheduling sleeps.
type blockedPeersRegressionClient struct {
	id         string
	capability pluginhost.CapabilityDescriptor
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
}

func (c *blockedPeersRegressionClient) ID() string { return c.id }
func (c *blockedPeersRegressionClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (c *blockedPeersRegressionClient) Close(context.Context) error { return nil }
func (c *blockedPeersRegressionClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *blockedPeersRegressionClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{c.capability}
}
func (c *blockedPeersRegressionClient) InvokeCapability(ctx context.Context, _ pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

// TestDisablingPeersCannotBlockOrdinaryMessageSends is the end-to-end
// disable-peers/ordinary-send regression: a queued peers-owned turn observes
// its lifecycle against a blocked helper, peers is then disabled mid-flight,
// and an ordinary message on the existing session must still complete. The
// turn drain must never synchronously re-enter the blocked helper, and the
// now-undeliverable terminal lifecycle event must be dropped from the outbox.
func TestDisablingPeersCannotBlockOrdinaryMessageSends(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{providersResponse("peer-done"), providersResponse("user-done")}}
	rt := newTestRuntime(t, client)
	rt = newReloadBehaviorRuntime(t, rt, client)
	cfg, _, err := rt.LoadEffectiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	peers := &blockedPeersRegressionClient{
		id: "peers", capability: pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityAgentTurnLifecycle, Kind: pluginhost.SeamObserve, Version: 1},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	preStep := &blockedPeersRegressionClient{
		id: "goal", capability: pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityAgentPreStep, Kind: pluginhost.SeamTransform, Version: 1},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	// Keep a foreground callback in flight across publication. A truncated
	// catalog would incorrectly revoke this unrelated provider along with peers.
	if outcome, found := rt.PluginHost.RetirePlugin(context.Background(), preStep.ID(), nil); outcome.Err != nil || !found {
		t.Fatalf("replace foreground pre-step provider: found=%t err=%v", found, outcome.Err)
	}
	rt.PluginHost.Add(preStep)
	releasePreStep := sync.OnceFunc(func() { close(preStep.release) })
	t.Cleanup(releasePreStep)
	// Replace the bundled observer so the queued turn reaches this blocked fixture.
	if outcome, _ := rt.PluginHost.RetirePlugin(context.Background(), peers.ID(), nil); outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	rt.PluginHost.Add(peers)
	// Preserve the complete discovered catalog and its real policy identity.
	// Publication must disable only peers, not remove unrelated providers.
	peersSubject := ""
	for _, item := range rt.Plugins {
		if item.ID == peers.ID() {
			peersSubject = item.SubjectID
			break
		}
	}
	if peersSubject == "" {
		t.Fatal("peers package is missing from the discovered catalog")
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	binding := testRuntimeState(t, rt)
	t.Cleanup(func() {
		close(peers.release)
		releasePreStep()
		srv.Close()
	})

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	if threadID == "" {
		t.Fatal("expected thread id")
	}

	// Queue a peers-owned turn whose lifecycle observation will block.
	srv.pendingQueuedTurns[threadID] = []queuedTurn{{
		id:  "peers-turn",
		msg: providers.ChatMessage{Role: "user", Content: "hello"},
		snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{
			PluginID: peers.ID(), RequestID: "peer-req-1", QueueID: "peer-queue-1",
		}},
	}}

	drained := make(chan struct{})
	go func() {
		srv.drainQueuedTurns(binding, threadID)
		close(drained)
	}()

	// The helper is now blocked inside a lifecycle observation. The drain must
	// schedule delivery in the background and keep going.
	select {
	case <-peers.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("peers lifecycle observer was never invoked")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("turn drain synchronously re-entered the blocked peers helper")
	}

	select {
	case <-preStep.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground pre-step callback was never invoked")
	}

	// Disable peers through the real publication boundary while its turn is
	// active. Retained callback leases must not delay explicit revocation.
	settings := cfg.Extensions
	if settings == nil {
		settings = &extensions.Settings{}
	}
	settings.SetEnabled(peersSubject, false)
	cfg.Extensions = settings
	candidate, err := rt.PreflightExtensionPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}

	if _, found := binding.Host.Capability(preStep.ID(), pluginhost.CapabilityAgentPreStep); !found {
		t.Fatal("disabling peers retired an unrelated foreground pre-step provider")
	}
	if _, found := binding.Host.Capability(peers.ID(), pluginhost.CapabilityAgentTurnLifecycle); found {
		t.Fatal("disabled peers retained its lifecycle callback")
	}
	releasePreStep()

	// The peers-owned turn itself still completes; its terminal lifecycle
	// event is undeliverable and must be dropped instead of replayed forever.
	waitForTurnCompletionContent(t, out, threadID, "peer-done")
	waitForEmptyLifecycleOutbox(t, binding.Sessions)

	// Ordinary send on the existing session must complete.
	raw, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodTurnStart,
		"params": TurnStartParams{ThreadID: threadID, Prompt: "still alive?"},
	})
	if err != nil {
		t.Fatalf("marshal turn request: %v", err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("turn/start: %v", err)
	}
	waitForTurnCompletionContent(t, out, threadID, "user-done")
}

func waitForTurnCompletionContent(t *testing.T, out *lockedBuffer, threadID, content string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range parseOutput(t, out.String()) {
			if msg["method"] != NotificationTurnCompleted {
				continue
			}
			params := remarshal[TurnCompletedNotification](t, msg["params"])
			if params.ThreadID == threadID && params.Content == content {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for turn completion %q on thread %q; output:\n%s", content, threadID, out.String())
}

func waitForEmptyLifecycleOutbox(t *testing.T, store *session.Store) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var entries []session.PluginTurnLifecycleOutboxEntry
	for time.Now().Before(deadline) {
		var err error
		entries, err = store.ListPluginTurnLifecycleOutbox()
		if err != nil {
			t.Fatalf("list plugin turn lifecycle outbox: %v", err)
		}
		if len(entries) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("undeliverable lifecycle outbox entries were not dropped: %+v", entries)
}
