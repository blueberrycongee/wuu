package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// An isolated review must not lift an owner's interrupt freeze or consume
// unrelated completion obligations. Ordinary resumption still folds the tree.
func TestPluginTurnToolPolicyPreservesFrozenWorkerTree(t *testing.T) {
	for _, isolated := range []bool{true, false} {
		name := "ordinary"
		if isolated {
			name = "isolated"
		}
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			client := &fakeClient{response: providersResponse("done"), onChat: func(_ int, _ providers.ChatRequest) { close(started); <-release }}
			rt := newTestRuntime(t, client)
			owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
			attachTestModelGeneration(t, rt, owner)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(func() { unblock(); srv.Close() })
			ctx := pluginInvocationContext(t, rt, context.Background())
			created, err := srv.createPluginSession(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), created.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			th.mu.Lock()
			th.workerTreeFrozen = true
			th.mu.Unlock()
			srv.agentCompletionMu.Lock()
			srv.pendingAgentCompletionTurns[created.SessionID] = []agentCompletionTurn{{agentID: "unrelated-worker", resultID: "unrelated-result", kind: agentCompletionTurnKindCompletion, msg: providers.ChatMessage{Role: "user", Content: "unrelated result"}}}
			srv.agentCompletionMu.Unlock()
			params := pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: "next", Input: pluginhost.SessionInput{Prompt: "next"}}
			if isolated {
				params.ToolPolicy = &pluginhost.SessionTurnToolPolicy{Allow: []string{}}
			}
			if _, err := srv.sendPluginSession(ctx, owner.id, params); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not start")
			}
			th.mu.Lock()
			frozen := th.workerTreeFrozen
			th.mu.Unlock()
			srv.agentCompletionMu.Lock()
			pending := len(srv.pendingAgentCompletionTurns[created.SessionID])
			srv.agentCompletionMu.Unlock()
			if frozen != isolated {
				t.Fatalf("frozen=%v, isolated=%v", frozen, isolated)
			}
			expected := 0
			if isolated {
				expected = 1
			}
			if pending != expected {
				t.Fatalf("pending obligations=%d, want %d", pending, expected)
			}
		})
	}
}

// A failed ordinary admission can leave previously folded results staged.
// Skipping a new fold is insufficient: neither staging field may be consumed.
func TestPluginTurnToolPolicyPreservesPreviouslyStagedWorkerResults(t *testing.T) {
	for _, isolated := range []bool{true, false} {
		name := "ordinary"
		if isolated {
			name = "isolated"
		}
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			client := &fakeClient{response: providersResponse("done"), onChat: func(_ int, _ providers.ChatRequest) { close(started); <-release }}
			rt := newTestRuntime(t, client)
			owner := &pluginTurnLifecycleClient{id: "observer", calls: make(chan pluginhost.AgentTurnLifecycleInput, 16)}
			attachTestModelGeneration(t, rt, owner)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(func() { unblock(); srv.Close() })
			ctx := pluginInvocationContext(t, rt, context.Background())
			created, err := srv.createPluginSession(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "create", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), created.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			th.mu.Lock()
			th.frozenTreeContext = agent.RequestOnlyContextMessages([]providers.ChatMessage{{Role: "user", Content: "previously folded worker context"}})
			th.frozenTreeResultIDs = []string{"previously-folded-result"}
			th.mu.Unlock()
			params := pluginhost.SessionSendParams{SessionID: created.SessionID, RequestID: "next", Input: pluginhost.SessionInput{Prompt: "next"}}
			if isolated {
				params.ToolPolicy = &pluginhost.SessionTurnToolPolicy{Allow: []string{}}
			}
			if _, err := srv.sendPluginSession(ctx, owner.id, params); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not start")
			}
			th.mu.Lock()
			contexts, results := len(th.frozenTreeContext), len(th.frozenTreeResultIDs)
			th.mu.Unlock()
			expected := 0
			if isolated {
				expected = 1
			}
			client.mu.Lock()
			sawContext := false
			for _, message := range client.requests[0].Messages {
				if message.Content == "previously folded worker context" {
					sawContext = true
				}
			}
			client.mu.Unlock()
			if sawContext == isolated {
				t.Fatalf("worker context included=%v, isolated=%v", sawContext, isolated)
			}
			if contexts != expected || results != expected {
				t.Fatalf("staged context/results = %d/%d, want %d/%d", contexts, results, expected, expected)
			}
		})
	}
}
