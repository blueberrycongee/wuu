package appserver

import (
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type ServerOption func(*Server) error

// RuntimeBindings connects all connections of one embedding runtime to the
// same generation-owned coordination providers without process-global state.
// One binding belongs to one embedding Runtime/UserHost lifetime; closing that
// host ends the binding. Transport connections may attach/detach independently.
type RuntimeBindings struct {
	project              *ProjectCoordinatorBridge
	personalCoordination *personalCoordinatorBridge
	personalEffects      *PersonalEffectBridge
	personalAccounts     PersonalAccountsFactory
}

func NewRuntimeBindings() *RuntimeBindings {
	return &RuntimeBindings{project: NewProjectCoordinatorBridge(), personalCoordination: newPersonalCoordinatorBridge(), personalEffects: NewPersonalEffectBridge()}
}
func (b *RuntimeBindings) NativeProviders() []runtime.NativeProviderFactory {
	factory := b.personalAccounts
	return []runtime.NativeProviderFactory{b.personalEffects.ProviderFactory(), b.project.ProviderFactory(), b.project.MechanismFactory(), ProjectCoordinatorConsumerFactory(), PersonalActionsConsumerFactory(), PersonalCoordinationConsumerFactory(),
		{ID: PersonalAccountsModule, DefaultEnabled: true, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
			return newPersonalAccountsProvider(input, factory), nil
		}},
		{ID: PersonalAccountsConsumer, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
			return &personalAccountsConsumer{}, nil
		}},
	}
}
func WithRuntimeBindings(bindings *RuntimeBindings) ServerOption {
	return func(server *Server) error {
		server.personalEffects = bindings.personalEffects
		server.personalAccountsRouted = true
		server.personalCoordination = bindings.personalCoordination
		_, err := bindings.project.Bind(server)
		return err
	}
}

// SetPersonalAccountsFactory configures the native account provider before the
// runtime is built. Disabled or replaced providers never invoke this factory.
func (b *RuntimeBindings) SetPersonalAccountsFactory(factory PersonalAccountsFactory) {
	b.personalAccounts = factory
}
