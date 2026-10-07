package appserver

import (
	"errors"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Read-only and orchestration calls remain available during delegation.
// Mutating tool calls share a cross-process lease until the call returns.
// Background processes outlive that lease; participants coordinate their output
// paths and finish conflicting writers before handing over task files.
func (s *Server) acquireFusionTool(member session.Session, call providers.ToolCall, base agent.ToolExecutor) (func(), error) {
	noop := func() {}
	if call.Name == "fusion_delegate" {
		return noop, nil
	}
	if metadata, ok := base.(agent.ToolMetadataProvider); ok {
		if info, found := metadata.ToolMetadata(call); found && (info.ReadOnly || info.Orchestrator) {
			return noop, nil
		}
	}
	leadID := member.ID
	if member.Source == fusionSideSource {
		leadID = member.ParentID
	}
	tasks, err := session.ListFusionTasks(s.rt.SessionDir, leadID)
	if err != nil {
		return noop, err
	}
	var current *session.FusionTask
	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].State == session.FusionTaskQueued || tasks[i].State == session.FusionTaskRunning || tasks[i].State == session.FusionTaskAwaitingReview {
			current = &tasks[i]
			break
		}
	}
	if member.Source == fusionSideSource {
		// Remove this legacy admission once no development store has pending
		// pre-task Fusion receipts. Their original control fences still apply.
		legacy := current == nil && len(tasks) == 0
		if !legacy && (current == nil || current.SideID != member.ID || current.State == session.FusionTaskAwaitingReview) {
			return noop, errors.New("Sidekick has no active Fusion assignment; obtain a new brief or review correction")
		}
		if current != nil && current.ReadOnly {
			return noop, errors.New("this Fusion task is read-only")
		}
	} else if current != nil && (current.State == session.FusionTaskQueued || current.State == session.FusionTaskRunning) {
		return noop, errors.New("Sidekick owns this workspace's writes; inspect or wait, or stop and confirm idle before taking over")
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, "fusion-write:"+leadID)
	if err != nil {
		return noop, err
	}
	if !acquired {
		return noop, errors.New("a Fusion participant is still writing; wait for its actual exit")
	}
	if member.Source != fusionSideSource {
		sides, err := s.fusionSides(leadID)
		if err == nil {
			for _, side := range sides {
				var view managedSessionView
				view, err = s.managedSessionView(side)
				if err != nil || view.State == "running" {
					if err == nil {
						err = errors.New("Sidekick is still executing; wait for idle before writing")
					}
					break
				}
			}
		}
		if err != nil {
			_ = lease.Release()
			return noop, err
		}
	} else {
		_, _, err = s.fusionActor(leadID, member.ID)
		if err == nil && current != nil {
			latest, found, readErr := session.ReadFusionTask(s.rt.SessionDir, current.ID)
			err = readErr
			if err == nil && (!found || latest.ReadOnly || (latest.State != session.FusionTaskQueued && latest.State != session.FusionTaskRunning)) {
				err = errors.New("Fusion assignment changed before the write lease was acquired")
			}
		}
		if err != nil {
			_ = lease.Release()
			return noop, err
		}
	}
	return func() { _ = lease.Release() }, nil
}
