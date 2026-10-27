package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
	"strings"
	"sync/atomic"
	"testing"
)

// Security contract: request and response delivery, list metadata, events and
// content reads must all observe the current policy, not a cached UI grant.
func TestResourceGateRevocationAndNoPrincipalSelfClaim(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	ownerOut := &lockedBuffer{}
	owner := New(rt, ownerOut, WithAccessPolicy(access.OwnerOnly{}))
	defer owner.Close()
	for _, id := range []string{"a", "b"} {
		if err := owner.handleLine(context.Background(), []byte(`{"id":"`+id+`","method":"thread/start"}`)); err != nil {
			t.Fatal(err)
		}
	}
	messages := parseOutput(t, ownerOut.String())
	a := remarshal[ThreadStartResult](t, responseByID(t, messages, "a")["result"]).Thread.ID
	identity, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	var revoked, offline atomic.Bool
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		if offline.Load() {
			return access.Decision{Allow: true}, errors.New("offline")
		}
		if revoked.Load() {
			return access.Decision{}, nil
		}
		if r.Action == MethodThreadListAll && r.Resources[0].Kind == "global" {
			return access.Decision{Allow: true}, nil
		}
		for _, resource := range r.Resources {
			if resource.Kind != "session" || resource.ID != a {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: r.Principal == identity.Identity()}, nil
	})
	out := &lockedBuffer{}
	reader := New(rt, out, WithPrincipal(identity), WithAccessPolicy(policy))
	defer reader.Close()
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(Request{ID: mustJSON(id), Method: method, Params: mustJSON(params)})
		if err := reader.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	if response := call("read", MethodThreadResume, map[string]any{"session_id": a}); response["error"] != nil {
		t.Fatalf("authorized read: %v", response)
	}
	response := call("list", MethodThreadListAll, map[string]any{"summary_only": true})
	if response["error"] != nil {
		t.Fatal(response)
	}
	list := remarshal[ThreadListResult](t, response["result"])
	if len(list.Threads) != 1 || list.Threads[0].ID != a {
		t.Fatalf("list leaked metadata: %+v", list)
	}
	before := out.String()
	if err := reader.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: Thread{ID: a}}); err != nil {
		t.Fatal(err)
	}
	if out.String() == before {
		t.Fatal("allowed event missing")
	}
	revoked.Store(true)
	before = out.String()
	_ = reader.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: Thread{ID: a}})
	if out.String() != before {
		t.Fatal("revoked event leaked")
	}
	for _, method := range []string{MethodThreadResume, "thread/attachment/read", "thread/artifact/read", "thread/history/page"} {
		if r := call(method, method, map[string]any{"thread_id": a}); r["error"] == nil {
			t.Fatalf("revoked read passed %s", method)
		}
	}
	revoked.Store(false)
	offline.Store(true)
	if r := call("offline", MethodThreadResume, map[string]any{"session_id": a}); r["error"] == nil {
		t.Fatal("policy failure was allowed")
	}
	if err := reader.handleLine(context.Background(), []byte(`{"id":"forged","method":"config/read","principal":{"owner":true,"subject":"owner"}}`)); err != nil {
		t.Fatal(err)
	}
	if r := responseByID(t, parseOutput(t, out.String()), "forged"); r["error"] == nil {
		t.Fatal("wire owner impersonation allowed")
	}
	if strings.Contains(out.String(), "policy token") {
		t.Fatal("credential leak")
	}
}

