package appserver

import (
	"errors"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The same work owns one write lease across processes. Read-only inspection
// stays available to the lead while the executor runs. The operating-system
// lock lasts until the actual tool returns, including cancellation cleanup.
func (s *Server) acquireProjectWorkTool(member session.Session, call providers.ToolCall, base agent.ToolExecutor) (func(), error) {
	noop := func() {}
	if call.Name == "session" || call.Name == "notes" {
		return noop, nil
	}
	w, found, err := s.workForMember(member.ParentID, member.ID)
	if err != nil || !found {
		return noop, err
	}
	if w.Phase == "stopped" || w.Phase == "delivered" {
		return noop, errors.New("work is stopped or delivered; obtain a new assignment before continuing")
	}
	if member.ID == w.ExecutorID {
		if w.ExecutorInput == nil || w.ExecutorInput.Revision != w.Revision || (w.Phase != "executing" && w.Phase != "verifying") {
			return noop, errors.New("executor assignment is no longer current")
		}
		_, err := s.currentWorkAssignmentTurn(member.ID, w.ExecutorInput.ClientID)
		if err != nil {
			return noop, errors.New("current turn has not consumed this assignment")
		}
	}
	if metadata, ok := base.(agent.ToolMetadataProvider); ok {
		if m, found := metadata.ToolMetadata(call); found && (m.ReadOnly || m.Orchestrator) {
			return noop, nil
		}
	}
	if w.Phase == "accepted" || w.Phase == "verifying" {
		return noop, errors.New("submitted or accepted content is frozen; start a new technical phase before changing it")
	}

	if member.ID == w.LeadID {
		if w.LeadInput == nil {
			return noop, errors.New("technical lead has no current assignment")
		}
		_, err := s.currentWorkAssignmentTurn(member.ID, w.LeadInput.ClientID)
		if err != nil {
			return noop, errors.New("consume the current work brief before writing")
		}

		active, err := s.workMemberActive(w.ExecutorID)
		if err != nil {
			return noop, err
		}
		if active || w.Phase == "executing" || w.Phase == "verifying" {
			return noop, errors.New("executor owns this work's writes; wait for its actual exit before taking over")
		}
	}
	key := "project-work-write:" + w.ID
	// Non-Git projects cannot isolate workstreams, so they share one write lease.
	if w.Workspace == "shared" {
		key = "project-work-write:" + w.ProjectID
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, key)
	if err != nil {
		return noop, err
	}
	if !acquired {
		return noop, errors.New("another work participant is still writing; retry after it exits")
	}
	current, err := session.ReadProjectWork(s.rt.SessionDir, w.ID)
	if err != nil || current.Revision != w.Revision || current.Phase != w.Phase {
		_ = lease.Release()
		if err != nil {
			return noop, err
		}
		return noop, session.ErrProjectWorkChanged
	}
	return func() { _ = lease.Release() }, nil
}
