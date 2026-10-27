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

func TestDefaultPolicyEffectLeaseIsCallerBoundAndCannotReplay(t *testing.T) {
	ctx := context.Background()
	provider := NewService(Module)
	if err := provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	request := pluginhost.AuthorizationRequest{CWD: "/workspace", PermissionMode: "standard", Tool: pluginhost.AuthorizationTool{Name: "write_file", Kind: "file"}}
	call := func(caller, method string, input any) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: caller, Service: pluginhost.SecurityAuthorizeService, Method: method, Params: raw})
	}
	acquire := map[string]any{"id": "lease", "request": request}
	if _, err := call("", "acquire-authorization", acquire); err == nil {
		t.Fatal("effect lease lacked authenticated registry caller")
	}
	result, err := call("consumer", "acquire-authorization", acquire)
	if err != nil {
		t.Fatal(err)
	}
	var lease struct {
		ID       string                           `json:"id"`
		Decision pluginhost.AuthorizationDecision `json:"decision"`
	}
	if json.Unmarshal(result, &lease) != nil || lease.ID != "lease" || lease.Decision.Outcome != "allow" {
		t.Fatalf("invalid lease %s", result)
	}
	request.PermissionMode = "read_only"
	acquire["request"] = request
	if _, err := call("consumer", "acquire-authorization", acquire); err == nil {
		t.Fatal("lease request changed")
	}
	if _, err := call("other", "release-authorization", map[string]string{"id": "lease"}); err != nil {
		t.Fatal(err)
	}
	request.PermissionMode = "standard"
	acquire["request"] = request
	if _, err := call("consumer", "acquire-authorization", acquire); err != nil {
		t.Fatalf("foreign consumer released lease: %v", err)
	}
	if _, err := call("consumer", "release-authorization", map[string]string{"id": "lease"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("consumer", "acquire-authorization", acquire); err == nil {
		t.Fatal("released effect lease replayed")
	}
}
