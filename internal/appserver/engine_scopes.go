package appserver

import (
	"context"
	"slices"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type engineScope struct {
	generation *runtime.PluginGeneration
	release    func() error
}

// Warm scopes are public provider leases. Acquiring the replacement before
// releasing the previous scope preserves warm processes without pinning stale
// configuration or preventing admitted turns from draining their old provider.
func (s *Server) retainEngineScope(ctx context.Context, binding *runtime.State, id agentengine.EngineID) error {
	var descriptor struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := binding.CallEngine(ctx, id, "descriptor", struct{}{}, &descriptor); err != nil {
		return err
	}
	var release func() error
	if slices.Contains(descriptor.Capabilities, "warm-scope") {
		var err error
		release, err = binding.RetainEngine(ctx, id)
		if err != nil {
			return err
		}
	}
	s.engineScopeMu.Lock()
	if s.closed.Load() {
		s.engineScopeMu.Unlock()
		if release != nil {
			_ = release()
		}
		return errServerClosed
	}
	if s.engineScopes == nil {
		s.engineScopes = make(map[agentengine.EngineID]engineScope)
	}
	previous := s.engineScopes[id]
	s.engineScopes[id] = engineScope{generation: binding.Generation, release: release}
	s.engineScopeMu.Unlock()
	if previous.release != nil {
		if err := previous.release(); err != nil {
			providers.DebugLogf("engine scope release: %v", err)
		}
	}
	return nil
}

func (s *Server) releaseEngineScopes() {
	s.engineScopeMu.Lock()
	scopes := s.engineScopes
	s.engineScopes = nil
	s.engineScopeMu.Unlock()
	for _, scope := range scopes {
		if scope.release != nil {
			if err := scope.release(); err != nil {
				providers.DebugLogf("engine scope release: %v", err)
			}
		}
	}
}

// Publication retires idle warming resources; bound sessions retain their own
// references until callbacks and cleanup finish on their original generation.
func (s *Server) retireEngineScopes() {
	s.engineScopeMu.Lock()
	var retired []engineScope
	for id, scope := range s.engineScopes {
		if !s.rt.IsCurrentPluginGeneration(scope.generation) {
			retired = append(retired, scope)
			delete(s.engineScopes, id)
		}
	}
	s.engineScopeMu.Unlock()
	for _, scope := range retired {
		if scope.release != nil {
			if err := scope.release(); err != nil {
				providers.DebugLogf("engine scope retirement: %v", err)
			}
		}
	}
}
