package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) pluginSessionModel(ctx context.Context, pluginID string, params pluginhost.SessionModelParams) (pluginhost.SessionModelResult, error) {
	var result pluginhost.SessionModelResult
	if err := clientauthority.RequireOwner(ctx, s.rt.UserBinding()); err != nil {
		return result, err
	}
	params.SessionID, params.Provider, params.Model = strings.TrimSpace(params.SessionID), strings.TrimSpace(params.Provider), strings.TrimSpace(params.Model)
	if params.SessionID == "" || strings.TrimSpace(pluginID) == "" || ((params.Provider == "") != (params.Model == "")) {
		return result, errors.New("session and complete provider/model selection are required")
	}
	if err := s.authorizeUserSessionOperation(ctx, "inspect", params.SessionID); err != nil {
		return result, err
	}
	binding, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return result, err
	}
	defer binding.Close()
	metadata, found, err := binding.Sessions.Find(params.SessionID)
	if err != nil {
		return result, err
	}
	if !found {
		return result, session.ErrSessionNotFound
	}
	if err := s.requireUserSessionBinding(metadata); err != nil {
		return result, err
	}
	if metadata.Owner != "plugin:"+strings.TrimSpace(pluginID) {
		return result, errors.New("session is not owned by plugin")
	}
	if params.Provider != "" {
		if err := s.authorizeUserSessionOperation(ctx, "update", params.SessionID); err != nil {
			return result, err
		}
		th, _, release, err := s.selectThreadModel(ConfigModelUpdateParams{ThreadID: params.SessionID, Provider: params.Provider, Model: params.Model})
		if err != nil {
			return result, err
		}
		defer release()
		th.mu.Lock()
		result.Provider, result.Model = th.ModelProvider, th.Model
		th.mu.Unlock()
		return result, nil
	}
	return pluginhost.SessionModelResult{Provider: metadata.Provider, Model: metadata.Model}, nil
}
