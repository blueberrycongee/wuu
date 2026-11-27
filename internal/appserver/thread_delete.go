package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// handleThreadDelete permanently removes a conversation. Unlike archive
// (which only hides the thread), delete is the storage-hygiene path: it
// removes the session row and chat history, the workspace-scoped session
// artifact directory (workers/threads/harness), and any fork
// worktree still bound to the thread. Only idle (not running) threads are
// eligible; archive-only requests also require durable archived state.
func (s *Server) handleThreadDelete(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params ThreadDeleteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	if _, err := s.requireRequestAccess(req, MethodThreadDelete, nil); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	lifecycle, err := s.acquireThreadLifecycleWriteLease(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseThreadLifecycleWriteLease(id, lifecycle)
	sessions, err := binding.Sessions.List(0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var children []string
	for _, row := range sessions {
		if row.Placement == session.PlacementSide && row.ParentID == id {
			children = append(children, row.ID)
		}
	}
	stateDirs := make(map[string][]string, len(children)+1)
	for _, target := range append([]string{id}, children...) {
		if th := s.threadForState(binding, target); th != nil {
			th.mu.Lock()
			busy := threadAdmissionProjectionBusyLocked(th)
			th.mu.Unlock()
			if busy {
				return s.writeResponse(req.ID, nil, errors.New("cannot delete a running thread"))
			}
			if threadHasActiveAgents(th) {
				return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread with active agents"))
			}
		}
		mutationLease, err := s.tryAcquireThreadMutationLease(binding, target)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		defer releaseThreadMutationLease(target, mutationLease)
		if target != id {
			lease, err := s.acquireThreadLifecycleWriteLease(target)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			defer releaseThreadLifecycleWriteLease(target, lease)
		}
		dirs, err := s.threadArtifactStateDirs(target)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		stateDirs[target] = dirs
		active, err := s.threadHasDurableActiveAgentsWithState(binding, target, dirs...)
		if err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("inspect durable agents for thread %q: %w", target, err))
		}
		if active {
			return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread with active agents"))
		}
	}
	services, err := binding.Services()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var deleted []session.Session
	if len(children) > 0 {
		deleted, err = binding.Sessions.DeleteWithSideChats(id, children, params.OnlyIfArchived)
	} else {
		var row session.Session
		row, err = s.deleteThreadSession(binding, id, params.OnlyIfArchived)
		row.ID = id
		deleted = []session.Session{row}
	}
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		var uncertain *persistence.CommitError
		if errors.As(err, &uncertain) || errors.Is(err, session.ErrOutcomeUnknown) {
			return s.writeResponse(req.ID, nil, fmt.Errorf("session deletion outcome is unknown for %q; retry thread/delete: %w", id, err))
		}
		return s.writeResponse(req.ID, nil, err)
	}

	var cleanupErr error
	for _, row := range deleted {
		cleanupErr = errors.Join(cleanupErr, s.cleanupDeletedThread(binding, services.Artifacts, row, stateDirs[row.ID]))
		_ = s.writeNotification(NotificationThreadDeleted, ThreadDeleteResult{ThreadID: row.ID})
	}
	if cleanupErr != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("session deleted; cleanup for %q is incomplete, retry thread/delete: %w", id, cleanupErr))
	}
	return s.writeResponse(req.ID, ThreadDeleteResult{ThreadID: id}, nil)
}

