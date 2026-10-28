package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
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

func TestNativeSessionDomainPreservesNestedAdmission(t *testing.T) {
	ctx := context.Background()
	registry := pluginhost.NewServiceRegistry()
	home := t.TempDir()
	documents := persistence.NewService(persistence.SQLiteModule, filepath.Join(home, "documents"), "sqlite")
	host := pluginhost.New()
	domain := newNativeSessionDomain(registry, home, host)
	consumer := &runtimeServiceConsumer{id: sessionStateConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: session.ServiceName, MajorVersion: 1, Required: true}}}
	clients := []pluginhost.Client{documents, domain, consumer}
	for _, client := range clients {
		host.Add(client)
	}
	host.AttachServiceRegistry(registry, registry.RegisterClients(clients...))
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	state := newRuntimeState(registry, "workspace")
	state.host, state.status = host, pluginhost.StateActive
	if _, _, err := state.sessions.Find("missing"); err != nil {
		t.Fatal(err)
	}
	host.StopBackground()
	if _, _, err := state.sessions.Find("missing"); err != nil {
		t.Fatalf("admitted native nested call failed after publication: %v", err)
	}
	if _, err := registry.Call(ctx, session.DefaultModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: "read", Params: json.RawMessage(`{"collection":"sessions","key":"missing"}`)}); err == nil || err.Code != "generation_closed" {
		t.Fatalf("autonomous documents call was not rejected by admission: %v", err)
	}
}
