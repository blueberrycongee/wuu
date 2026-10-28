package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// requestAccessContext restores transport provenance for legacy synchronous
// handlers. It never substitutes caller JSON for an authenticated principal.
func (s *Server) requestAccessContext(req Request) context.Context {
	s.access.mu.Lock()
	pending, ok := s.access.pending[string(req.ID)]
	s.access.mu.Unlock()
	if ok {
		return pending.ctx
	}
	return principal.WithContext(context.Background(), s.ConnectionPrincipal())
}

// requireRequestAccess is the admission fence after locks/provider waits and
// before a protected effect. Effective attributes replace requested defaults and
// remain bound to the response/replay. Accepted irreversible work is not rolled
// back on later revocation; every new admission and delivery is checked afresh.
func (s *Server) requireRequestAccess(req Request, action string, effects map[string]json.RawMessage, resolved ...access.Resource) (context.Context, error) {
	s.access.mu.Lock()
	pending, tracked := s.access.pending[string(req.ID)]
	s.access.mu.Unlock()
	ctx := s.requestAccessContext(req)
	resources := resolved
	delegated := false
	if tracked {
		ctx = pending.ctx
		action = pending.action
		delegated = pending.delegated
		resources = append(append([]access.Resource(nil), pending.resources...), resolved...)
	} else {
		if !s.Principal(ctx).IsOwner() {
			return ctx, &access.Denied{Action: action, Reason: "untracked-admission"}
		}
		if len(resources) == 0 {
			req.Method = action
			var err error
			resources, err = s.requestResources(req)
			if err != nil {
				return ctx, err
			}
		}
	}
	attrs := access.AttributesFromContext(ctx)
	for key, value := range effects {
		attrs[key] = value
	}
	ctx = access.WithAttributes(ctx, attrs)
	if err := s.authorizeResource(ctx, action, delegated, resources...); err != nil {
		return ctx, err
	}
	if tracked {
		s.access.mu.Lock()
		defer s.access.mu.Unlock()
		current, ok := s.access.pending[string(req.ID)]
		if !ok || current.generation != pending.generation {
			return ctx, errors.New("authorized request changed before admission")
		}
		current.ctx = ctx
		current.resources = resources
		s.access.pending[string(req.ID)] = current
	}
	return ctx, nil
}

func executionEffects(provider, model, permission, engine string) map[string]json.RawMessage {
	effects := make(map[string]json.RawMessage, 4)
	for key, value := range map[string]string{"provider": provider, "model": model, "permission_mode": permission, "engine": engine} {
		effects[key], _ = json.Marshal(value)
	}
	return effects
}
