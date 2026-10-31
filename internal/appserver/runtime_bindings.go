package appserver

import (
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type ServerOption func(*Server) error

// RuntimeBindings connects all connections of one embedding runtime to the
// same generation-owned coordination providers without process-global state.
type RuntimeBindings struct {
	project          *ProjectCoordinatorBridge
	personalAccounts PersonalAccountsFactory
}

func NewRuntimeBindings() *RuntimeBindings {
	return &RuntimeBindings{project: NewProjectCoordinatorBridge()}
}
func (b *RuntimeBindings) NativeProviders() []runtime.NativeProviderFactory {
	factory := b.personalAccounts
	return []runtime.NativeProviderFactory{b.project.ProviderFactory(), b.project.MechanismFactory(), ProjectCoordinatorConsumerFactory(), PersonalActionsConsumerFactory(),
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
		server.personalAccountsRouted = true
		_, err := bindings.project.Bind(server)
		return err
	}
}

// SetPersonalAccountsFactory configures the native account provider before the
// runtime is built. Disabled or replaced providers never invoke this factory.
func (b *RuntimeBindings) SetPersonalAccountsFactory(factory PersonalAccountsFactory) {
	b.personalAccounts = factory
}
