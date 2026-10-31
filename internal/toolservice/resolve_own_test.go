package toolservice

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Lookup works from a background callback without a tool backend, but only
// for the registry-authenticated caller. It is not an execution capability.
func TestResolveOwnBindingUsesAuthenticatedCallerWithoutBackend(t *testing.T) {
	calls := 0
	service := NewServiceWithOwnToolResolver(func(caller, localID string) ResolveOwnResult {
		calls++
		if caller != "test.consumer" {
			t.Fatalf("untrusted caller: %q", caller)
		}
		if localID != "review" {
			return ResolveOwnResult{}
		}
		return ResolveOwnResult{Found: true, Name: "collision_resolved_review", DirectOnly: true}
	})
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := nativeServiceTestClient(t, service)
	raw, err := client.invoke(context.Background(), "resolve-own", map[string]string{"tool_id": "review"})
	if err != nil {
		t.Fatal(err)
	}
	var result ResolveOwnResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Name != "collision_resolved_review" || !result.DirectOnly {
		t.Fatalf("binding: %+v", result)
	}
	raw, err = client.invoke(context.Background(), "resolve-own", map[string]string{"tool_id": "other"})
	if err != nil || string(raw) != "{\"found\":false}" {
		t.Fatalf("missing: %s %v", raw, err)
	}
	for _, input := range []map[string]string{{"tool_id": "review", "plugin_id": "victim"}, {"tool_id": " "}} {
		if _, err := client.invoke(context.Background(), "resolve-own", input); err == nil {
			t.Fatal("accepted invalid/spoofed lookup")
		}
	}
	if calls != 2 {
		t.Fatalf("invalid input reached resolver: %d", calls)
	}
	if _, err := client.invoke(context.Background(), "execute", CallRequest{}); err == nil {
		t.Fatal("lookup enabled execution")
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.invoke(context.Background(), "resolve-own", map[string]string{"tool_id": "review"}); err == nil {
		t.Fatal("retired provider resolved binding")
	}
	if calls != 2 {
		t.Fatal("retired lookup reached generation resolver")
	}
}

func TestResolveOwnBindingUnavailableWithoutResolver(t *testing.T) {
	service := NewService()
	if err := service.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := nativeServiceTestClient(t, service)
	_, err := client.invoke(context.Background(), "resolve-own", map[string]string{"tool_id": "review"})
	denied, ok := err.(*pluginhost.HostServiceError)
	if !ok || denied.Code != "service_unavailable" {
		t.Fatalf("missing resolver: %v", err)
	}
}
