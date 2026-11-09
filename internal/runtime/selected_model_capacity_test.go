package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestDirectCompactionOutputErrorSettlesFailedSubmission(t *testing.T) {
	caller := &modelReceiptCapabilityClient{
		fakeServicePluginClient: fakeServicePluginClient{id: "compact-adapter"},
		capability:              pluginhost.CapabilityDescriptor{ID: pluginhost.CapabilityModelAdapter, Kind: "transform", Version: 1, ErrorPolicy: pluginhost.ErrorPolicyPropagate},
		adapters:                []pluginhost.ModelAdapterDescriptor{{ID: "test.compact", Version: 1, Mode: "direct", Operations: []string{"compact"}}},
		invoke: func(context.Context, pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{"error":{"code":"invalid_request","message":"compaction refused"}}`)}, nil
		},
	}
	resources := NewCapacityResources(1)
	defer resources.Close(context.Background())
	policy, err := capacityProviderFactory(resources, false).Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	physical, err := capacityProviderFactory(resources, true).Build(NativeProviderContext{})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &runtimeServiceConsumer{id: modelExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: capacityPolicyService, MajorVersion: 1, Required: true}, {Name: capacityResourceService, MajorVersion: 1, Required: true}}}
	host := pluginhost.New(caller, consumer, policy, physical)
	registry, conflicts := pluginhost.BuildServiceRegistry(caller, consumer, policy, physical)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	req, err := providers.EnsureInferenceAttemptContext(context.Background(), providers.ChatRequest{Provider: "fixture", Model: "fixture", Adapter: "test.compact"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&modelAdapterClient{host: host}).auxiliary(context.Background(), "compact", req, "", "")
	if err == nil {
		t.Fatal("model output error reported success")
	}
	submissions := req.Attempt.SubmissionSnapshot()
	if len(submissions) != 1 || submissions[0].Outcome != providers.InferenceSubmissionFailed {
		t.Fatalf("failed compaction receipt=%+v", submissions)
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if len(resources.slots) != 0 { // Settled scopes may retain empty channels; inspect occupancy only.
		for name, slot := range resources.slots {
			if len(slot) != 0 {
				t.Fatalf("compaction retained physical slot %q", name)
			}
		}
	}
}
