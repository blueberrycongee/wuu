package appserver

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type sessionCacheKey struct {
	store *session.Store
	id    string
}

// Cached projections belong to the authority that produced them. A request
// against a replacement authority must load its own projection.
func (s *Server) threadForState(binding *runtime.State, id string) *threadState {
	th := s.thread(id)
	if th == nil || th.state == nil || th.state.Sessions != binding.Sessions {
		return nil
	}
	return th
}

// Transfer one reference before dispatch, including a dispatch rejected during
// shutdown. The caller may close its request capture as soon as this returns.
func (s *Server) startBackgroundWithState(binding *runtime.State, work func(*runtime.State)) bool {
	held := binding.Retain()
	if held == nil {
		return false
	}
	if !s.startBackground(func() { defer held.Close(); work(held) }) {
		_ = held.Close()
		return false
	}
	return true
}

func (s *Server) reserveBackgroundWithState(binding *runtime.State, work func(*runtime.State)) (*backgroundLaunch, bool) {
	launch := &backgroundLaunch{decision: make(chan bool, 1)}
	if !s.startBackgroundWithState(binding, func(held *runtime.State) {
		if <-launch.decision {
			work(held)
		}
	}) {
		return nil, false
	}
	return launch, true
}

// A queued input is a new admission. Keep an admitted execution run pinned,
// but wait for old workers to finish before replacing an idle projection.
// The old thread's immutable State remains valid for its finishing callbacks.
func (s *Server) retainThreadAdmissionState(id string) (*runtime.State, *threadState, error) {
	current, err := s.rt.RetainState()
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*runtime.State, *threadState, error) { _ = current.Close(); return nil, nil, err }
	th := s.thread(id)
	if th == nil {
		return current, nil, nil
	}
	th.mu.Lock()
	if th.state.Sessions == current.Sessions {
		th.mu.Unlock()
		return current, th, nil
	}
	if s.activeExecutionRunID(id) != "" {
		held := th.state.Retain()
		th.mu.Unlock()
		_ = current.Close()
		if held == nil {
			return nil, nil, errors.Join(errRetryableTurnAdmission, errors.New("execution run state is closed"))
		}
		return held, th, nil
	}
	if threadAdmissionProjectionBusyLocked(th) {
		th.mu.Unlock()
		return fail(threadExecutionBusyError(id))
	}
	persistent := th.PersistHistory
	th.mu.Unlock()
	var next *threadState
	if persistent {
		next, err = s.loadPersistedThreadState(current, id, time.Now().UTC())
		if errors.Is(err, session.ErrSessionNotFound) {
			return current, nil, nil
		}
		if err != nil {
			return fail(err)
		}
	}
	// Publish a fresh projection under the cache and thread locks. Durable
	// history comes from the selected store; nonpersistent history and local
	// conversation controls must survive without copying execution ownership.
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return fail(errServerClosed)
	}
	if s.threads[id] != th {
		s.mu.Unlock()
		return fail(errors.Join(errRetryableTurnAdmission, fmt.Errorf("thread %q changed during admission", id)))
	}
	th.mu.Lock()
	if s.activeExecutionRunID(id) != "" || threadAdmissionProjectionBusyLocked(th) {
		th.mu.Unlock()
		s.mu.Unlock()
		return fail(threadExecutionBusyError(id))
	}
	if next == nil {
		next = copyEphemeralAdmissionProjectionLocked(th)
	}
	next.state = current.Retain()
	copyThreadAdmissionContextLocked(next, th)
	detached := detachThreadRuntimeLocked(th)
	th.maybeReleasePluginGenerationExecutionLeaseLocked()
	s.threads[id] = next
	th.mu.Unlock()
	s.mu.Unlock()
	s.releaseDetachedThreadRuntime(detached)
	_ = th.state.Close()
	return current, next, nil
}

func threadAdmissionProjectionBusyLocked(th *threadState) bool {
	return th.running || th.admissionReserved || th.executionLease != nil || th.runtimeSelectionMutation || th.nativeHistoryMutation || threadRuntimeHasOutstandingWork(th.ID, th.execRuntime)
}

