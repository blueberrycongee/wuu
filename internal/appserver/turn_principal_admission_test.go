package appserver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestTurnAdmissionRechecksCapturedPrincipal(t *testing.T) {
	for _, name := range []string{"already_revoked", "revoked_after_lease", "still_live"} {
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			s := New(rt, &lockedBuffer{})
			defer s.Close()
			if _, err := rt.Sessions.CreateInitialized(session.Session{ID: "guarded", Owner: "human", Visibility: "user", CWD: rt.RootDir, Provider: "fake-provider", Model: "fake-model", PermissionMode: "standard"}, nil); err != nil {
				t.Fatal(err)
			}
			binding, err := rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close()
			th, err := s.ensureThreadLoaded(binding, "guarded")
			if err != nil {
				t.Fatal(err)
			}
			var revoked atomic.Bool
			caller, err := principal.AuthenticatedWithValidity(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "local", Subject: "queued-source"}, func() bool { return !revoked.Load() })
			if err != nil {
				t.Fatal(err)
			}
			th.mu.Lock()
			baselineHistory, baselineTurns := len(th.History), len(th.Turns)
			th.mu.Unlock()
			revoked.Store(name == "already_revoked")
			appended := false
			started, ok, err := s.startThreadUserTurnWithAdmission(context.Background(), th, providers.ChatMessage{Role: "user", Content: "must not execute"}, turnRuntimeSnapshot{AdmissionPrincipal: caller}, false, turnReadOnlyFail, turnAdmissionHooks{
				afterLease: func(*threadState, *providers.ChatMessage) error {
					revoked.Store(name == "revoked_after_lease")
					return nil
				},
				beforeUserAppendLocked: func(*threadState) (func() error, error) { appended = true; return nil, nil },
			})
			if ok {
				started.cancel()
				th.mu.Lock()
				th.completeTurnLocked(started.turnID, TurnStatusCompleted, nil, time.Now().UTC(), "", "", false)
				th.mu.Unlock()
			}
			if name == "still_live" {
				if err != nil || !ok || !appended {
					t.Fatalf("live source denied: ok=%v appended=%v err=%v", ok, appended, err)
				}
				return
			}
			if err == nil || ok || appended {
				t.Fatalf("revoked principal reached durable admission: ok=%v appended=%v err=%v", ok, appended, err)
			}
			th.mu.Lock()
			defer th.mu.Unlock()
			if th.running || len(th.Turns) != baselineTurns || len(th.History) != baselineHistory {
				t.Fatal("revoked principal created turn or history")
			}
		})
	}
}

func TestQueuedAdmissionUsesRetainedOriginWithoutReplacingSuppliedCapture(t *testing.T) {
	for _, name := range []string{"retained_origin", "failed_supplied_capture", "closed_origin"} {
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			s := New(rt, &lockedBuffer{})
			defer s.Close()
			if _, err := rt.Sessions.CreateInitialized(session.Session{ID: "queued", Owner: "human", Visibility: "user", CWD: rt.RootDir, Provider: "fake-provider", Model: "fake-model", PermissionMode: "standard"}, nil); err != nil {
				t.Fatal(err)
			}
			binding, err := rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close()
			th, err := s.ensureThreadLoaded(binding, "queued")
			if err != nil {
				t.Fatal(err)
			}
			admitting := binding.Retain()
			origin, err := admitting.RetainExecution()
			if err != nil {
				t.Fatal(err)
			}
			defer origin.Close()
			original := runtime.WithInvocationState(context.Background(), admitting)
			caller, err := principal.LocalOwner().Constrain(func(check context.Context) error {
				source := original
				if runtime.HasInvocationState(check) {
					source = check
				}
				held, err := runtime.RetainInvocationState(source)
				if err != nil {
					return err
				}
				defer held.Close()
				if held.Generation != binding.Generation {
					t.Fatal("admission selected a different generation")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// The accepted queue owns origin, but its admitting callback has returned.
			_ = admitting.Close()
			ctx := context.Background()
			if name == "failed_supplied_capture" {
				ctx = original
			}
			if name == "closed_origin" {
				_ = origin.Close()
			}
			started, ok, err := s.startThreadUserTurnWithAdmission(ctx, th, providers.ChatMessage{Role: "user", Content: "retained queued request"}, turnRuntimeSnapshot{AdmissionPrincipal: caller, PluginTurn: &pluginTurnReference{Origin: origin}}, false, turnReadOnlyFail, turnAdmissionHooks{})
			if ok {
				started.cancel()
				th.mu.Lock()
				th.completeTurnLocked(started.turnID, TurnStatusCompleted, nil, time.Now().UTC(), "", "", false)
				th.mu.Unlock()
			}
			if name == "retained_origin" {
				if err != nil || !ok {
					t.Fatalf("accepted live origin unavailable after callback: %v", err)
				}
			} else if err == nil || ok {
				t.Fatalf("failed capture was replaced: %s ok=%v err=%v", name, ok, err)
			}
		})
	}
}
