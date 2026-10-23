package permissionpolicy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// The selected policy must enforce the existing read-only mutation boundary,
// reject unrecognized modes, and stop decisions when its generation closes.
func TestDefaultPolicyLifecycleAndReadOnlyBoundary(t *testing.T) {
	ctx := context.Background()
	provider := NewService(Module)
	request := pluginhost.AuthorizationRequest{CWD: "/workspace", PermissionMode: "read_only", Tool: pluginhost.AuthorizationTool{Name: "write_file", Kind: "file"}}
	invoke := func() (pluginhost.AuthorizationDecision, error) {
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		result, err := provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: pluginhost.SecurityAuthorizeService, Method: pluginhost.SecurityAuthorizeMethod, Params: raw})
		var decision pluginhost.AuthorizationDecision
		if err == nil {
			err = json.Unmarshal(result, &decision)
		}
		return decision, err
	}
	if _, err := invoke(); err == nil {
		t.Fatal("prepared policy allowed invocation")
	}
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if decision, err := invoke(); err != nil || decision.Outcome != "deny" {
		t.Fatalf("read-only mutation: %+v %v", decision, err)
	}
	request.Tool.ReadOnly = true
	if decision, err := invoke(); err != nil || decision.Outcome != "allow" {
		t.Fatalf("read-only observation: %+v %v", decision, err)
	}
	request.Tool.ReadOnly = false
	request.PermissionMode = "standard"
	if decision, err := invoke(); err != nil || decision.Outcome != "allow" {
		t.Fatalf("standard: %+v %v", decision, err)
	}
	request.PermissionMode = "unrecognized"
	if _, err := invoke(); err == nil {
		t.Fatal("unknown policy silently became standard")
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatal(err)
	}
	request.PermissionMode = "standard"
	if _, err := invoke(); err == nil {
		t.Fatal("closed policy remained active")
	}
}
