package runtime

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"testing"
)

type ownBindingToolClient struct{ pluginToolTestClient }

func (*ownBindingToolClient) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}
}
func (*ownBindingToolClient) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

func TestNativeOwnToolBindingUsesActualGenerationDeclaration(t *testing.T) {
	contributor := &ownBindingToolClient{pluginToolTestClient: pluginToolTestClient{directOnly: true}}
	host := pluginhost.New(contributor)
	var provider pluginhost.Client
	for _, factory := range defaultNativeProviderCatalog() {
		if factory.ID == toolservice.DefaultModule {
			var err error
			provider, err = factory.Build(NativeProviderContext{Host: host})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if provider == nil {
		t.Fatal("native provider missing")
	}
	host.Add(provider)
	registry, conflicts := pluginhost.BuildServiceRegistry(provider, contributor)
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	raw, err := host.CallService(context.Background(), contributor.ID(), toolservice.ServiceName, "resolve-own", json.RawMessage(`{"tool_id":"change"}`))
	if err != nil {
		t.Fatal(err)
	}
	var binding toolservice.ResolveOwnResult
	if err := json.Unmarshal(raw, &binding); err != nil {
		t.Fatal(err)
	}
	declared, found := host.DeclaredTool(contributor.ID(), "change")
	if !found || !binding.Found || binding.Name != declared.PublicName || !binding.DirectOnly {
		t.Fatalf("binding %+v declaration %+v", binding, declared)
	}
	if contributor.executed {
		t.Fatal("lookup executed tool")
	}
	if _, err := host.CallService(context.Background(), "other-plugin", toolservice.ServiceName, "resolve-own", json.RawMessage(`{"tool_id":"change"}`)); err == nil {
		t.Fatal("undeclared consumer obtained binding")
	}
}
