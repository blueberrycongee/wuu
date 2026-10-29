package runtime

// UserHostPluginAvailable reports current owner availability for user-host
// session cancellation. Same-ID replacement remains available; a revoked
// current implementation does not. Legacy Coding callers must not use this
// result to change their session lifetime policy.
func (s *Session) UserHostPluginAvailable(pluginID string) bool {
	if s == nil || !s.UserBinding().IsUser() {
		return false
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	for _, item := range s.ActivePlugins {
		if item.ID != pluginID {
			continue
		}
		return s.pluginGeneration == nil || s.pluginGeneration.PluginAvailable(pluginID)
	}
	return false
}
