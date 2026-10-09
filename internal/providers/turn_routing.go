package providers

import "sync"

// TurnRouting keeps an opaque server routing token for one model/tool loop.
// It is transient transport state, never conversation content or durable state.
// A different provider/credential scope starts fresh; the first token wins.
type TurnRouting struct {
	mu    sync.Mutex
	scope string
	token string
}

func (r *TurnRouting) Token(scope string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.scope != scope {
		r.scope, r.token = scope, ""
	}
	return r.token
}

func (r *TurnRouting) Remember(scope, token string) {
	if r == nil || token == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Ignore a late response after a provider or credential change.
	if r.scope == scope && r.token == "" {
		r.token = token
	}
}
