package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

type projectSessionView struct {
	SessionID             string                    `json:"session_id"`
	Title                 string                    `json:"title"`
	State                 string                    `json:"state"`
	Control               string                    `json:"control,omitempty"`
	Workspace             string                    `json:"workspace"`
	LatestCompletedTurnID string                    `json:"latest_completed_turn_id,omitempty"`
	TurnID                string                    `json:"turn_id,omitempty"`
	Candidates            []projectCandidateSummary `json:"candidates,omitempty"`
}

type projectCandidateSummary struct {
	TurnID       string   `json:"turn_id"`
	ChangedFiles []string `json:"changed_files"`
	Disposition  string   `json:"disposition,omitempty"`
}

// projectSessionHandler serves the session tool of one coordinator.
func (s *Server) projectSessionHandler(projectID string) tools.ProjectSessionHandler {
	return func(ctx context.Context, callID string, request tools.ProjectSessionRequest) (any, error) {
		project, live := s.projectCoordinator(projectID)
		if !live {
			return nil, errors.New("this project is no longer active")
		}
		switch request.Action {
		case "list":
			return s.listProjectSessions(projectID)
		case "create":
			return s.createProjectSession(ctx, project, callID, request)
		case "send":
			return s.sendProjectSession(ctx, project, "project:"+projectID+":"+callID, request)
		case "stop":
			if _, err := s.projectManagedSession(projectID, request.SessionID); err != nil {
				return nil, err
			}
			if err := s.interruptOwnedThreadExecution(request.SessionID); err != nil {
				return nil, err
			}
			return map[string]string{"session_id": request.SessionID, "state": "stopping"}, nil
		case "inspect":
			if _, err := s.projectManagedSession(projectID, request.SessionID); err != nil {
				return nil, err
			}
			return s.sessionTranscriptExcerpt(ctx, request.SessionID, request.Query, request.Before, request.Limit)
		default:
			return nil, errors.New("action must be list, create, send, stop or inspect")
		}
	}
}

// projectSessions lists a project's live managed sessions, oldest first.
func (s *Server) projectSessions(projectID string) ([]session.Session, error) {
	all, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return nil, err
	}
	var managed []session.Session
	for index := len(all) - 1; index >= 0; index-- {
		if metadata := all[index]; metadata.Source == projectSessionSource && metadata.ParentID == projectID && metadata.ArchivedAt == nil {
			managed = append(managed, metadata)
		}
	}
	return managed, nil
}

func (s *Server) listProjectSessions(projectID string) (any, error) {
	managed, err := s.projectSessions(projectID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(managed))
	for index, metadata := range managed {
		ids[index] = metadata.ID
	}
	candidates, err := session.ListCandidates(s.rt.SessionDir, ids)
	if err != nil {
		return nil, err
	}
	views := make([]projectSessionView, 0, len(managed))
	for _, metadata := range managed {
		view, err := s.projectSessionView(metadata)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.SessionID == metadata.ID {
				view.Candidates = append(view.Candidates, projectCandidateSummary{TurnID: candidate.TurnID, ChangedFiles: candidate.ChangedFiles, Disposition: candidate.Disposition})
			}
		}
		views = append(views, view)
	}
	return map[string]any{"sessions": views}, nil
}

func (s *Server) projectSessionView(metadata session.Session) (projectSessionView, error) {
	view := projectSessionView{SessionID: metadata.ID, Title: metadata.Title, State: "idle", Workspace: "shared", LatestCompletedTurnID: metadata.LatestCompletedTurnID}
	if metadata.WorktreePath != "" {
		view.Workspace = "worktree"
	}
	if active, err := session.ThreadExecutionActive(s.rt.SessionDir, metadata.ID); err != nil {
		return view, err
	} else if active {
		view.State = "running"
	}
	if control, ok, err := session.ReadControl(s.rt.SessionDir, metadata.ID); err != nil {
		return view, err
	} else if ok {
		view.Control = control.State
	}
	return view, nil
}

