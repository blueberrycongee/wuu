package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// The remote ingress must fail closed on missing selection, forged authority,
// cross-session records, and revocation, while exposing only task metadata.
type durableIngressFixture struct {
	calls  atomic.Int32
	invoke func(pluginhost.ServiceInvokeParams) json.RawMessage
}

func (f *durableIngressFixture) ID() string { return "fixture.durable" }
func (f *durableIngressFixture) Status() pluginhost.Status {
	return pluginhost.Status{ID: f.ID(), State: pluginhost.StateActive}
}
func (f *durableIngressFixture) Activate(context.Context) error                    { return nil }
func (f *durableIngressFixture) Close(context.Context) error                       { return nil }
func (f *durableIngressFixture) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (f *durableIngressFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	var result []pluginhost.ServiceDescriptor
	for _, service := range []string{"durable.tasks", "host.durable-wait"} {
		var methods []pluginhost.ServiceMethodDescriptor
		for _, method := range []string{"list", "get", "inspect", "decide"} {
			methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: service + "." + method + ".input.v1", OutputSchema: service + "." + method + ".output.v1"})
		}
		result = append(result, pluginhost.ServiceDescriptor{Name: service, Version: "1.0.0", Methods: methods})
	}
	return result
}
func (f *durableIngressFixture) InvokeService(_ context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	f.calls.Add(1)
	return f.invoke(p), nil
}

func durableIngressCall(t *testing.T, s *Server, out *lockedBuffer, ctx context.Context, id, method string, params any) map[string]any {
	t.Helper()
	if err := s.handleLine(ctx, mustJSON(Request{ID: mustJSON(id), Method: method, Params: mustJSON(params)})); err != nil {
		t.Fatal(err)
	}
	return responseByID(t, parseOutput(t, out.String()), id)
}

func TestDurableIngressScopedAttestationAndProjection(t *testing.T) {
	f := &durableIngressFixture{}
	rt := newTestRuntime(t, &fakeClient{}, runtime.NewDurableIngressConsumer(), f)
	for _, id := range []string{"a", "b"} {
		if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "test", Tenant: "tenant", Subject: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		for _, resource := range r.Resources {
			if resource.Kind != "session" || resource.ID != "a" {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: r.Principal == identity.Identity()}, nil
	})
	out := &lockedBuffer{}
	s := New(rt, out, WithPrincipal(identity), WithAccessPolicy(policy))
	defer s.Close()
	f.invoke = func(p pluginhost.ServiceInvokeParams) json.RawMessage {
		if p.Caller != runtime.DurableIngressConsumer || p.Admission == nil || p.Admission.Principal != identity.Identity() || p.Admission.InputDigest != executionauth.RawRequestDigest(p.Params) || p.Admission.InputJSON != string(p.Params) {
			t.Fatalf("missing exact transport attestation: %+v", p)
		}
		prefix := "durable-tasks."
		if p.Service == "host.durable-wait" {
			prefix = "durable-wait."
		}
		if p.Admission.Action != prefix+p.Method {
			t.Fatalf("incorrect action: %+v", p.Admission)
		}
		if p.Service == "host.durable-wait" {
			record := map[string]any{"version": 1, "id": "wait-a", "kind": "approval", "owner": map[string]any{"session_id": "a", "plugin_id": "driver"}, "status": "pending", "effect_state": "not_started", "question": "private approval prompt"}
			if p.Method == "decide" {
				record["status"] = "approved"
				return mustJSON(record)
			}
			return mustJSON(map[string]any{"waits": []any{record}, "has_more": true})
		}
		task := map[string]any{"id": "task-a", "session_id": "a", "run_id": "run-a", "kind": "run", "status": "waiting", "usage_status": "incomplete", "usage": map[string]int{"input_tokens": 8, "output_tokens": 3}, "prompt": "private task prompt", "tool_arguments": map[string]string{"secret": "credential"}, "checkpoint": map[string]string{"output": "private output"}}
		if p.Method == "get" {
			return mustJSON(map[string]any{"version": 1, "task": task, "revision": "rev-1"})
		}
		return mustJSON(map[string]any{"version": 1, "tasks": []any{task}, "revision": "rev-1"})
	}
	for _, entry := range []struct {
		id, method string
		params     any
	}{
		{"tasks", "durable/tasks/list", map[string]string{"session_id": "a", "run_id": "run-a"}},
		{"task", "durable/tasks/get", map[string]string{"session_id": "a", "run_id": "run-a", "task_id": "task-a"}},
		{"waits", "durable/waits/list", map[string]string{"session_id": "a"}},
		{"decision", "durable/waits/decide", map[string]string{"session_id": "a", "wait_id": "wait-a", "decision": "approve"}},
	} {
		response := durableIngressCall(t, s, out, context.Background(), entry.id, entry.method, entry.params)
		if response["error"] != nil {
			t.Fatalf("authorized %s: %v", entry.method, response)
		}
		if entry.method == MethodDurableWaitsList && response["result"].(map[string]any)["has_more"] != true {
			t.Fatal("bounded provider response lost its truncation marker")
		}
		raw := string(mustJSON(response["result"]))
		if (entry.method == MethodDurableTasksList || entry.method == MethodDurableTasksGet) && !strings.Contains(raw, `"usage_status":"incomplete"`) {
			t.Fatalf("partial usage lost its availability marker: %s", raw)
		}
		for _, secret := range []string{"private", "credential", "tool_arguments", "checkpoint\""} {
			if strings.Contains(raw, secret) {
				t.Fatalf("private content leaked: %s", raw)
			}
		}
	}
	before := f.calls.Load()
	for _, entry := range []struct {
		id, method string
		params     any
	}{
		{"cross", "durable/tasks/list", map[string]string{"session_id": "b"}},
		{"forged", "durable/waits/decide", map[string]any{"session_id": "a", "wait_id": "wait-a", "decision": "approve", "admission": map[string]any{"principal": identity.Identity()}}},
		{"arbitrary", "durable/tasks/list", map[string]string{"session_id": "a", "service": "state.documents", "method": "query"}},
	} {
		if response := durableIngressCall(t, s, out, context.Background(), entry.id, entry.method, entry.params); response["error"] == nil {
			t.Fatalf("unsafe request allowed: %v", response)
		}
	}
	if f.calls.Load() != before {
		t.Fatal("denied request reached provider")
	}
}

