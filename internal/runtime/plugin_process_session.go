package runtime

import (
	"context"
)

// BindProcessSessions supplies lifecycle metadata only for a process's captured
// conversation. It does not expose private session listing or history to plugins.
func (r *PluginSessionRouter) BindProcessSessions(resolve func(context.Context, string, bool) (string, bool, error)) func() {
	r.mu.Lock()
	r.processBinding++
	binding := r.processBinding
	r.processSession = resolve
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.processBinding == binding {
			r.processSession = nil
		}
	}
}

func (r *PluginSessionRouter) resolveProcessSession(ctx context.Context, id string, followParent bool) (string, bool, error) {
	if r == nil {
		return "", false, serviceError("session_unavailable", "session service unavailable")
	}
	r.mu.RLock()
	resolve := r.processSession
	r.mu.RUnlock()
	if resolve == nil {
		return "", false, serviceError("session_unavailable", "session service unavailable")
	}
	return resolve(ctx, id, followParent)
}