func (s *Server) cleanupDeletedThread(binding *runtime.State, artifacts *runtime.ArtifactClient, deleted session.Session, stateDirs []string) error {
	id := deleted.ID
	var cleanupErr error
	// Empty authoritative images are deletion tombstones: an explicit retry or
	// a stale legacy artifact directory must never reimport deleted child data.
	childStateErr := persistence.Update(context.Background(), binding.Documents, func(tx *persistence.Tx) error {
		for _, namespace := range []string{agentthread.DocumentNamespace, harness.DocumentNamespace, tools.BrowserTabDocumentNamespace} {
			if err := tx.Put(namespace, id, persistence.DocumentImage{Version: 1, Values: map[string]json.RawMessage{}}); err != nil {
				return err
			}
		}
		records, err := tx.Read(persistence.Query{Namespace: subagent.HistoryDocumentNamespace, Prefix: id + "/"})
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := tx.Put(record.Namespace, record.Key, persistence.DocumentImage{Version: 1, Values: map[string]json.RawMessage{}}); err != nil {
				return err
			}
		}
		return nil
	})
	if childStateErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("child state cleanup: %w", childStateErr))
	}

	// Code-mode state outlives cached thread runtimes, but never a successful
	// permanent delete. This also covers owners already evicted from s.threads.
	if s.rt.CodeMode != nil {
		s.rt.CodeMode.ForgetOwner(id)
	}

	// Remove the in-memory owner and stop its subscriptions before deleting
	// runtime artifacts. Otherwise an idle thread leaves the AgentControl and
	// app-server forwarding goroutines alive after its durable state is gone.
	s.mu.Lock()
	removed := s.threads[id]
	delete(s.threads, id)
	s.mu.Unlock()
	s.releaseThreadRuntime(removed)
	if removed != nil && removed.state != nil {
		_ = removed.state.Close()
	}

	if err := persistence.Update(context.Background(), binding.Documents, func(tx *persistence.Tx) error {
		return tx.Delete(restartRecoveryNamespace, id)
	}); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restart recovery cleanup: %w", err))
	}

	// The document commit is irreversible. Surface an incomplete selected
	// artifact cleanup and allow an explicit retry even when the row is gone.
	_, artifactErr := artifacts.DeleteThread(context.Background(), id)
	if artifactErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("artifact cleanup: %w", artifactErr))
	}

	// Local execution projections, input copies and clean worktrees remain host-owned.
	// Managed payloads and index data belong to their selected providers.
	for _, stateDir := range stateDirs {
		worktreeRoot := statepath.WorktreeRoot(stateDir)
		parentRepo := firstNonEmpty(deleted.WorktreeBaseRepo, deleted.CWD, s.rt.RootDir)
		manager, managerErr := worktree.NewManager(parentRepo, worktreeRoot)
		if info, bound := deleted.WorktreeInfo(); bound && managerErr == nil {
			// Never use a stale or foreign metadata path to remove arbitrary data.
			if pathWithinRoot(info.Path, worktreeRoot) {
				_, _ = manager.CleanupIfClean(&worktree.Worktree{Path: info.Path, SessionID: id, HEAD: info.BaseHEAD, BaseRepo: info.BaseRepo})
			}
		}
		if managerErr == nil {
			// Worker isolation worktrees share their globally unique owner's ID.
			// Preserve dirty checkouts for review even after conversation deletion.
			kept, cleanupErr := manager.CleanupSessionIfClean(id)
			if cleanupErr != nil {
				providers.DebugLogf("cleanup deleted thread worktrees %q: %v", id, cleanupErr)
			} else if kept {
				providers.DebugLogf("preserved dirty deleted thread worktrees %q", id)
			}
		}
		artifactDir := statepath.SessionArtifactDir(stateDir, id)
		for _, name := range []string{"workers", "threads", "harness", "input-images", "input-attachments", "execution-environment.json"} {
			_ = os.RemoveAll(filepath.Join(artifactDir, name))
		}
		_ = os.Remove(statepath.ThreadBrowserTabsPath(stateDir, id))
		_ = os.Remove(artifactDir)
	}

	return cleanupErr
}

func (s *Server) deleteThreadSession(binding *runtime.State, threadID string, onlyIfArchived bool) (session.Session, error) {
	if s != nil && s.deleteSessionForTest != nil {
		return s.deleteSessionForTest(threadID)
	}
	if onlyIfArchived {
		return binding.Sessions.DeleteArchived(threadID)
	}
	return binding.Sessions.Delete(threadID)
}

// threadArtifactStateDirs finds actual artifact locations rather than inferring
// them from CWD: a runtime can execute a thread in a different directory or
// resume it from another workspace while retaining the global session identity.
// The caller holds the global mutation lease while inspecting and deleting them.
func (s *Server) threadArtifactStateDirs(threadID string) ([]string, error) {
	own, err := s.workspaceStateDir()
	if err != nil {
		return nil, err
	}
	return statepath.SessionArtifactStateDirs(s.rt.WuuHome, s.rt.SessionDir, own, threadID)
}

