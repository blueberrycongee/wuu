package appserver

import (
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type ServerOption func(*Server) error

// RuntimeBindings connects all connections of one embedding runtime to the
// same generation-owned coordination providers without process-global state.
// One binding belongs to one embedding Runtime/UserHost lifetime; closing that
// host ends the binding. Transport connections may attach/detach independently.
type RuntimeBindings struct {
	desktopSessionTransport *desktopSessionTransportBridge
	managedSessions         *managedSessionBridge
	personalCoordination    *personalCoordinatorBridge
	personalEffects         *PersonalEffectBridge
}

func NewRuntimeBindings() *RuntimeBindings {
	return &RuntimeBindings{desktopSessionTransport: newDesktopSessionTransportBridge(), managedSessions: newManagedSessionBridge(), personalCoordination: newPersonalCoordinatorBridge(), personalEffects: NewPersonalEffectBridge()}
}
func (b *RuntimeBindings) NativeProviders() []runtime.NativeProviderFactory {
	return []runtime.NativeProviderFactory{b.desktopSessionTransport.factory(), b.personalEffects.ProviderFactory(), b.managedSessions.MechanismFactory(), PersonalActionsConsumerFactory(), PersonalCoordinationConsumerFactory()}
}
func WithRuntimeBindings(bindings *RuntimeBindings) ServerOption {
	return func(server *Server) error {
		bindings.desktopSessionTransport.bind(server)
		server.personalEffects = bindings.personalEffects
		server.personalCoordination = bindings.personalCoordination
		_, err := bindings.managedSessions.Bind(server)
		return err
	}
}
