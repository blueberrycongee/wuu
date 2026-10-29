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
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type pluginClientRequestTestRuntime struct {
	id       string
	lastCall pluginhost.PluginClientRequestInput
}

type desktopServiceTestClient struct {
	id       string
	provider bool
	call     func(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error)
}

func (c *desktopServiceTestClient) ID() string { return c.id }
func (c *desktopServiceTestClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (*desktopServiceTestClient) Close(context.Context) error { return nil }
func (c *desktopServiceTestClient) ProvidedServices() []pluginhost.ServiceDescriptor {
	if !c.provider {
		return nil
	}
	return []pluginhost.ServiceDescriptor{{Name: "example.catalog", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "list", InputSchema: "example.list.input.v1", OutputSchema: "example.list.output.v1"}}}}
}
func (c *desktopServiceTestClient) RequiredServices() []pluginhost.ServiceRequirement {
	if c.provider {
		return nil
	}
	return []pluginhost.ServiceRequirement{{Name: "example.catalog", MajorVersion: 1, Required: true}}
}
func (c *desktopServiceTestClient) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if c.call == nil {
		return nil, errors.New("consumer has no service implementation")
	}
	return c.call(ctx, p)
}

func TestDesktopServiceRequestUsesDeclaredDependencyAndGraph(t *testing.T) {
	var calls int
	srv, item, out := newPluginStateTestServer(t,
		func(id string) pluginhost.Client { return &desktopServiceTestClient{id: id} },
		func(string) pluginhost.Client {
			return &desktopServiceTestClient{id: "external.catalog", provider: true, call: func(_ context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
				calls++
				if p.Service != "example.catalog" || p.Method != "list" || string(p.Params) != `{"query":"selected"}` {
					t.Errorf("service call = %+v", p)
				}
				return json.RawMessage(`{"provider":"external.catalog"}`), nil
			}}
		},
	)
	graph, release, err := srv.rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, tc := range []struct {
		id, service, generation string
		success                 bool
	}{
		{"selected", "example.catalog", graph.GenerationID, true},
		{"undeclared", "state.credentials", graph.GenerationID, false},
		{"stale-graph", "example.catalog", "previous-graph-same-code", false},
		{"missing-graph", "example.catalog", "", false},
	} {
		callPluginPackageRPC(t, srv, tc.id, MethodPluginClientRequest, map[string]any{
			"id": item.SubjectID, "fingerprint": item.Fingerprint, "graph_generation": tc.generation,
			"service": tc.service, "method": "list", "input": json.RawMessage(`{"query":"selected"}`),
		})
		response := responseByID(t, parseOutput(t, out.String()), tc.id)
		if (response["error"] == nil) != tc.success {
			t.Fatalf("%s response = %+v", tc.id, response)
		}
		if tc.success && !strings.Contains(string(remarshal[PluginClientRequestResult](t, response["result"]).Result), "external.catalog") {
			t.Fatalf("result = %+v", response)
		}
	}
	if calls != 1 {
		t.Fatalf("provider invoked %d times", calls)
	}
	srv.rt.PluginHost.RetirePlugin(context.Background(), "external.catalog", errors.New("provider disabled"))
	callPluginPackageRPC(t, srv, "disabled", MethodPluginClientRequest, map[string]any{
		"id": item.SubjectID, "fingerprint": item.Fingerprint, "graph_generation": graph.GenerationID,
		"service": "example.catalog", "method": "list", "input": json.RawMessage(`{"query":"selected"}`),
	})
	if responseByID(t, parseOutput(t, out.String()), "disabled")["error"] == nil || calls != 1 {
		t.Fatal("retired graph accepted a new service request")
	}
}