// threadHasDurableActiveAgents checks the cross-process task state while the
// caller owns the parent thread's mutation lease. The thread index covers
// direct and nested workers; the harness task and queue indexes cover queued
// work even if one of the redundant lifecycle writes was interrupted.
func (s *Server) threadHasDurableActiveAgents(threadID string, stateDirs ...string) (bool, error) {
	binding, err := s.rt.RetainState()
	if err != nil {
		return false, err
	}
	defer binding.Close()
	return s.threadHasDurableActiveAgentsWithState(binding, threadID, stateDirs...)
}

func (s *Server) threadHasDurableActiveAgentsWithState(binding *runtime.State, threadID string, stateDirs ...string) (bool, error) {
	if len(stateDirs) == 0 {
		var err error
		stateDirs, err = s.threadArtifactStateDirs(threadID)
		if err != nil {
			return false, err
		}
	}
	for _, stateDir := range stateDirs {
		active, err := threadHasDurableActiveAgentsInStateDir(binding.Documents, threadID, stateDir, stateDirs...)
		if err != nil || active {
			return active, err
		}
	}
	return false, nil
}

func threadHasDurableActiveAgentsInStateDir(documents persistence.Documents, threadID, stateDir string, legacyStateDirs ...string) (bool, error) {
	artifactDir := statepath.SessionArtifactDir(stateDir, threadID)
	workerIDs := make(map[string]struct{})

	threadDirs, harnessDirs := []string{}, []string{}
	for _, root := range legacyStateDirs {
		threadDirs = append(threadDirs, filepath.Join(statepath.SessionArtifactDir(root, threadID), "threads"))
		harnessDirs = append(harnessDirs, filepath.Join(statepath.SessionArtifactDir(root, threadID), "harness"))
	}
	threads, err := agentthread.NewDocumentStore(documents, threadID, threadDirs...).ListThreads()
	if err != nil {
		return false, err
	}
	for _, meta := range threads {
		if meta.ID == threadID || meta.Source.Kind != agentthread.SourceThreadSpawn {
			continue
		}
		workerIDs[meta.ID] = struct{}{}
	}

	harnessDir := filepath.Join(artifactDir, "harness")
	terminalPending, err := agentcontrol.WorkerTerminalFinalizationsPending(harnessDir)
	if err != nil {
		return false, fmt.Errorf("inspect terminal agent ownership: %w", err)
	}
	if terminalPending {
		return true, nil
	}
	harnessStore := harness.NewDocumentStore(documents, threadID, harnessDirs...)
	tasks, err := harnessStore.ListTasks()
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.ID == threadID {
			continue
		}
		workerIDs[task.ID] = struct{}{}
	}

	queue, err := harnessStore.ListQueueItems()
	if err != nil {
		return false, err
	}
	for _, item := range queue {
		if item.Kind == "agent_spawn" {
			return true, nil
		}
	}
	ids := make([]string, 0, len(workerIDs))
	for workerID := range workerIDs {
		if workerID = strings.TrimSpace(workerID); workerID != "" {
			ids = append(ids, workerID)
		}
	}
	sort.Strings(ids)
	for _, workerID := range ids {
		active, err := agentcontrol.WorkerExecutionActive(harnessDir, workerID)
		if err != nil {
			return false, fmt.Errorf("inspect worker %q execution lease: %w", workerID, err)
		}
		if active {
			return true, nil
		}
	}
	// Thread and harness status files are projections, not ownership. A process
	// can die after writing pending/running and before it either launches or
	// reconciles the worker. Once the parent mutation lease is held, only a
	// durable queue item, terminal intent, or live OS execution lease may block
	// deletion; stale projected status alone must not make a thread immortal.
	return false, nil
}

func threadHasActiveAgents(th *threadState) bool {
	if th == nil {
		return false
	}
	th.mu.Lock()
	threadRuntime := th.execRuntime
	th.mu.Unlock()
	if threadRuntime == nil || threadRuntime.AgentControl == nil {
		return false
	}
	for _, snapshot := range threadRuntime.AgentControl.List() {
		if isRunningSubAgentStatus(snapshot.Status) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}