func TestUnknownResourceMethodCannotBorrowGlobalGrant(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	v, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "a", Subject: "a"})
	var calls atomic.Int32
	s := New(rt, &lockedBuffer{}, WithPrincipal(v), WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
		calls.Add(1)
		return access.Decision{Allow: true}, nil
	})))
	defer s.Close()
	if err := s.authorizeRequest(context.Background(), Request{Method: "future/unsafe", Params: mustJSON(map[string]string{"thread_id": "s"})}); err == nil {
		t.Fatal("unknown method accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unknown method reached wildcard policy")
	}
	for _, raw := range []string{`{"thread_id":"a","source_thread_id":"b"}`, `{"thread_id":["a"]}`, `{"thread_id":""}`} {
		resources, err := requestResources(Request{Method: MethodThreadResume, Params: json.RawMessage(raw)})
		if strings.Contains(raw, "source_thread") {
			if err != nil || len(resources) != 2 {
				t.Fatalf("multi-resource gate %v %v", resources, err)
			}
		} else if err == nil {
			t.Fatalf("malformed resource accepted %s", raw)
		}
	}
}

type capturedFrame struct {
	payload []byte
	guard   func() error
}
type captureGuardedWriter struct{ frames []capturedFrame }

func (w *captureGuardedWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *captureGuardedWriter) WriteAuthorized(p []byte, guard func() error) (int, error) {
	w.frames = append(w.frames, capturedFrame{append([]byte(nil), p...), guard})
	return len(p), nil
}
func TestResponseReplayRetainsExactAuthorizationAndEffects(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &captureGuardedWriter{}
	var revoked atomic.Bool
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: !revoked.Load()}, nil
	})
	srv := New(rt, out, WithAccessPolicy(policy))
	defer srv.Close()
	raw := []byte(`{"id":"create","method":"thread/start","params":{"permission_mode":"read_only"}}`)
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	var found *capturedFrame
	for i := range out.frames {
		var response Response
		_ = json.Unmarshal(out.frames[i].payload, &response)
		if string(response.ID) == `"create"` {
			found = &out.frames[i]
		}
	}
	if found == nil || found.guard == nil || found.guard() != nil {
		t.Fatal("response did not retain authorization")
	}
	revoked.Store(true)
	if found.guard() == nil {
		t.Fatal("replayed response retained old grant")
	}
}

func TestResponseReplayRechecksAdditionalScopes(t *testing.T) {
	var revoked atomic.Bool
	s := &Server{access: connectionAccess{principal: principal.LocalOwner(), policy: access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: r.Action != "inspect" || !revoked.Load()}, nil
	})}}
	req := Request{ID: mustJSON("list")}
	if err := s.RegisterAuthorizedResponse(context.Background(), req, "list", true, access.Resource{Kind: "workspace", ID: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAuthorizedResponseGuard(req, "inspect", true, access.Resource{Kind: "session", ID: "private"}); err != nil {
		t.Fatal(err)
	}
	_, guard, ok := s.authorizeOutbound(context.Background(), Response{ID: req.ID, Result: map[string]string{"id": "private"}})
	if !ok || guard() != nil {
		t.Fatal("authorized response missing")
	}
	revoked.Store(true)
	if guard() == nil {
		t.Fatal("list authority replayed revoked row")
	}
	if err := s.AppendAuthorizedResponseGuard(req, "inspect", true, access.Resource{Kind: "session", ID: "private"}); err == nil {
		t.Fatal("appended scope after response")
	}
}

func TestResponseAdditionalScopeRevokedBeforeLiveDeliveryReturnsDenial(t *testing.T) {
	var revoked atomic.Bool
	s := &Server{access: connectionAccess{principal: principal.LocalOwner(), policy: access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: r.Action != "inspect" || !revoked.Load()}, nil
	})}}
	req := Request{ID: mustJSON("list")}
	if err := s.RegisterAuthorizedResponse(context.Background(), req, "list", true, access.Resource{Kind: "workspace", ID: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAuthorizedResponseGuard(req, "inspect", true, access.Resource{Kind: "session", ID: "private"}); err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	value, guard, ok := s.authorizeOutbound(context.Background(), Response{ID: req.ID, Result: "protected"})
	response, typed := value.(Response)
	if !ok || !typed || response.Result != nil || response.Error == nil || response.Error.Code != "access_denied" || guard() != nil {
		t.Fatalf("live response did not return safe denial: %+v", value)
	}
}