func TestDesktopServiceCancellationCannotCrossGraphs(t *testing.T) {
	started := make(chan struct{})
	srv, item, out := newPluginStateTestServer(t,
		func(id string) pluginhost.Client { return &desktopServiceTestClient{id: id} },
		func(string) pluginhost.Client {
			return &desktopServiceTestClient{id: "external.catalog", provider: true, call: func(ctx context.Context, _ pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}}
		},
	)
	graph, release, err := srv.rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	callPluginPackageRPC(t, srv, "request", MethodPluginClientRequest, PluginClientRequestParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: graph.GenerationID, Service: "example.catalog", Method: "list", RequestID: "same-request",
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("desktop request did not reach its selected provider: %s", out.String())
	}
	for _, generation := range []string{"old-graph-same-code", graph.GenerationID} {
		callPluginPackageRPC(t, srv, generation, MethodPluginClientCancel, PluginClientCancelParams{
			ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: generation, RequestID: "same-request",
		})
		response := responseByID(t, parseOutput(t, out.String()), generation)
		if response["error"] != nil {
			t.Fatalf("cancel: %+v", response)
		}
		got := remarshal[PluginClientCancelResult](t, response["result"]).Cancelled
		if got != (generation == graph.GenerationID) {
			t.Fatalf("cancelled=%t for graph %s", got, generation)
		}
	}
	if response := waitForResponseByID(t, out, "request"); response["error"] == nil {
		t.Fatalf("cancelled request: %+v", response)
	}
}

