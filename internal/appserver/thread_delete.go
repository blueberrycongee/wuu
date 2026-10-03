package appserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// handleThreadDelete permanently removes a conversation. Unlike archive
// (which only hides the thread), delete is the storage-hygiene path: it
// removes the session row and chat history, the workspace-scoped session
// artifact directory (workers/threads/harness), and any fork
// worktree still bound to the thread. Only idle (not running) threads are
// eligible; archive-only requests also require durable archived state.
func (s *Server) handleThreadDelete(req Request) error {
	var params ThreadDeleteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	if th := s.thread(id); th != nil {
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
	mutationLease, err := s.tryAcquireThreadMutationLease(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseThreadMutationLease(id, mutationLease)
	sideThreadLease, err := s.acquireSideThreadExecutionLease(id)
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
	active, err := s.threadHasDurableActiveAgents(id, stateDirs...)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("inspect durable agents for thread %q: %w", id, err))
	}
	if active {
		return s.writeResponse(req.ID, nil, errors.New("cannot delete a thread with active agents"))
	}

	// Execution ownership excludes model rewrites and child spawns. The
	// independent short lifecycle lease then waits for any in-flight group or
	// participant append-and-route write. Session deletion atomically removes
	// pending envelopes sourced from this thread before this lease is released.
	lifecycleLease, err := s.acquireThreadLifecycleWriteLease(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// Stage the independent side-chat file before deleting the main SQLite row.
	// The staged record remains readable and can be atomically restored if the
	// main transaction fails, avoiding either partial data loss or an orphaned
	// side conversation across process crashes.
	sideDelete, err := s.sideThreadStore.BeginDelete(id)
	if err != nil {
		releaseThreadLifecycleWriteLease(id, lifecycleLease)
		return s.writeResponse(req.ID, nil, fmt.Errorf("delete side thread for %q (stage): %w", id, err))
	}
	deleted, err := s.deleteThreadSession(id, params.OnlyIfArchived)
	if err != nil {
		rollbackErr := sideDelete.Rollback()
		releaseThreadLifecycleWriteLease(id, lifecycleLease)
		return s.writeResponse(req.ID, nil, errors.Join(err, rollbackErr))
	}
	releaseThreadLifecycleWriteLease(id, lifecycleLease)
	if err := sideDelete.Commit(); err != nil {
		// The main row is already durably gone. A staged tombstone is harmless
		// and remains eligible for a later idempotent cleanup attempt.
		providers.DebugLogf("commit side thread delete for %q: %v", id, err)
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

	if deleted.Source == projectSource {
		// Deleting the coordinator does not delete or release its sessions. Send
		// fresh grouping metadata so open sidebars can expose the surviving rows.
		members, err := session.List(s.rt.SessionDir, 0)
		if err != nil {
			providers.DebugLogf("list surviving project sessions of %q: %v", id, err)
		}
		for _, member := range members {
			if member.Source != projectSessionSource || member.ParentID != id {
				continue
			}
			thread, err := s.threadAfterMetadataUpdate(member)
			if err == nil {
				err = s.notifyThreadUpdated(thread)
			}
			if err != nil {
				providers.DebugLogf("refresh surviving project session %q: %v", member.ID, err)
			}
		}
	}

	// Everything past this point is best-effort cleanup: the session row
	// (and its cascaded history) is already gone, so a failing worktree or
	// artifact removal must not resurrect the thread — it only leaves disk
	// garbage that a later cleanup pass can reclaim.
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
		_ = os.RemoveAll(statepath.SessionArtifactDir(stateDir, id))
	}

	return s.writeResponse(req.ID, ThreadDeleteResult{ThreadID: id}, nil)
}

func (s *Server) deleteThreadSession(threadID string, onlyIfArchived bool) (session.Session, error) {
	if s != nil && s.deleteSessionForTest != nil {
		return s.deleteSessionForTest(threadID)
	}
	if onlyIfArchived {
		return session.DeleteArchived(s.rt.SessionDir, threadID)
	}
	return session.Delete(s.rt.SessionDir, threadID)
}

// threadArtifactStateDirs finds actual artifact locations rather than inferring
// them from CWD: a runtime can execute a thread in a different directory or
// resume it from another workspace while retaining the global session identity.
// The caller holds the global mutation lease while inspecting and deleting them.
func (s *Server) threadArtifactStateDirs(threadID string) ([]string, error) {
	if threadID == "." || threadID == ".." || filepath.Base(threadID) != threadID {
		return nil, errors.New("invalid thread id for artifact lookup")
	}
	own, err := s.workspaceStateDir()
	if err != nil {
		return nil, err
	}
	stateDirs := []string{own}
	home := strings.TrimSpace(s.rt.WuuHome)
	if home == "" {
		home, err = statepath.Home("")
		if err != nil {
			return nil, err
		}
	}
	// Custom stores may reuse IDs from the global store. Their delete request
	// must never inspect or reclaim another store's workspace artifacts.
	if filepath.Clean(s.rt.SessionDir) != filepath.Clean(statepath.SessionsDir(home)) {
		return stateDirs, nil
	}
	workspaceRoot := filepath.Join(home, "workspaces")
	rootInfo, err := os.Lstat(workspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return stateDirs, nil
	}
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("workspace artifact root is not a directory: %s", workspaceRoot)
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("workspace artifact path is a symbolic link: %s", filepath.Join(workspaceRoot, entry.Name()))
		}
		if !entry.IsDir() {
			continue
		}
		stateDir := filepath.Join(workspaceRoot, entry.Name())
		matched := false
		for _, name := range []string{"sessions", "worktrees"} {
			root := filepath.Join(stateDir, name)
			for _, path := range []string{root, filepath.Join(root, threadID)} {
				info, err := os.Lstat(path)
				if errors.Is(err, os.ErrNotExist) {
					break
				}
				if err != nil {
					return nil, err
				}
				// Lstat refuses links at both boundaries before reading contents.
				if !info.IsDir() {
					return nil, fmt.Errorf("thread artifact path is not a directory: %s", path)
				}
				if path != root {
					matched = true
				}
			}
		}
		if matched && filepath.Clean(stateDir) != filepath.Clean(own) {
			stateDirs = append(stateDirs, stateDir)
		}
	}
	return stateDirs, nil
}

// threadHasDurableActiveAgents checks the cross-process task state while the
// caller owns the parent thread's mutation lease. The thread index covers
// direct and nested workers; the harness task and queue indexes cover queued
// work even if one of the redundant lifecycle writes was interrupted.
func (s *Server) threadHasDurableActiveAgents(threadID string, stateDirs ...string) (bool, error) {
	if len(stateDirs) == 0 {
		var err error
		stateDirs, err = s.threadArtifactStateDirs(threadID)
		if err != nil {
			return false, err
		}
	}
	for _, stateDir := range stateDirs {
		active, err := threadHasDurableActiveAgentsInStateDir(threadID, stateDir)
		if err != nil || active {
			return active, err
		}
	}
	return false, nil
}

func threadHasDurableActiveAgentsInStateDir(threadID, stateDir string) (bool, error) {
	artifactDir := statepath.SessionArtifactDir(stateDir, threadID)
	workerIDs := make(map[string]struct{})

	threads, err := agentthread.NewStore(filepath.Join(artifactDir, "threads")).ListThreads()
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
	harnessStore := harness.NewStore(harnessDir)
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
