package appserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Intent mutation is deliberately separate from runtime mutation. It never
// takes over an execution lease, changes permissions, or detaches resources.
func (s *Server) beginNextStepModelSelection(threadID string) (*threadState, func(), error) {
	binding, err := s.rt.RetainState()
	if err != nil {
		return nil, func() {}, err
	}
	th, err := s.ensureThreadLoaded(binding, strings.TrimSpace(threadID))
	if err != nil {
		binding.Close()
		return nil, func() {}, err
	}
	th.mu.Lock()
	if !th.running {
		th.mu.Unlock()
		binding.Close()
		return s.beginThreadRuntimeSelectionMutation(threadID)
	}
	if th.runtimeSelectionMutation || th.admissionReserved {
		th.mu.Unlock()
		binding.Close()
		return nil, func() {}, fmt.Errorf("%w while thread %q is changing selection", errThreadRuntimeSelectionBusy, threadID)
	}
	if th.execRuntime == nil || th.execRuntime.StreamRunner == nil || th.execRuntime.StreamRunner.ResolveModelStep == nil || !th.PersistHistory {
		th.mu.Unlock()
		binding.Close()
		return nil, func() {}, errors.New("active runtime does not support durable next-step model selection")
	}
	th.runtimeSelectionMutation = true
	th.mu.Unlock()
	return th, func() { th.mu.Lock(); th.runtimeSelectionMutation = false; th.mu.Unlock(); binding.Close() }, nil
}

func (s *Server) commitNextStepModelSelection(th *threadState, selected session.RuntimeSelection) (session.Session, error) {
	th.mu.Lock()
	current := th.execRuntime
	running := th.running
	th.mu.Unlock()
	if current == nil {
		return session.Session{}, errors.New("prepare a conversation runtime before selecting its next-step model")
	}
	baseline, found, err := th.state.Sessions.Find(th.ID)
	if err != nil {
		return session.Session{}, err
	}
	if !found {
		return session.Session{}, session.ErrSessionNotFound
	}
	if agentengine.NormalizeEngineID(baseline.EngineID) != current.EngineID {
		return session.Session{}, session.ErrModelSelectionChanged
	}
	if err := current.PrepareNextStepModel(context.Background(), selected); err != nil {
		return session.Session{}, err
	}
	committed, err := th.state.Sessions.SetNextStepModelSelection(th.ID, session.NextStepModelSelection{
		ExpectedRevision: baseline.ModelSelectionRevision, ExpectedEngineID: baseline.EngineID, Selection: selected,
	})
	if err != nil {
		return session.Session{}, err
	}
	if committed.ModelSelectionRevision != baseline.ModelSelectionRevision+1 || committed.ModelSelectionApplyAt != "next_step" || committed.EngineID != baseline.EngineID || committed.Provider != selected.Provider || committed.Model != selected.Model || committed.Variant != selected.Variant || committed.Effort != selected.Effort || committed.Speed != selected.Speed || !maps.Equal(committed.ModelOptions, selected.ModelOptions) || committed.PermissionMode != baseline.PermissionMode || committed.ApproveForMe != baseline.ApproveForMe {
		// The new operation may have committed even when its acknowledgement violates
		// the contract. Never roll it back over a concurrent writer or retry blindly.
		return session.Session{}, errors.Join(session.ErrOutcomeUnknown, errors.New("selected session provider did not acknowledge a durable next-step model intent"))
	}
	th.mu.Lock()
	applyThreadRuntimeSelection(th, selected)
	th.stepModelSelection = th.running
	// An idle runtime must be rebuilt on its next admission; active resources
	// remain alive until their existing finalizers and workers have completed.
	if !running {
		th.pendingRuntimeReset = true
	}
	snapshot := th.snapshotLocked()
	th.mu.Unlock()
	return committed, s.notifyThreadUpdated(th.state, snapshot)
}
