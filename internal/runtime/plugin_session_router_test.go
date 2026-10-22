package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPluginSessionRouterBindsLatestHandlersAndProtectsNewBinding(t *testing.T) {
	router := NewPluginSessionRouter()
	if _, err := router.Send(context.Background(), "demo", pluginhost.SessionSendParams{}); err == nil {
		t.Fatal("unbound router accepted a session input")
	}
	create := func(_ context.Context, pluginID string, _ pluginhost.SessionCreateParams) (pluginhost.SessionCreateResult, error) {
		return pluginhost.SessionCreateResult{SessionID: "create:" + pluginID}, nil
	}
	firstUnbind := router.Bind(create, func(_ context.Context, pluginID string, _ pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
		return pluginhost.SessionSendResult{SessionID: "first:" + pluginID}, nil
	}, nil, nil)
	secondUnbind := router.Bind(create, func(_ context.Context, pluginID string, _ pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
		return pluginhost.SessionSendResult{SessionID: "second:" + pluginID}, nil
	}, nil, nil)
	firstUnbind()
	result, err := router.Send(context.Background(), "owner", pluginhost.SessionSendParams{})
	if err != nil || result.SessionID != "second:owner" {
		t.Fatalf("latest binding = %+v, %v", result, err)
	}
	secondUnbind()
	if _, err := router.Send(context.Background(), "owner", pluginhost.SessionSendParams{}); err == nil {
		t.Fatal("unbound latest handler remained callable")
	}
}

func TestPluginHostSessionSendUsesGenerationBoundOwner(t *testing.T) {
	router := NewPluginSessionRouter()
	var owner string
	router.Bind(func(_ context.Context, _ string, _ pluginhost.SessionCreateParams) (pluginhost.SessionCreateResult, error) {
		return pluginhost.SessionCreateResult{}, nil
	}, func(_ context.Context, pluginID string, params pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
		owner = pluginID
		return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleRunning, SessionID: params.SessionID, TurnID: params.RequestID}, nil
	}, nil, nil)
	handler := newPluginHostServices(serviceTestPlugin("alpha", "plugin:user:alpha", "one"), t.TempDir(), t.TempDir(), router)
	handler.activate()
	raw, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionSend, json.RawMessage(`{"request_id":"opaque","session_id":"thread-1","input":{"prompt":"work"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var result pluginhost.SessionSendResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if owner != "alpha" || result.TurnID != "opaque" {
		t.Fatalf("owner = %q, result = %+v", owner, result)
	}
	for _, invalid := range []string{`"true"`, `1`, `{}`, `[]`} {
		raw := json.RawMessage(`{"request_id":"invalid","session_id":"thread-1","input":{"prompt":"work"},"disable_tools":` + invalid + `}`)
		if _, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionSend, raw); err == nil {
			t.Fatalf("accepted non-boolean disable_tools: %s", invalid)
		}
	}
	if _, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionSend, json.RawMessage(`{"plugin_id":"beta","request_id":"opaque","session_id":"thread-1","input":{"prompt":"work"}}`)); err == nil {
		t.Fatal("caller-supplied plugin identity was accepted")
	}
}