func (s *Server) createProjectSession(ctx context.Context, project session.Session, callID string, request tools.ProjectSessionRequest) (any, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, errors.New("create needs a brief in prompt")
	}
	requestID := "project:" + project.ID + ":" + callID
	if existing, found, err := session.FindManagedByRequest(s.rt.SessionDir, projectSessionOwner, requestID); err != nil {
		return nil, err
	} else if found {
		return s.projectSessionView(existing)
	}
	workspace := strings.TrimSpace(request.Workspace)
	if workspace == "" {
		workspace = "shared"
		if worktree.IsGitRepo(project.CWD) {
			workspace = "worktree"
		}
	}
	if workspace != "shared" && workspace != "worktree" {
		return nil, errors.New("workspace must be worktree or shared")
	}
	baseRevision := ""
	if request.Candidate != nil {
		if _, err := s.projectManagedSession(project.ID, request.Candidate.SessionID); err != nil {
			return nil, err
		}
		candidate, found, err := session.FindCandidate(s.rt.SessionDir, request.Candidate.SessionID, request.Candidate.TurnID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("candidate not found")
		}
		baseRevision, workspace = candidate.Revision, "worktree"
	}
	coordinator, err := s.ensureThreadLoaded(project.ID)
	if err != nil {
		return nil, err
	}
	coordinator.mu.Lock()
	provider, model, variant, effort := coordinator.ModelProvider, coordinator.Model, coordinator.ModelVariant, coordinator.ModelEffort
	coordinator.mu.Unlock()
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = excerpt(prompt, 60)
	}
	th, err := s.createHostSessionThreadAtRevision(projectSessionOwner, projectSessionSource, "", pluginhost.SessionCreateParams{
		RequestID: requestID, Name: title, Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		ParentSessionID: project.ID, Workspace: workspace, WorkspaceID: project.WorkspaceID,
		// Sessions write, so they run in the workspace's default mode rather
		// than inheriting the coordinator's read-only pin.
		Provider: provider, Model: model, Variant: variant, Effort: effort,
		PermissionMode: s.currentSessionRuntimeSelection().PermissionMode,
		Instructions:   projectSessionInstructions,
	}, baseRevision)
	if err != nil {
		return nil, err
	}
	s.controlMu.Lock()
	_, err = session.ChangeControl(s.rt.SessionDir, th.ID, project.ID, session.ControlActive, 0)
	s.controlMu.Unlock()
	if err != nil {
		return nil, err
	}
	s.publishSessionControl(th.ID)
	return s.sendProjectSession(ctx, project, requestID, tools.ProjectSessionRequest{SessionID: th.ID, Prompt: prompt})
}

// sendProjectSession starts a turn or steers the running one. Input the
// session cannot admit now fails instead of waiting in a volatile queue.
func (s *Server) sendProjectSession(ctx context.Context, project session.Session, clientID string, request tools.ProjectSessionRequest) (any, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, errors.New("send needs an instruction in prompt")
	}
	metadata, err := s.projectManagedSession(project.ID, request.SessionID)
	if err != nil {
		return nil, err
	}
	control, ok, err := session.ReadControl(s.rt.SessionDir, metadata.ID)
	if err != nil {
		return nil, err
	}
	if !ok || control.ManagerID != project.ID || control.State != session.ControlActive {
		return nil, session.ErrControlChanged
	}
	th, err := s.ensureThreadLoaded(metadata.ID)
	if err != nil {
		return nil, err
	}
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return nil, err
	}
	snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
	snapshot.Control = &control
	msg := providers.ChatMessage{
		Role: "user", Content: prompt, ClientID: clientID, Name: project.Title,
		Origin: pluginhost.SessionInputPlugin, Cause: "project",
		PresentationKind: pluginhost.SessionPresentationSessionMessage, RelatedSessionID: project.ID, ReadOnly: true,
	}
	result, admitted, err := s.trySubmitSessionInput(ctx, th, msg, pluginhost.SessionIfRunningSteer, snapshot)
	if err != nil {
		return nil, err
	}
	if !admitted {
		return nil, errors.New("the session is busy with a step that cannot take input; send again after it finishes")
	}
	metadata, _, err = session.Find(s.rt.SessionDir, metadata.ID)
	if err != nil {
		return nil, err
	}
	view, err := s.projectSessionView(metadata)
	view.TurnID = result.TurnID
	return view, err
}