func TestDurableIngressRejectsUntrustedDecisionsAndProviderScope(t *testing.T) {
	f := &durableIngressFixture{invoke: func(pluginhost.ServiceInvokeParams) json.RawMessage {
		return json.RawMessage(`{"version":1,"tasks":[],"revision":"r"}`)
	}}
	rt := newTestRuntime(t, &fakeClient{}, runtime.NewDurableIngressConsumer(), f)
	if _, err := rt.Sessions.CreateWithMetadata("a", rt.RootDir); err != nil {
		t.Fatal(err)
	}
	agentIdentity, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "test", Tenant: "tenant", Subject: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	s := New(rt, out, WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
		return access.Decision{Allow: true}, nil
	})))
	defer s.Close()
	for i, identity := range []principal.Verified{{}, agentIdentity} {
		id := []string{"anonymous", "agent"}[i]
		if response := durableIngressCall(t, s, out, principal.WithContext(context.Background(), identity), id, "durable/waits/decide", map[string]string{"session_id": "a", "wait_id": "w", "decision": "approve"}); response["error"] == nil {
			t.Fatalf("untrusted decision: %v", response)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("untrusted human decision reached provider")
	}
	for _, entry := range []struct {
		id, method, response string
		params               map[string]string
	}{
		{"task-scope", "durable/tasks/list", `{"version":1,"tasks":[{"id":"t","session_id":"other","run_id":"r"}],"revision":"r"}`, map[string]string{"session_id": "a"}},
		{"run-scope", "durable/tasks/list", `{"version":1,"tasks":[{"id":"t","session_id":"a","run_id":"other"}],"revision":"r"}`, map[string]string{"session_id": "a", "run_id": "r"}},
		{"id-scope", "durable/tasks/get", `{"version":1,"task":{"id":"other","session_id":"a","run_id":"r"},"revision":"r"}`, map[string]string{"session_id": "a", "task_id": "t"}},
		{"usage-status", "durable/tasks/list", `{"version":1,"tasks":[{"id":"t","session_id":"a","run_id":"r","usage_status":"private diagnostic"}],"revision":"r"}`, map[string]string{"session_id": "a"}},
		{"wait-scope", "durable/waits/list", `{"waits":[{"version":1,"id":"w","owner":{"session_id":"other"}}]}`, map[string]string{"session_id": "a"}},
	} {
		f.invoke = func(pluginhost.ServiceInvokeParams) json.RawMessage { return json.RawMessage(entry.response) }
		if response := durableIngressCall(t, s, out, context.Background(), entry.id, entry.method, entry.params); response["error"] == nil || response["result"] != nil {
			t.Fatalf("out-of-scope data delivered: %v", response)
		}
	}
	if _, err := rt.Sessions.UpdateArchived("a", true); err != nil {
		t.Fatal(err)
	}
	before := f.calls.Load()
	if response := durableIngressCall(t, s, out, context.Background(), "archived", "durable/waits/decide", map[string]string{"session_id": "a", "wait_id": "w", "decision": "approve"}); response["error"] == nil {
		t.Fatal("archived session decision accepted")
	}
	if f.calls.Load() != before {
		t.Fatal("archived decision reached provider")
	}
}

