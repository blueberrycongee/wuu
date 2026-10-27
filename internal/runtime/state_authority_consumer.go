package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const stateAuthorityConsumer = "wuu.state-authority"

// The authority is a host mechanism independent of session storage. Every
// document consumer depends on its lifetime, so final writes finish before
// admission closes and the selected provider is released.
type runtimeStateAuthority struct {
	*runtimeServiceConsumer
	authority *StateAuthority
	documents persistence.Documents
}

func newRuntimeStateAuthority(host *pluginhost.Host, registry *pluginhost.ServiceRegistry, authority *StateAuthority) *runtimeStateAuthority {
	consumer := &runtimeStateAuthority{
		runtimeServiceConsumer: &runtimeServiceConsumer{id: stateAuthorityConsumer, state: pluginhost.StatePrepared,
			requirements: []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}},
		authority: authority,
	}
	consumer.documents = persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if consumer.Status().State == pluginhost.StateActive {
			result, err := host.CallService(ctx, consumer.ID(), persistence.ServiceName, method, params)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		result, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return consumer
}

func (c *runtimeStateAuthority) Prepare(ctx context.Context) error {
	return c.authority.Preflight(ctx, c.documents)
}

func (c *runtimeStateAuthority) Activate(ctx context.Context) error {
	if err := c.authority.Activate(ctx, c.documents); err != nil {
		return err
	}
	return c.runtimeServiceConsumer.Activate(ctx)
}

func (c *runtimeStateAuthority) Close(ctx context.Context) error {
	err := c.authority.Close(ctx)
	_ = c.runtimeServiceConsumer.Close(ctx)
	return err
}

// One composition has one logical document dataset and one authority boundary.
// A scoped route may connect implementation adapters beneath that boundary; it
// cannot give an ordinary consumer a path around admission or select a second
// dataset. Independent datasets belong in isolated compositions instead.
func stateProviderChain(registry *pluginhost.ServiceRegistry) (map[string]bool, error) {
	snapshot := registry.Snapshot(0)
	selected, found := registry.ProviderPluginID(persistence.ServiceName, 1)
	chain := map[string]bool{}
	edges := map[string]string{}
	for found {
		if chain[selected] {
			return nil, fmt.Errorf("state.documents delegation cycle at %q", selected)
		}
		chain[selected] = true
		if !registry.RuntimeConsumesService(selected, persistence.ServiceName, 1) {
			break
		}
		next, exists := registry.ProviderPluginIDFor(selected, persistence.ServiceName, 1)
		if !exists {
			return nil, fmt.Errorf("state.documents delegate for %q is unavailable", selected)
		}
		edges[selected] = next
		selected = next
	}
	for _, route := range snapshot.Services {
		major, valid := pluginhost.ServiceVersionMajor(route.Version)
		if route.Service == persistence.ServiceName && valid && major == 1 && route.Consumer != "" && edges[route.Consumer] != route.Provider {
			return nil, fmt.Errorf("state.documents scoped binding for %q is outside the selected provider delegation chain; use an isolated composition for independent state", route.Consumer)
		}
	}
	return chain, nil
}
