package runtime

import (
	"github.com/blueberrycongee/wuu/internal/titleservice"
)

// TitlePolicy captures the optional selection from this retained state. Absence
// disables title generation; failures after selection never choose a default.
func (s *State) TitlePolicy() *titleservice.Client {
	if s.Host == nil {
		// Generationless embedded runtimes explicitly assemble the native policy.
		return titleservice.NewClient(titleservice.InvokeNative)
	}
	registry := s.Host.ServiceRegistry()
	if registry == nil || !registry.HasProviderFor(sessionExecutionConsumer, titleservice.ServiceName, 1) {
		return nil
	}
	return titleservice.NewClient(boundServiceCall(s.Host, sessionExecutionConsumer, titleservice.ServiceName))
}
