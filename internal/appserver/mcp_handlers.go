package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/mcp"
)

func (s *Server) handleMCPList(req Request) error {
	result, err := s.mcpServerList(context.Background())
	return s.writeResponse(req.ID, result, err)
}

func (s *Server) handleMCPConnect(ctx context.Context, req Request) error {
	params, err := parseMCPServerActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.admitMCPConnectorAction(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	mgr, err := s.mcpManager()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := mgr.Connect(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// Only this owner-initiated connect probes; generic/background refresh does not.
	// Identity failure never changes a successfully established transport.
	_, _ = mgr.RefreshIdentity(ctx, params.Name)
	status := mcpStatusByName(mgr.Status(), params.Name)
	s.notifyMCPStatus(status)
	return s.writeResponse(req.ID, MCPServerActionResult{Status: status}, nil)
}

func (s *Server) handleMCPDisconnect(req Request) error {
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
	if err := mgr.Disconnect(params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	status := mcpStatusByName(mgr.Status(), params.Name)
	s.notifyMCPStatus(status)
	return s.writeResponse(req.ID, MCPServerActionResult{Status: status}, nil)
}

func (s *Server) handleMCPRefresh(ctx context.Context, req Request) error {
	params, err := parseMCPServerActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.admitMCPConnectorAction(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	mgr, err := s.mcpManager()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := mgr.Refresh(ctx, params.Name); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	status := mcpStatusByName(mgr.Status(), params.Name)
	s.notifyMCPStatus(status)
	return s.writeResponse(req.ID, MCPServerActionResult{Status: status}, nil)
}

func parseMCPServerActionParams(req Request) (MCPServerActionParams, error) {
	var params MCPServerActionParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return params, fmt.Errorf("parse mcp params: %w", err)
		}
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return params, errors.New("mcp server name is required")
	}
	return params, nil
}

func (s *Server) mcpManager() (*mcp.Manager, error) {
	if s == nil || s.rt == nil || s.rt.Toolkit == nil || s.rt.Toolkit.MCPManager() == nil {
		return nil, errors.New("mcp manager is unavailable")
	}
	return s.rt.Toolkit.MCPManager(), nil
}

func mcpStatusList(statuses map[string]mcp.ServerStatus) []MCPServerStatus {
	out := make([]MCPServerStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, mcpStatusFromRuntime(status))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func mcpStatusByName(statuses map[string]mcp.ServerStatus, name string) MCPServerStatus {
	status, ok := statuses[strings.TrimSpace(name)]
	if !ok {
		return MCPServerStatus{Name: strings.TrimSpace(name), State: string(mcp.MCPServerStateFailed), Error: "mcp server status not found"}
	}
	return mcpStatusFromRuntime(status)
}

func mcpStatusFromRuntime(status mcp.ServerStatus) MCPServerStatus {
	state := string(status.State)
	if state == "" {
		if status.Connected {
			state = string(mcp.MCPServerStateConnected)
		} else if status.Error != "" {
			state = string(mcp.MCPServerStateFailed)
		} else {
			state = string(mcp.MCPServerStateConfigured)
		}
	}
	return MCPServerStatus{
		Name:       status.Name,
		State:      state,
		AuthStatus: string(status.AuthStatus),
		Connected:  status.Connected,
		ToolCount:  status.ToolCount,
		Error:      status.Error,
	}
}

func (s *Server) notifyMCPStatus(status MCPServerStatus) {
	if status.Name == "" {
		return
	}
	_ = s.writeNotification(NotificationMCPStatusUpdated, status)
}
