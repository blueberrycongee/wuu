package pluginhost

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type fakeClient struct {
	id       string
	status   Status
	closed   bool
	closeLog *[]string
}

type fakeCapabilityClient struct {
	*fakeClient
	capabilities []CapabilityDescriptor
	invoke       func(CapabilityInvokeParams) (CapabilityInvokeResult, error)
}

func (f *fakeCapabilityClient) ProtocolVersion() int { return CapabilityProtocolVersion }
func (f *fakeCapabilityClient) Capabilities() []CapabilityDescriptor {
	return cloneCapabilityDescriptors(f.capabilities)
}
func (f *fakeCapabilityClient) InvokeCapability(_ context.Context, params CapabilityInvokeParams) (CapabilityInvokeResult, error) {
	if f.invoke != nil {
		return f.invoke(params)
	}
	return CapabilityInvokeResult{}, nil
}

func (f *fakeClient) ID() string     { return f.id }
func (f *fakeClient) Status() Status { return f.status }
func (f *fakeClient) Close(context.Context) error {
	f.closed = true
	if f.closeLog != nil {
		*f.closeLog = append(*f.closeLog, f.id)
	}
	return nil
}

func TestInvokeCapabilityRejectsUnknownOutputFields(t *testing.T) {
	client := &fakeCapabilityClient{
		fakeClient:   &fakeClient{id: "strict", status: Status{ID: "strict", State: StateActive}},
		capabilities: []CapabilityDescriptor{{ID: CapabilityAgentSystemPromptSection, Kind: "transform", Version: 1}},
		invoke: func(CapabilityInvokeParams) (CapabilityInvokeResult, error) {
			return CapabilityInvokeResult{Output: json.RawMessage(`{"text":"ok","extra":true}`)}, nil
		},
	}
	host := New(client)
	registered := host.Capabilities(CapabilityAgentSystemPromptSection)
	var output SystemPromptSectionOutput
	err := host.InvokeCapability(context.Background(), registered[0], SystemPromptSectionInput{}, &output)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want strict typed output rejection", err)
	}
}

func TestCapabilitySelectsExactActivePluginOwner(t *testing.T) {
	capability := CapabilityDescriptor{ID: CapabilityPluginClientRequest, Kind: "decision", Version: 1}
	host := New(
		&fakeCapabilityClient{fakeClient: &fakeClient{id: "one", status: Status{ID: "one", State: StateActive}}, capabilities: []CapabilityDescriptor{capability}},
		&fakeCapabilityClient{fakeClient: &fakeClient{id: "two", status: Status{ID: "two", State: StateActive}}, capabilities: []CapabilityDescriptor{capability}},
	)
	got, ok := host.Capability("two", CapabilityPluginClientRequest)
	if !ok || got.PluginID != "two" {
		t.Fatalf("capability = %+v, ok = %v", got, ok)
	}
	if _, ok := host.Capability("missing", CapabilityPluginClientRequest); ok {
		t.Fatal("missing plugin unexpectedly resolved a capability")
	}
}

func TestHostCloseUsesReverseInitializationOrder(t *testing.T) {
	var closed []string
	host := New(
		&fakeClient{id: "one", closeLog: &closed},
		&fakeClient{id: "two", closeLog: &closed},
	)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(closed, []string{"two", "one"}) {
		t.Fatalf("closed = %v", closed)
	}
}

func TestHostValidatesCapabilityDependenciesAndConflicts(t *testing.T) {
	client := func(id string, capabilities ...CapabilityDescriptor) *fakeCapabilityClient {
		return &fakeCapabilityClient{
			fakeClient:   &fakeClient{id: id, status: Status{ID: id, State: StateActive}},
			capabilities: capabilities,
		}
	}

	missing := New(client("one", CapabilityDescriptor{
		ID: "agent.capability.one", Kind: "transform", Version: 1, DependsOn: []string{"agent.capability.two"},
	}))
	if err := missing.ValidateCapabilities(); err == nil || !strings.Contains(err.Error(), "requires missing") {
		t.Fatalf("missing dependency error = %v", err)
	}

	conflict := New(
		client("one", CapabilityDescriptor{
			ID: "agent.capability.one", Kind: "transform", Version: 1, Conflicts: []string{"agent.capability.two"},
		}),
		client("two", CapabilityDescriptor{ID: "agent.capability.two", Kind: "observe", Version: 1}),
	)
	if err := conflict.ValidateCapabilities(); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflict error = %v", err)
	}
}
