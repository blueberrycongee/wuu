package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const stateAuthorityConsumer = "wuu.state-authority"

// The authority is a host mechanism independent of session storage. Every
// document consumer depends on its lifetime, so final writes finish before
// membership closes and the selected provider is released.
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
