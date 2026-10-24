package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

type selectedSessionFixture struct {
	*runtimeServiceConsumer
	calls int
}

func (p *selectedSessionFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: "session.domain", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "Find", InputSchema: "session.domain.Find.input.v1", OutputSchema: "session.domain.Find.output.v1"}}}}
}
func (p *selectedSessionFixture) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if call.Caller != sessionStateConsumer {
		return nil, serviceError("invalid_caller", call.Caller)
	}
	p.calls++
	return json.Marshal(struct {
		Values []any `json:"values"`
	}{Values: []any{session.Session{ID: "external-domain"}, true}})
}

// A runtime state must use the selected domain even without any document
// backend. Replacing that domain must not reroute a previously captured client.
func TestRuntimeSessionClientUsesCapturedSelectedDomain(t *testing.T) {
	ctx := context.Background()
	var previous *runtimeState
	for _, id := range []string{"first.domain", "second.domain"} {
		provider := &selectedSessionFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: id, state: pluginhost.StatePrepared}}
		consumer := &runtimeServiceConsumer{id: sessionStateConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: "session.domain", MajorVersion: 1, Required: true}}}
		registry, err := pluginhost.NewServiceRegistryWithBindings([]composition.Binding{{Service: "session.domain", Major: 1, Provider: id}})
		if err != nil {
			t.Fatal(err)
		}
		conflicts := registry.RegisterClients(provider, consumer)
		host := pluginhost.New(provider, consumer)
		host.AttachServiceRegistry(registry, conflicts)
		defer host.Close(ctx)
		if err := host.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		state := newRuntimeState(registry, "test")
		state.host = host
		row, found, err := state.sessions.Find("requested")
		if err != nil || !found || row.ID != "external-domain" || provider.calls != 1 {
			t.Fatalf("selected domain bypassed: %+v %v %v calls=%d", row, found, err, provider.calls)
		}
		if previous != nil {
			if _, _, err := previous.sessions.Find("old"); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 1 {
				t.Fatal("old capture switched to replacement domain")
			}
		}
		previous = state
	}
}
