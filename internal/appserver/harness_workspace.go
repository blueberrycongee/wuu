package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	MethodWorkspaceHarnessDispatch = "workspace/harness/dispatch"
	MethodHarnessDispatch          = "session/harness/dispatch"
)

type harnessWorkspaceRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRoot string `json:"workspace_root"`
	OperationID   string `json:"operation_id,omitempty"`
}

func (s *Server) harnessOperationWorkspace(op channels.HarnessOperation) (string, string, error) {
	p := op.Params
	if p.Action == "create" {
		if p.WorkspaceID == "" && p.WorkspaceRoot == "" {
			return "", "", errors.New("session creation has no workspace binding")
		}
		root, id, err := s.resolveSessionWorkspace(p.WorkspaceID, p.WorkspaceRoot)
		if err != nil {
			return "", "", err
		}
		if m, exists, err := session.Find(s.rt.SessionDir, p.SessionID); err != nil {
			return "", "", err
		} else if exists {
			actualRoot, actualID, err := s.sessionWorkspace(m)
			if err != nil || sessionWorkspacePath(actualRoot) != sessionWorkspacePath(root) || actualID != id {
				return "", "", errors.New("created session no longer matches its accepted workspace binding")
			}
		}
		return root, id, nil
	}
	m, err := s.sharedHarnessSession(p.SessionID)
	if err != nil {
		return "", "", err
	}
	root, id, err := s.sessionWorkspace(m)
	if err != nil {
		return "", "", err
	}
	// Older outbox entries did not carry a project binding. Their session's
	// persisted binding is authoritative; new entries must agree with it.
	if p.WorkspaceRoot != "" || p.WorkspaceID != "" {
		boundRoot, boundID, err := s.resolveSessionWorkspace(p.WorkspaceID, p.WorkspaceRoot)
		if err != nil || root != boundRoot || id != boundID {
			return "", "", errors.New("session workspace binding changed after input was accepted")
		}
	}
	return root, id, nil
}

func (s *Server) dispatchHarnessOperation(ctx context.Context, op *channels.HarnessOperation) error {
	root, id, err := s.harnessOperationWorkspace(*op)
	if err != nil {
		return err
	}
	request := harnessWorkspaceRequest{WorkspaceID: id, WorkspaceRoot: root, OperationID: op.ID}
	if s.ownsSessionWorkspace(root, id) {
		if err := s.applyHarnessDispatch(ctx, request); err != nil {
			return err
		}
	} else if _, err := s.callClient(ctx, MethodWorkspaceHarnessDispatch, request); err != nil {
		// Keep the durable operation pending after a transport failure. The
		// target may already have accepted it; recovery reconciles its receipt.
		return fmt.Errorf("cannot dispatch session to workspace %q: %w", root, err)
	}
	*op, err = s.channelService.HarnessOperation(ctx, op.ID)
	return err
}

func (s *Server) applyHarnessDispatch(ctx context.Context, p harnessWorkspaceRequest) error {
	if !s.ownsSessionWorkspace(p.WorkspaceRoot, p.WorkspaceID) {
		return errors.New("dispatch reached a different workspace runtime")
	}
	if _, _, err := s.resolveSessionWorkspace(p.WorkspaceID, p.WorkspaceRoot); err != nil {
		return err
	}
	if p.OperationID == "" {
		return s.reconcileLocalHarnessSessions(ctx)
	}
	s.harnessMu.Lock()
	defer s.harnessMu.Unlock()
	op, err := s.channelService.HarnessOperation(ctx, p.OperationID)
	if err != nil {
		return err
	}
	root, id, err := s.harnessOperationWorkspace(op)
	if err != nil {
		return err
	}
	if !s.ownsSessionWorkspace(root, id) {
		return errors.New("operation belongs to another workspace")
	}
	if op.State == "pending" {
		err := s.applyHarnessOperationLocked(ctx, &op)
		if errors.Is(err, errHarnessMedia) {
			// A rejected evidence handoff must not later start as a pending
			// text-only task. The synchronous caller receives the exact failure.
			op.State, op.Error = "failed", err.Error()
			return errors.Join(err, s.channelService.PutHarnessOperation(ctx, op))
		}
		return err
	}
	return nil
}

func (s *Server) handleHarnessDispatch(ctx context.Context, req Request) error {
	var p harnessWorkspaceRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	err := s.applyHarnessDispatch(ctx, p)
	return s.writeResponse(req.ID, struct{}{}, err)
}

func (s *Server) reconcileHarnessSessions(ctx context.Context) error {
	if err := s.reconcileLocalHarnessSessions(ctx); err != nil {
		return err
	}
	if !s.supportsClientMethod(MethodWorkspaceHarnessDispatch) {
		return nil
	}
	// Route only outstanding work. Idle managed sessions must not keep every
	// registered workspace resident. Do not hold harnessMu across host RPCs:
	// two projects may simultaneously have work for each other.
	targets := map[string]harnessWorkspaceRequest{}
	add := func(root, id string) {
		if !s.ownsSessionWorkspace(root, id) {
			targets[root] = harnessWorkspaceRequest{WorkspaceID: id, WorkspaceRoot: root}
		}
	}
	ops, err := s.channelService.PendingHarnessOperations(ctx)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.State == "submitted" {
			if active, err := session.ThreadExecutionActive(s.rt.SessionDir, op.Params.SessionID); err != nil {
				return err
			} else if active {
				continue
			}
		}
		if root, id, err := s.harnessOperationWorkspace(op); err == nil {
			add(root, id)
		}
	}
	links, err := s.channelService.HarnessLinks(ctx, "", "")
	if err != nil {
		return err
	}
	for _, link := range links {
		if !link.Active {
			continue
		}
		m, err := s.sharedHarnessSession(link.SessionID)
		if err == nil && m.LatestCompletedTurnID != "" && m.LatestCompletedTurnID != link.LastTurnID {
			if root, id, err := s.sessionWorkspace(m); err == nil {
				add(root, id)
			}
		}
	}
	var failures []error
	for _, p := range targets {
		if _, err := s.callClient(ctx, MethodWorkspaceHarnessDispatch, p); err != nil {
			failures = append(failures, fmt.Errorf("workspace %q: %w", p.WorkspaceRoot, err))
		}
	}
	return errors.Join(failures...)
}