func copyEphemeralAdmissionProjectionLocked(th *threadState) *threadState {
	next := &threadState{
		ID:               th.ID,
		Source:           th.Source,
		Owner:            th.Owner,
		Visibility:       th.Visibility,
		Instructions:     th.Instructions,
		ProjectID:        th.ProjectID,
		ProjectRole:      th.ProjectRole,
		ParentID:         th.ParentID,
		AgentPath:        th.AgentPath,
		historyHeadSeq:   th.historyHeadSeq,
		CreatedAt:        th.CreatedAt,
		UpdatedAt:        th.UpdatedAt,
		LastAccessedAt:   th.LastAccessedAt,
		Title:            th.Title,
		TitleRevision:    th.TitleRevision,
		ModelProvider:    th.ModelProvider,
		Model:            th.Model,
		ModelVariant:     th.ModelVariant,
		ModelEffort:      th.ModelEffort,
		Speed:            th.Speed,
		PermissionMode:   th.PermissionMode,
		ApproveForMe:     th.ApproveForMe,
		EngineID:         th.EngineID,
		EngineRef:        th.EngineRef,
		CWD:              th.CWD,
		WorkspaceKind:    th.WorkspaceKind,
		ForkedFromID:     th.ForkedFromID,
		ForkedFromTurnID: th.ForkedFromTurnID,
		ForkedFromItemID: th.ForkedFromItemID,
		WorktreePath:     th.WorktreePath,
		WorktreeBaseHEAD: th.WorktreeBaseHEAD,
		WorktreeBaseRepo: th.WorktreeBaseRepo,
		WorkspaceID:      th.WorkspaceID,
		PinnedAt:         th.PinnedAt,
		FolderID:         th.FolderID,
		ArchivedAt:       th.ArchivedAt,
		ArchiveReason:    th.ArchiveReason,
		PersistHistory:   th.PersistHistory,
		ReadOnly:         th.ReadOnly,
		Ephemeral:        th.Ephemeral,
		History:          cloneHistory(th.History), Turns: cloneTurns(th.Turns), ModelOptions: maps.Clone(th.ModelOptions),
		toolItems: make(map[string]string),
	}
	if th.SessionControl != nil {
		control := *th.SessionControl
		next.SessionControl = &control
	}
	return next
}

// These controls live in memory even for durable conversations. A state reload
// must not unfreeze an interrupted worker tree or lose pending steer context.
func copyThreadAdmissionContextLocked(next, th *threadState) {
	next.nativeSteerUnsupported = th.nativeSteerUnsupported
	next.activeSteerContextSet = th.activeSteerContextSet
	next.workerTreeFrozen = th.workerTreeFrozen
	next.pendingSteers = cloneHistory(th.pendingSteers)
	next.pendingSteerControls = maps.Clone(th.pendingSteerControls)
	next.activeSteerDocument = cloneActiveDocument(th.activeSteerDocument)
	next.frozenTreeContext = slices.Clone(th.frozenTreeContext)
	next.frozenTreeResultIDs = slices.Clone(th.frozenTreeResultIDs)
	for _, override := range th.steerDocumentOverrides {
		next.steerDocumentOverrides = append(next.steerDocumentOverrides, activeDocumentOverride{steerID: override.steerID, document: cloneActiveDocument(override.document)})
	}
	if th.PersistHistory {
		return
	}
	th.ephemeralDriver.mu.Lock()
	next.ephemeralDriver.binding, next.ephemeralDriver.bound = th.ephemeralDriver.binding, th.ephemeralDriver.bound
	next.ephemeralDriver.checkpoint, next.ephemeralDriver.saved = th.ephemeralDriver.checkpoint, th.ephemeralDriver.saved
	next.ephemeralDriver.checkpoint.State = slices.Clone(th.ephemeralDriver.checkpoint.State)
	th.ephemeralDriver.mu.Unlock()
}
