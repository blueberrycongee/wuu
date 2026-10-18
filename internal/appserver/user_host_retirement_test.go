package appserver

import (
	"context"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Disable must settle owned execution, while same-ID replacement and legacy
// Coding retain their existing work. Session identity must survive retirement.
func TestUserHostRetirementCancelsOnlyUnavailablePluginOwners(t *testing.T) {
	for _, tc := range []struct {
		name, owner                                       string
		userHost, enabled, running, wantCancel, wantQueue bool
	}{
		{name: "disabled active user owner", owner: "plugin:fixture", userHost: true, running: true, wantCancel: true},
		{name: "disabled idle queued user owner", owner: "plugin:fixture", userHost: true},
		{name: "same ID replacement remains enabled", owner: "plugin:fixture", userHost: true, enabled: true, running: true, wantQueue: true},
		{name: "legacy Coding remains unchanged", owner: "plugin:fixture", running: true, wantQueue: true},
		{name: "ordinary user owner is unaffected", owner: "user", userHost: true, running: true, wantQueue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			if tc.userHost {
				rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			}
			if tc.enabled {
				rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
			}
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			th := newThreadState("owned-session", []providers.ChatMessage{{Role: "user", Content: "Keep this conversation"}}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
			th.Owner, th.Visibility = tc.owner, pluginhost.SessionVisibilityPlugin
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if tc.running {
				th.running, th.currentTurn, th.cancel = true, "active-turn", cancel
			}
			srv.threads[th.ID] = th
			srv.pendingQueuedTurns[th.ID] = []queuedTurn{{id: "queued-work", snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "fixture", RequestID: "queued-request", QueueID: "queued-work"}}}}
			srv.retireIdlePluginRuntimes()
			if cancelled := ctx.Err() != nil; cancelled != tc.wantCancel {
				t.Fatalf("cancelled = %v, want %v", cancelled, tc.wantCancel)
			}
			srv.queuedTurnMu.Lock()
			queued := len(srv.pendingQueuedTurns[th.ID]) > 0
			srv.queuedTurnMu.Unlock()
			if queued != tc.wantQueue {
				t.Fatalf("queued work retained = %v, want %v", queued, tc.wantQueue)
			}
			if srv.thread(th.ID) != th || len(th.History) != 1 {
				t.Fatal("retirement replaced or erased the persistent conversation")
			}
		})
	}
}

func TestUserHostRetirementPreservesStoredMainBinding(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(context.Background(), "fixture", pluginhost.SessionCreateParams{RequestID: "main-binding", Name: "Fixture", Visibility: "plugin", ContextSource: "fresh", UserSpaceID: "local", ProductDomain: "personal", AgentID: "named-agent"})
	if err != nil {
		t.Fatal(err)
	}
	th, err := srv.ensureThreadLoaded(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	th.running, th.currentTurn, th.cancel = true, "main-turn", cancel
	srv.retireIdlePluginRuntimes()
	if ctx.Err() == nil {
		t.Fatal("disabled owner retained active main turn")
	}
	stored, found, err := session.Find(rt.SessionDir, created.SessionID)
	if err != nil || !found || stored.AgentID != "named-agent" || stored.Owner != "plugin:fixture" {
		t.Fatalf("persistent main binding changed: %+v, found %v, error %v", stored, found, err)
	}
}

func TestUserHostUnavailableOwnerRejectsTurnAdmission(t *testing.T) {
	for _, userHost := range []bool{true, false} {
		t.Run(map[bool]string{true: "user host rejects", false: "legacy Coding remains allowed"}[userHost], func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			if userHost {
				rt.UserSpaceID, rt.ProductDomain = "local", "personal"
			}
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			th := newThreadState("unavailable-owner", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
			th.Owner = "plugin:fixture"
			srv.threads[th.ID] = th
			started, admitted, err := srv.startThreadUserTurn(context.Background(), th, providers.ChatMessage{Role: "user", Content: "Do not revive a disabled owner"}, turnRuntimeSnapshot{}, false, turnReadOnlyFail)
			if admitted {
				abortStartedThreadTurn(th, started, nil)
			}
			if userHost {
				if err == nil || admitted {
					t.Fatalf("disabled user-host owner admitted = %v, error %v", admitted, err)
				}
				if len(th.History) != 0 {
					t.Fatal("rejected admission appended a user message")
				}
			} else if err != nil || !admitted {
				t.Fatalf("legacy Coding admission changed: admitted %v, error %v", admitted, err)
			}
		})
	}
}

func TestUserHostOwnerDisabledAfterReservationCannotStartTurn(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th := newThreadState("reserved-owner", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	th.Owner = "plugin:fixture"
	srv.threads[th.ID] = th
	reserved := false
	started, admitted, err := srv.startThreadUserTurnWithAdmission(context.Background(), th, providers.ChatMessage{Role: "user", Content: "Pause after reserving admission"}, turnRuntimeSnapshot{}, false, turnReadOnlyFail, turnAdmissionHooks{afterLease: func(thread *threadState, _ *providers.ChatMessage) error {
		thread.mu.Lock()
		reserved = thread.admissionReserved
		thread.mu.Unlock()
		rt.ActivePlugins = nil
		srv.retireIdlePluginRuntimes()
		return nil
	}})
	if admitted {
		abortStartedThreadTurn(th, started, nil)
	}
	if !reserved {
		t.Fatal("fixture did not reach the reserved admission boundary")
	}
	if err == nil || admitted {
		t.Fatalf("owner disabled after reservation still admitted = %v, error %v", admitted, err)
	}
	if th.admissionReserved || len(th.History) != 0 {
		t.Fatal("failed admission retained a lease or appended a user message")
	}
}