func (c *pluginClientRequestTestRuntime) ID() string { return c.id }
func (c *pluginClientRequestTestRuntime) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *pluginClientRequestTestRuntime) Close(context.Context) error { return nil }
func (c *pluginClientRequestTestRuntime) ProtocolVersion() int {
	return pluginhost.CapabilityProtocolVersion
}
func (c *pluginClientRequestTestRuntime) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityPluginClientRequest, Kind: "decision", Version: 1}}
}
func (c *pluginClientRequestTestRuntime) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	if err := json.Unmarshal(params.Input, &c.lastCall); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{"result":{"accepted":true}}`)}, nil
}

func TestPluginClientRequestRoutesOnlyToActiveGeneration(t *testing.T) {
	var client *pluginClientRequestTestRuntime
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		client = &pluginClientRequestTestRuntime{id: id}
		return client
	})

	callPluginPackageRPC(t, srv, "ok", MethodPluginClientRequest, PluginClientRequestParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: srv.rt.ExtensionSnapshot().GenerationID, Method: "example.summary", Input: json.RawMessage(`{"thread_id":"thread-1"}`),
	})
	response := responseByID(t, parseOutput(t, out.String()), "ok")
	if response["error"] != nil {
		t.Fatalf("response = %+v", response)
	}
	result := remarshal[PluginClientRequestResult](t, response["result"])
	if string(result.Result) != `{"accepted":true}` {
		t.Fatalf("result = %s", result.Result)
	}
	if client.lastCall.Method != "example.summary" || string(client.lastCall.Input) != `{"thread_id":"thread-1"}` {
		t.Fatalf("call = %+v", client.lastCall)
	}

	callPluginPackageRPC(t, srv, "stale", MethodPluginClientRequest, PluginClientRequestParams{
		ID: item.SubjectID, Fingerprint: "stale", GraphGeneration: srv.rt.ExtensionSnapshot().GenerationID, Method: "example.summary",
	})
	if responseByID(t, parseOutput(t, out.String()), "stale")["error"] == nil {
		t.Fatal("stale generation unexpectedly reached plugin runtime")
	}
}

func TestPluginClientNonOwnerResourceAdmissionAndReplay(t *testing.T) {
	var calls atomic.Int32
	base, item, _ := newPluginStateTestServer(t,
		func(id string) pluginhost.Client { return &desktopServiceTestClient{id: id} },
		func(string) pluginhost.Client {
			return &desktopServiceTestClient{id: "external.catalog", provider: true, call: func(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
				calls.Add(1)
				return json.RawMessage(`{"private":"catalog"}`), nil
			}}
		},
	)
	viewer, _ := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "viewer"})
	params := PluginClientRequestParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: base.rt.ExtensionSnapshot().GenerationID, Service: "example.catalog", Method: "list"}
	var revoked atomic.Bool
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		want := map[string]string{"plugin-subject": params.ID, "plugin-fingerprint": params.Fingerprint, "plugin-generation": params.GraphGeneration, "plugin-service": params.Service, "plugin-method": params.Method}
		if revoked.Load() || r.Owner || r.Principal != viewer.Identity() || r.Action != MethodPluginClientRequest || len(r.Resources) != len(want) {
			return access.Decision{}, nil
		}
		for _, resource := range r.Resources {
			if want[resource.Kind] != resource.ID || resource.ID == "" {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})
	out := &captureGuardedWriter{}
	srv := New(base.rt, out, WithPrincipal(viewer), WithAccessPolicy(policy))
	defer srv.Close()
	call := func(id string, p PluginClientRequestParams) Response {
		t.Helper()
		raw, _ := json.Marshal(Request{ID: mustJSON(id), Method: MethodPluginClientRequest, Params: mustJSON(p)})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		for _, frame := range out.frames {
			var response Response
			_ = json.Unmarshal(frame.payload, &response)
			if string(response.ID) == string(mustJSON(id)) {
				return response
			}
		}
		t.Fatal("missing response")
		return Response{}
	}
	if response := call("allowed", params); response.Error != nil || calls.Load() != 1 {
		t.Fatalf("nonowner selected request failed: %+v, calls=%d", response, calls.Load())
	}
	for _, field := range []string{"subject", "fingerprint", "generation", "service", "method"} {
		bad := params
		switch field {
		case "subject":
			bad.ID = "other"
		case "fingerprint":
			bad.Fingerprint = "other"
		case "generation":
			bad.GraphGeneration = "other"
		case "service":
			bad.Service = "other"
		case "method":
			bad.Method = "other"
		}
		if response := call(field, bad); response.Error == nil {
			t.Fatalf("cross-%s request admitted", field)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("denied route reached provider")
	}
	revoked.Store(true)
	for _, frame := range out.frames {
		var response Response
		_ = json.Unmarshal(frame.payload, &response)
		if string(response.ID) == `"allowed"` && (frame.guard == nil || frame.guard() == nil) {
			t.Fatal("revoked plugin response replay disclosed data")
		}
	}
}

func TestPluginClientNonOwnerCancelUsesCapturedOperation(t *testing.T) {
	started := make(chan struct{})
	base, item, _ := newPluginStateTestServer(t,
		func(id string) pluginhost.Client { return &desktopServiceTestClient{id: id} },
		func(string) pluginhost.Client {
			return &desktopServiceTestClient{id: "external.catalog", provider: true, call: func(ctx context.Context, _ pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}}
		},
	)
	viewer, _ := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "viewer"})
	generation := base.rt.ExtensionSnapshot().GenerationID
	out := &lockedBuffer{}
	var denyCancel atomic.Bool
	srv := New(base.rt, out, WithPrincipal(viewer), WithAccessPolicy(access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		want := map[string]string{"plugin-subject": item.SubjectID, "plugin-fingerprint": item.Fingerprint, "plugin-generation": generation, "plugin-service": "example.catalog", "plugin-method": "list", "plugin-client-request": "request"}
		if r.Principal != viewer.Identity() || r.Owner || len(r.Resources) != len(want) || (r.Action != MethodPluginClientRequest && r.Action != MethodPluginClientCancel) || (r.Action == MethodPluginClientCancel && denyCancel.Load()) {
			return access.Decision{}, nil
		}
		for _, resource := range r.Resources {
			if want[resource.Kind] != resource.ID || resource.ID == "" {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: true}, nil
	})))
	defer srv.Close()
	callPluginPackageRPC(t, srv, "request", MethodPluginClientRequest, PluginClientRequestParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: generation, Service: "example.catalog", Method: "list", RequestID: "request"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("nonowner request not dispatched: %s", out.String())
	}
	cancel := PluginClientCancelParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: generation, RequestID: "request"}
	denyCancel.Store(true)
	callPluginPackageRPC(t, srv, "denied", MethodPluginClientCancel, cancel)
	if responseByID(t, parseOutput(t, out.String()), "denied")["error"] == nil {
		t.Fatal("cancel bypassed selected policy")
	}
	denyCancel.Store(false)
	callPluginPackageRPC(t, srv, "cancel", MethodPluginClientCancel, cancel)
	response := responseByID(t, parseOutput(t, out.String()), "cancel")
	if response["error"] != nil || !remarshal[PluginClientCancelResult](t, response["result"]).Cancelled {
		t.Fatalf("captured operation cancel failed: %+v", response)
	}
}
