package appserver

import "github.com/blueberrycongee/wuu/internal/runtime"

type ServerOption func(*Server) error

// RuntimeBindings connects all connections of one embedding runtime to the
// same generation-owned coordination providers without process-global state.
type RuntimeBindings struct{ project *ProjectCoordinatorBridge }

func NewRuntimeBindings() *RuntimeBindings {
	return &RuntimeBindings{project: NewProjectCoordinatorBridge()}
}
func (b *RuntimeBindings) NativeProviders() []runtime.NativeProviderFactory {
	return []runtime.NativeProviderFactory{b.project.ProviderFactory(), b.project.MechanismFactory(), ProjectCoordinatorConsumerFactory()}
}
func WithRuntimeBindings(bindings *RuntimeBindings) ServerOption {
	return func(server *Server) error { _, err := bindings.project.Bind(server); return err }
}
