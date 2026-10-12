package appserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// stuckPeersLifecycleClient simulates a peers helper whose single ordered
// worker is blocked: lifecycle observations enter InvokeCapability and stay
// there until released. Any synchronous re-entry from the turn drain into
// this helper deadlocks, so the drain must never make that call inline.
type stuckPeersLifecycleClient struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *stuckPeersLifecycleClient) ID() string { return "peers" }
func (c *stuckPeersLifecycleClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (c *stuckPeersLifecycleClient) Close(context.Context) error { return nil }
func (c *stuckPeersLifecycleClient) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *stuckPeersLifecycleClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentTurnLifecycle, Kind: pluginhost.SeamObserve, Version: 1}}
}
func (c *stuckPeersLifecycleClient) InvokeCapability(ctx context.Context, _ pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-c.release:
		return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
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
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	rt.Toolkit = kit
	peers := &stuckPeersLifecycleClient{entered: make(chan struct{}), release: make(chan struct{})}
	rt.PluginHost = pluginhost.New(peers)
	rt.Plugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: peers.ID()}, SubjectID: "plugin:peers", Official: true}}
	rt.ActivePlugins = rt.Plugins
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() {
		close(peers.release)
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
		srv.drainQueuedTurns(threadID)
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

	// Disable peers through the real publication boundary while its turn is
	// active. Retained callback leases must not delay explicit revocation.
	settings := &extensions.Settings{}
	settings.SetEnabled("plugin:peers", false)
	candidate, err := rt.PreflightExtensionPolicy(config.Config{Extensions: settings})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}

	// The peers-owned turn itself still completes; its terminal lifecycle
	// event is undeliverable and must be dropped instead of replayed forever.
	waitForTurnCompletionContent(t, out, threadID, "peer-done")
	waitForEmptyLifecycleOutbox(t, rt.SessionDir)

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

func waitForEmptyLifecycleOutbox(t *testing.T, sessionDir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var entries []session.PluginTurnLifecycleOutboxEntry
	for time.Now().Before(deadline) {
		var err error
		entries, err = session.ListPluginTurnLifecycleOutbox(sessionDir)
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