func TestDurableIngressRevocationAndExplicitSelection(t *testing.T) {
	t.Run("archive-before-effect", func(t *testing.T) {
		f := &durableIngressFixture{invoke: func(pluginhost.ServiceInvokeParams) json.RawMessage {
			t.Fatal("decision reached provider after session was archived")
			return nil
		}}
		rt := newTestRuntime(t, &fakeClient{}, runtime.NewDurableIngressConsumer(), f)
		if _, err := rt.Sessions.CreateWithMetadata("a", rt.RootDir); err != nil {
			t.Fatal(err)
		}
		policy := access.PolicyFunc(func(ctx context.Context, request access.Request) (access.Decision, error) {
			if request.Action == MethodDurableWaitsDecide && pluginhost.ServiceScopeFromContext(ctx) != nil {
				if _, err := rt.Sessions.UpdateArchived("a", true); err != nil {
					t.Fatal(err)
				}
			}
			return access.Decision{Allow: true}, nil
		})
		out := &lockedBuffer{}
		s := New(rt, out, WithAccessPolicy(policy))
		defer s.Close()
		response := durableIngressCall(t, s, out, context.Background(), "late-archive", MethodDurableWaitsDecide, map[string]string{"session_id": "a", "wait_id": "w", "decision": "approve"})
		if response["error"] == nil {
			t.Fatal("archived session acquired a new decision")
		}
	})
	t.Run("selection", func(t *testing.T) {
		f := &durableIngressFixture{invoke: func(pluginhost.ServiceInvokeParams) json.RawMessage {
			t.Fatal("unselected ingress called provider")
			return nil
		}}
		rt := newTestRuntime(t, &fakeClient{}, f)
		if _, err := rt.Sessions.CreateWithMetadata("a", rt.RootDir); err != nil {
			t.Fatal(err)
		}
		out := &lockedBuffer{}
		s := New(rt, out)
		defer s.Close()
		if response := durableIngressCall(t, s, out, context.Background(), "disabled", "durable/tasks/list", map[string]string{"session_id": "a"}); response["error"] == nil {
			t.Fatal("durable ingress silently enabled")
		}
	})
	t.Run("revocation", func(t *testing.T) {
		var revoked atomic.Bool
		f := &durableIngressFixture{invoke: func(pluginhost.ServiceInvokeParams) json.RawMessage {
			revoked.Store(true)
			return json.RawMessage(`{"version":1,"tasks":[{"id":"sensitive-task","session_id":"a","run_id":"r"}],"revision":"r"}`)
		}}
		rt := newTestRuntime(t, &fakeClient{}, runtime.NewDurableIngressConsumer(), f)
		if _, err := rt.Sessions.CreateWithMetadata("a", rt.RootDir); err != nil {
			t.Fatal(err)
		}
		identity, err := principal.AuthenticatedWithValidity(principal.Identity{Kind: "user", Issuer: "test", Tenant: "tenant", Subject: "viewer"}, func() bool { return !revoked.Load() })
		if err != nil {
			t.Fatal(err)
		}
		out := &lockedBuffer{}
		s := New(rt, out, WithPrincipal(identity), WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
			return access.Decision{Allow: true}, nil
		})))
		defer s.Close()
		if err := s.handleLine(context.Background(), []byte(`{"id":"revoked","method":"durable/tasks/list","params":{"session_id":"a"}}`)); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "sensitive-task") {
			t.Fatalf("revoked result leaked: %s", out.String())
		}
		if f.calls.Load() != 1 {
			t.Fatal("revocation did not happen during service call")
		}
	})
}
