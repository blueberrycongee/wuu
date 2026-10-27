package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
