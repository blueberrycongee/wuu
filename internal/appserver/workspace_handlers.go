package appserver

import (
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

// handleWorkspaceList exposes the host's registered workspaces (the desktop
// sidebar's projects.json) so a phone can offer a workspace picker. It is
// read-only: thread/start carries the chosen workspace for a conversation.
func (s *Server) handleWorkspaceList(req Request) error {
	list, err := workspaces.List(s.rt.WuuHome)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	out := make([]WorkspaceInfo, 0, len(list))
	for _, ws := range list {
		out = append(out, WorkspaceInfo{
			ID:   ws.ID,
			Name: ws.Name,
			Path: ws.Root,
		})
	}
	return s.writeResponse(req.ID, WorkspaceListResult{
		Workspaces: out,
		Current:    s.rt.RootDir,
		CurrentID:  s.rt.WorkspaceID,
	}, nil)
}
