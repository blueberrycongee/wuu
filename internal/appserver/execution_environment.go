package appserver

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) handleThreadExecutionEnvironment(req Request) error {
	var params struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return s.writeResponse(req.ID, nil, errors.New("valid thread_id is required"))
	}
	var binding session.Session
	if th := s.thread(id); th != nil {
		th.mu.Lock()
		binding = session.Session{CWD: th.CWD, EngineID: th.EngineID, WorkspaceID: th.WorkspaceID, WorktreeBaseRepo: th.WorktreeBaseRepo}
		th.mu.Unlock()
	} else {
		saved, found, err := session.FindExisting(s.rt.SessionDir, id)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if !found {
			return s.writeResponse(req.ID, nil, session.ErrSessionNotFound)
		}
		binding = relocatedSessionMetadata(saved, s.registeredWorkspaces())
	}
	if binding.WorkspaceID != "" {
		root, workspaceID, err := s.sessionWorkspace(binding)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if !s.ownsSessionWorkspace(root, workspaceID) {
			return s.writeResponse(req.ID, nil, errors.New("session environment must be read in its bound project runtime"))
		}
	}
	summary, err := s.rt.ExecutionEnvironmentSummary(id, binding.CWD)
	if err == nil && agentengine.NormalizeEngineID(binding.EngineID) != agentengine.EngineWuu {
		if summary.ID != "" {
			// Admission rejects a Wuu environment for native engines. Show the
			// incompatible pin rather than implying a usable host fallback.
			summary.State = "unsupported"
		} else {
			summary = executionenv.Summary{Name: binding.EngineID, Backend: "external", Root: binding.CWD, State: "external"}
		}
	}
	return s.writeResponse(req.ID, ThreadExecutionEnvironmentResult{ThreadID: id, Environment: summary}, err)
}
