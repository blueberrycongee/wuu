package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type pluginClientRequestTestRuntime struct {
	id       string
	lastCall pluginhost.PluginClientRequestInput
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
		ID: item.SubjectID, Fingerprint: item.Fingerprint, Method: "example.summary", Input: json.RawMessage(`{"thread_id":"thread-1"}`),
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
		ID: item.SubjectID, Fingerprint: "stale", Method: "example.summary",
	})
	if responseByID(t, parseOutput(t, out.String()), "stale")["error"] == nil {
		t.Fatal("stale generation unexpectedly reached plugin runtime")
	}
}
