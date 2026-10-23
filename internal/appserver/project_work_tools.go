package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// The same work owns one write lease across processes. Read-only inspection
// stays available to the lead while the executor runs. The operating-system
// lock lasts until the actual tool returns, including cancellation cleanup.
func (s *ProjectCoordinationProvider) projectToolPolicy(member session.Session, call providers.ToolCall, readOnly bool) (ProjectToolPolicy, error) {
	noop := ProjectToolPolicy{}
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
	if readOnly {
		return noop, nil
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
	return ProjectToolPolicy{LeaseKey: key, WorkID: w.ID, Revision: w.Revision, Phase: w.Phase}, nil
}

// ProjectToolPolicy asks the host to hold an OS execution lease for this write.
// Revalidation occurs after acquisition; release follows the actual tool exit.
type ProjectToolPolicy struct {
	LeaseKey string `json:"lease_key,omitempty"`
	WorkID   string `json:"work_id,omitempty"`
	Revision int    `json:"revision,omitempty"`
	Phase    string `json:"phase,omitempty"`
}
type projectToolPolicyRequest struct {
	Member   session.Session    `json:"member"`
	Call     providers.ToolCall `json:"call"`
	ReadOnly bool               `json:"read_only"`
	Policy   ProjectToolPolicy  `json:"policy"`
}

func (s *ProjectCoordinationProvider) invokeToolPolicy(call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var input projectToolPolicyRequest
	if err := json.Unmarshal(call.Params, &input); err != nil {
		return nil, err
	}
	if call.Method == "tool-policy" {
		policy, err := s.projectToolPolicy(input.Member, input.Call, input.ReadOnly)
		if err != nil {
			return nil, err
		}
		return json.Marshal(policy)
	}
	current, err := s.store.ReadProjectWork(input.Policy.WorkID)
	if err != nil {
		return nil, err
	}
	if current.Revision != input.Policy.Revision || current.Phase != input.Policy.Phase {
		return nil, session.ErrProjectWorkChanged
	}
	return json.RawMessage(`{}`), nil
}
func (s *Server) acquireProjectWorkToolForBinding(member session.Session, call providers.ToolCall, base agent.ToolExecutor, host *pluginhost.Host, store *session.Store) (func(), error) {
	noop := func() {}
	readOnly := false
	if metadata, ok := base.(agent.ToolMetadataProvider); ok {
		if m, found := metadata.ToolMetadata(call); found {
			readOnly = m.ReadOnly || m.Orchestrator
		}
	}
	input := projectToolPolicyRequest{Member: member, Call: call, ReadOnly: readOnly}
	var policy ProjectToolPolicy
	if err := callProjectCoordination(context.Background(), host, "tool-policy", input, &policy); err != nil {
		return noop, err
	}
	if policy.LeaseKey == "" {
		return noop, nil
	}
	lease, acquired, err := store.TryAcquireThreadExecutionLease(s.rt.SessionDir, policy.LeaseKey)
	if err != nil {
		return noop, err
	}
	if !acquired {
		return noop, errors.New("another work participant is still writing; retry after it exits")
	}
	input.Policy = policy
	if err := callProjectCoordination(context.Background(), host, "tool-validate", input, nil); err != nil {
		_ = lease.Release()
		return noop, err
	}
	return func() { _ = lease.Release() }, nil
}
