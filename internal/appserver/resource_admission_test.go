package appserver

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResourceAdmissionResolvesOmittedPermission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.Permissions = config.ResolvedPermissions{Mode: config.PermissionModeUnconfined}
	v, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "review", Tenant: "review", Subject: "reader"})
	// An initial intent check may allow omitted effects; final resolved effects
	// must nevertheless enforce the read-only restriction.
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		mode, exists := r.Attributes["permission_mode"]
		return access.Decision{Allow: r.Action == MethodThreadStart && (!exists || string(mode) == `"read_only"`)}, nil
	})
	out := &lockedBuffer{}
	srv := New(rt, out, WithPrincipal(v), WithAccessPolicy(policy))
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(`{"id":"omitted","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "omitted")
	if response["error"] == nil {
		t.Fatalf("omitted permission escaped effective policy: %v", response)
	}
	sessions, err := rt.Sessions.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("denied creation persisted %d sessions", len(sessions))
	}
}

func TestResourceAdmissionRechecksDeleteAfterLease(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	ownerOut := &lockedBuffer{}
	owner := New(rt, ownerOut)
	defer owner.Close()
	if err := owner.handleLine(context.Background(), []byte(`{"id":"create","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, ownerOut.String()), "create")["result"]).Thread.ID
	lease, err := session.AcquireThreadLifecycleLease(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	admitted := make(chan struct{})
	var once sync.Once
	var revoked atomic.Bool
	v, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "review", Tenant: "review", Subject: "deleter"})
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		if r.Action == MethodThreadDelete && !revoked.Load() {
			once.Do(func() { close(admitted) })
			return access.Decision{Allow: true}, nil
		}
		return access.Decision{}, nil
	})
	out := &lockedBuffer{}
	srv := New(rt, out, WithPrincipal(v), WithAccessPolicy(policy))
	defer srv.Close()
	raw, _ := json.Marshal(Request{ID: json.RawMessage(`"delete"`), Method: MethodThreadDelete, Params: mustJSON(ThreadDeleteParams{ThreadID: threadID})})
	done := make(chan error, 1)
	go func() { done <- srv.handleLine(context.Background(), raw) }()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("no admission")
	}
	revoked.Store(true)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete stalled")
	}
	_, found, err := rt.Sessions.Find(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("revoked delete removed session after lease wait")
	}
	response := responseByID(t, parseOutput(t, out.String()), "delete")
	if response["error"] == nil {
		t.Fatal("expected denied response without deletion")
	}

}

type admissionAuthenticator struct{ result principal.Authentication }

func (a admissionAuthenticator) Authenticate(context.Context, principal.Credential) (principal.Authentication, error) {
	return a.result, nil
}

func TestResourceAdmissionCannotOutliveIdentityDuringPolicy(t *testing.T) {
	identity := principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"}
	caller, err := principal.Authenticate(context.Background(), admissionAuthenticator{principal.Authentication{Identity: identity, ExpiresAt: time.Now().Add(40 * time.Millisecond)}}, principal.Credential{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{access: connectionAccess{principal: caller, policy: access.PolicyFunc(func(ctx context.Context, _ access.Request) (access.Decision, error) {
		<-ctx.Done()
		return access.Decision{Allow: true}, nil
	})}}
	if err := s.AuthorizeResource(context.Background(), "read", access.Resource{Kind: "session", ID: "a"}); err == nil {
		t.Fatal("policy wait extended authentication expiry")
	}
}

func TestResourceAdmissionCannotOutliveEnrollmentDuringPolicy(t *testing.T) {
	var revoked atomic.Bool
	caller, _ := principal.AuthenticatedWithValidity(principal.Identity{Kind: "device", Issuer: "test", Tenant: "test", Subject: "device"}, func() bool { return !revoked.Load() })
	s := &Server{access: connectionAccess{principal: caller, policy: access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
		revoked.Store(true)
		return access.Decision{Allow: true}, nil
	})}}
	if err := s.AuthorizeResource(context.Background(), "read", access.Resource{Kind: "session", ID: "a"}); err == nil {
		t.Fatal("policy wait retained revoked enrollment")
	}
}

func TestResourceAdmissionRejectsUnauditedDelegatedMutationAndEvents(t *testing.T) {
	caller, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"})
	s := &Server{access: connectionAccess{principal: caller, policy: access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
		return access.Decision{Allow: true}, nil
	})}}
	for _, method := range []string{"plugin/package/install", "config/model/update", "turn/queue", "thread/fork"} {
		if err := s.authorizeRequest(context.Background(), Request{Method: method, Params: mustJSON(map[string]string{"thread_id": "a"})}); err == nil {
			t.Fatalf("unsupported delegated mutation %s", method)
		}
	}
	for _, event := range []Notification{{Method: "future/event", Params: map[string]string{"thread_id": "a"}}, {Method: NotificationTurnCompleted, Params: map[string]string{}}} {
		if _, _, ok := s.authorizeOutbound(context.Background(), event); ok {
			t.Fatalf("unscoped/unknown event passed %+v", event)
		}
	}
}

func TestResourceAdmissionResolvesTurnPermissionBeforeAppend(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	ownerOut := &lockedBuffer{}
	owner := New(rt, ownerOut)
	defer owner.Close()
	dispatchPayload(t, owner, "create", MethodThreadStart, ThreadStartParams{PermissionMode: "unconfined"})
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, ownerOut.String()), "create")["result"]).Thread.ID
	caller, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"})
	var effective atomic.Bool
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		mode, known := r.Attributes["permission_mode"]
		if known {
			effective.Store(true)
		}
		return access.Decision{Allow: r.Action == MethodTurnStart && (!known || string(mode) == `"read_only"`)}, nil
	})
	out := &lockedBuffer{}
	s := New(rt, out, WithPrincipal(caller), WithAccessPolicy(policy))
	defer s.Close()
	dispatchPayload(t, s, "send", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: "must not append"})
	response := responseByID(t, parseOutput(t, out.String()), "send")
	if response["error"] == nil || !effective.Load() {
		t.Fatalf("resolved turn effects not checked: %v", response)
	}
	history, err := rt.Sessions.LoadHistoryRecords(threadID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("denied turn appended history: %+v", history)
	}
}

func TestResourceAdmissionResumeResolvesNamedResource(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	ownerOut := &lockedBuffer{}
	owner := New(rt, ownerOut)
	defer owner.Close()
	dispatchPayload(t, owner, "create", MethodThreadStart, ThreadStartParams{})
	caller, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "test", Subject: "agent"})
	out := &lockedBuffer{}
	s := New(rt, out, WithPrincipal(caller), WithAccessPolicy(access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		for _, resource := range r.Resources {
			if resource.Kind != "global" {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})))
	defer s.Close()
	dispatchPayload(t, s, "resume", MethodThreadResume, ThreadResumeParams{})
	response := responseByID(t, parseOutput(t, out.String()), "resume")
	if response["error"] == nil {
		t.Fatalf("global grant exposed latest named conversation: %v", response)
	}
}
