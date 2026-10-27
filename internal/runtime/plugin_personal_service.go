package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type PersonalHostCall struct {
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
}
type PersonalHostHandler func(context.Context, string, json.RawMessage, string) (json.RawMessage, error)

func (r *PluginSessionRouter) BindPersonalHost(handler PersonalHostHandler) func() {
	r.mu.Lock()
	r.personalBinding++
	generation := r.personalBinding
	r.personalHost = handler
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.personalBinding == generation {
				r.personalHost = nil
			}
		})
	}
}

func (s *pluginHostServices) personalService(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if s.binding.Validate() != nil || !s.binding.IsUser() || s.binding.Domain() != "personal" {
		return nil, errors.New("Personal host service is unavailable")
	}
	var call PersonalHostCall
	if err := decodeServiceParams(raw, &call); err != nil {
		return nil, err
	}
	verifiedSession := ""
	if strings.HasPrefix(call.Operation, "calendar.read.") || call.Operation == "review.execution.status" || call.Operation == "connectors.call" || call.Operation == "connectors.poll" || strings.HasPrefix(call.Operation, "connectors.read.") {
		metadata, found, err := session.Find(s.sessionDir, call.SessionID)
		if err != nil {
			return nil, err
		}
		if !found || !s.binding.Matches(userspace.Binding{UserSpaceID: metadata.UserSpaceID, ProductDomain: metadata.ProductDomain}) || metadata.Visibility != "plugin" || !strings.HasPrefix(metadata.Owner, "plugin:") {
			return nil, errors.New("Personal read requires a host-resolved plugin session")
		}
		if (call.Operation == "review.execution.status" || strings.HasPrefix(call.Operation, "connectors.")) && metadata.Owner != "plugin:"+s.pluginID {
			return nil, errors.New("review status requires the owning plugin session")
		}
		verifiedSession = metadata.ID
	}
	if s.turnRouter == nil {
		return nil, errors.New("Personal host is unavailable")
	}
	s.turnRouter.mu.RLock()
	handler := s.turnRouter.personalHost
	s.turnRouter.mu.RUnlock()
	if handler == nil {
		return nil, errors.New("Personal host is unavailable")
	}
	value, err := handler(ctx, call.Operation, call.Input, verifiedSession)
	if err != nil {
		var failure *personalcalendar.Failure
		if errors.As(err, &failure) {
			return json.Marshal(map[string]any{"ok": false, "error": failure})
		}
		var connectorFailure *personalconnectors.Failure
		if errors.As(err, &connectorFailure) {
			return json.Marshal(map[string]any{"ok": false, "error": connectorFailure})
		}
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "value": value})
}
