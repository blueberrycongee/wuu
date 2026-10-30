package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/mcp"
)

func (s *Server) handleMCPAuthStart(ctx context.Context, req Request) error {
	var params MCPAuthStartParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.admitMCPConnectorAction(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	result, err := s.rt.StartMCPAuth(ctx, s.sessionOperationCtx, params.Name, params.RedirectURI)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, MCPAuthStartResult{
		AuthorizationURL: result.AuthorizationURL,
		State:            result.State,
		Scopes:           result.Scopes,
	}, nil)
}

func (s *Server) handleMCPAuthStatus(ctx context.Context, req Request) error {
	params, err := parseMCPServerActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.checkMCPConnectorDomain(params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	mgr, err := s.mcpManager()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	status, err := mgr.OAuthStatus(ctx, params.Name)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, mcpAuthStatusResult(status), nil)
}

func (s *Server) handleMCPAuthFinish(ctx context.Context, req Request) error {
	params, err := parseMCPAuthFinishParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.admitMCPConnectorAction(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	serverStatus, authStatus, err := s.rt.FinishMCPAuth(ctx, params.Name, params.State, params.Code)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	server := mcpStatusFromRuntime(serverStatus)
	s.notifyMCPStatus(server)
	return s.writeResponse(req.ID, MCPAuthFinishResult{Auth: mcpAuthStatusResult(authStatus), Server: server}, nil)
}

func (s *Server) handleMCPAuthRemove(ctx context.Context, req Request) error {
	params, err := parseMCPServerActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.checkMCPConnectorDomain(params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	mgr, err := s.mcpManager()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.rt.CancelPendingMCPAuth(params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := mgr.RemoveOAuth(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	authStatus, err := mgr.OAuthStatus(ctx, params.Name)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	server := mcpStatusByName(mgr.Status(), params.Name)
	s.notifyMCPStatus(server)
	return s.writeResponse(req.ID, MCPAuthRemoveResult{Auth: mcpAuthStatusResult(authStatus), Server: server}, nil)
}

func (s *Server) handleMCPAuthCancel(ctx context.Context, req Request) error {
	var params MCPAuthCancelParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.checkMCPConnectorDomain(params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, map[string]bool{"ok": true}, s.rt.CancelMCPAuth(ctx, params.Name, params.State))
}

func parseMCPAuthFinishParams(req Request) (MCPAuthFinishParams, error) {
	var params MCPAuthFinishParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return params, fmt.Errorf("parse mcp OAuth finish params: %w", err)
		}
	}
	params.Name = strings.TrimSpace(params.Name)
	params.State = strings.TrimSpace(params.State)
	params.Code = strings.TrimSpace(params.Code)
	if params.Name == "" || params.State == "" || params.Code == "" {
		return params, errors.New("mcp server name, OAuth state, and authorization code are required")
	}
	return params, nil
}

func mcpAuthStatusResult(status mcp.OAuthStatus) MCPAuthStatusResult {
	result := MCPAuthStatusResult{
		Name:          status.ServerID,
		Authenticated: status.Authenticated,
		Scopes:        append([]string(nil), status.Scopes...),
	}
	if !status.ExpiresAt.IsZero() {
		result.ExpiresAt = status.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return result
}
