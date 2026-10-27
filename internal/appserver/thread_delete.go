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
	"github.com/blueberrycongee/wuu/internal/sidethread"
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
	if th := s.threadForState(binding, id); th != nil {
		th.mu.Lock()
		running := th.running
		th.mu.Unlock()
		if running {
			return s.writeResponse(req.ID, nil, errors.New("cannot delete a running thread"))
		}
		if threadHasActiveAgents(th) {
			return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread with active agents"))
		}
	}
	mutationLease, err := s.tryAcquireThreadMutationLease(binding, id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseThreadMutationLease(id, mutationLease)
	sideThreadLease, err := s.acquireSideThreadExecutionLease(binding, id)
	if errors.Is(err, errSideThreadExecutionBusy) {
		return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread while its side thread is running"))
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseSideThreadExecutionLease(id, sideThreadLease)

	stateDirs, err := s.threadArtifactStateDirs(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	active, err := s.threadHasDurableActiveAgentsWithState(binding, id, stateDirs...)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("inspect durable agents for thread %q: %w", id, err))
	}
	if active {
		return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread with active agents"))
	}

	services, err := binding.Services()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}

	// Execution ownership excludes model rewrites and child spawns. The
	// independent short lifecycle lease then waits for any in-flight group or
	// participant append-and-route write. Session deletion atomically removes
	// pending envelopes sourced from this thread before this lease is released.
	lifecycleLease, err := s.acquireThreadLifecycleWriteLease(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	admissionCtx, err := s.requireRequestAccess(req, MethodThreadDelete, nil)
	if err != nil {
		releaseThreadLifecycleWriteLease(id, lifecycleLease)
		return s.writeResponse(req.ID, nil, err)
	}
	// Stage side-chat history before deleting the main session.
	// The staged record remains readable and can be atomically restored if the
	// main transaction fails, avoiding either partial data loss or an orphaned
	// side conversation across process crashes.
	sideDelete, err := sidethread.NewStore(admissionCtx, binding.Documents).BeginDelete(id)
	if err != nil {
		releaseThreadLifecycleWriteLease(id, lifecycleLease)
		return s.writeResponse(req.ID, nil, fmt.Errorf("delete side thread for %q (stage): %w", id, err))
	}
	deleted, err := s.deleteThreadSession(binding, id, params.OnlyIfArchived)
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		var uncertain *persistence.CommitError
		if errors.As(err, &uncertain) || errors.Is(err, session.ErrOutcomeUnknown) {
			// The main row may already be gone. Keep the readable deletion
			// stage until an explicit retry can finish either storage outcome.
			releaseThreadLifecycleWriteLease(id, lifecycleLease)
			return s.writeResponse(req.ID, nil, fmt.Errorf("session deletion outcome is unknown for %q; retry thread/delete: %w", id, err))
		}
		rollbackErr := sideDelete.Rollback()
		releaseThreadLifecycleWriteLease(id, lifecycleLease)
		return s.writeResponse(req.ID, nil, errors.Join(err, rollbackErr))
	}
	releaseThreadLifecycleWriteLease(id, lifecycleLease)
	cleanupErr := sideDelete.Commit()
	if cleanupErr != nil {
		// Staged history remains readable until this commit succeeds. Continue
		// independent cleanup, then report every incomplete deletion together.
		cleanupErr = fmt.Errorf("side history cleanup: %w", cleanupErr)
	}

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

	if deleted.Source == projectSource {
		// Deleting the coordinator does not delete or release its sessions. Send
		// fresh grouping metadata so open sidebars can expose the surviving rows.
		members, err := binding.Sessions.List(0)
		if err != nil {
			providers.DebugLogf("list surviving project sessions of %q: %v", id, err)
		}
		for _, member := range members {
			if member.Source != projectSessionSource || member.ParentID != id {
				continue
			}
			thread, err := s.threadAfterMetadataUpdate(binding, member)
			if err == nil {
				err = s.notifyThreadUpdated(binding, thread)
			}
			if err != nil {
				providers.DebugLogf("refresh surviving project session %q: %v", member.ID, err)
			}
		}
	}

	// The document commit is irreversible. Surface an incomplete selected
	// artifact cleanup and allow an explicit retry even when the row is gone.
	_, artifactErr := services.Artifacts.DeleteThread(context.Background(), id)
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

	if cleanupErr != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("session deleted; cleanup for %q is incomplete, retry thread/delete: %w", id, cleanupErr))
	}
	return s.writeResponse(req.ID, ThreadDeleteResult{ThreadID: id}, nil)
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
