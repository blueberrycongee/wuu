package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const restartRecoveryNamespace = "wuu.restart_recovery.v1"
const restartRecoveryBootNamespace = "wuu.restart_recovery_boot.v1"

// The boot boundary is shared by workspace app-servers. Opening another
// workspace or replacing a renderer must not revoke this boot's user choices.
type restartRecoveryRecord struct {
	ThreadID  string    `json:"thread_id"`
	TurnID    string    `json:"turn_id"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	Dismissed bool      `json:"dismissed,omitempty"`
	AllowedAt time.Time `json:"allowed_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RestartRecoveryItem struct {
	Thread Thread `json:"thread"`
	TurnID string `json:"turn_id"`
	Reason string `json:"reason"`
}

func (s *Server) initializeRestartRecovery(binding *runtime.State, first bool) error {
	return persistence.Update(context.Background(), binding.Documents, func(tx *persistence.Tx) error {
		found, err := tx.Get(restartRecoveryBootNamespace, "boot", &s.recoveryBootAt)
		if err != nil {
			return err
		}
		if first || !found {
			s.recoveryBootAt = time.Now().UTC()
			return tx.Put(restartRecoveryBootNamespace, "boot", s.recoveryBootAt)
		}
		return nil
	})
}

func (s *Server) recordRecoveryTurn(th *threadState, turnID, state string) error {
	if !th.PersistHistory || th.Visibility == pluginhost.SessionVisibilityPlugin || th.ParentID != "" {
		return nil
	}
	return persistence.Update(context.Background(), th.state.Documents, func(tx *persistence.Tx) error {
		var rec restartRecoveryRecord
		_, err := tx.Get(restartRecoveryNamespace, th.ID, &rec)
		if err != nil {
			return err
		}
		if state != "running" && rec.TurnID != turnID {
			return nil
		}
		rec.ThreadID, rec.TurnID, rec.State = th.ID, turnID, state
		rec.UpdatedAt = time.Now().UTC()
		if state == "running" {
			rec.Reason, rec.Dismissed = "", false
			rec.AllowedAt = rec.UpdatedAt
		} else if state == "pending" {
			rec.Reason = "interrupted"
		}
		return tx.Put(restartRecoveryNamespace, th.ID, rec)
	})
}

// Completion receipts stay unconsumed while paused. Admitting new work (including
// explicit Continue) releases the gate; reading history and dismissing do not.
func (s *Server) pauseCompletionAfterRestart(th *threadState) (bool, error) {
	return s.holdRestartRecovery(th, "background_result")
}

func (s *Server) holdRestartRecovery(th *threadState, reason string) (bool, error) {
	if !th.PersistHistory || th.Visibility == pluginhost.SessionVisibilityPlugin || th.ParentID != "" {
		return false, nil
	}
	// A conversation created during this boot has no pre-restart work.
	meta, found, err := th.state.Sessions.Find(th.ID)
	if err != nil {
		return true, err
	}
	if found && !meta.CreatedAt.Before(s.recoveryBootAt) {
		return false, nil
	}
	paused, changed := false, false
	err = persistence.Update(context.Background(), th.state.Documents, func(tx *persistence.Tx) error {
		var rec restartRecoveryRecord
		_, err := tx.Get(restartRecoveryNamespace, th.ID, &rec)
		if err != nil {
			return err
		}
		if !rec.AllowedAt.Before(s.recoveryBootAt) {
			return nil
		}
		paused = true
		if rec.State == "pending" {
			return nil
		}
		rec.ThreadID, rec.State, rec.Reason = th.ID, "pending", reason
		rec.UpdatedAt = time.Now().UTC()
		changed = true
		return tx.Put(restartRecoveryNamespace, th.ID, rec)
	})
	if changed && err == nil {
		_ = s.writeNotification("thread/recoveryChanged", struct{}{})
	}
	return paused || err != nil, err
}

func (s *Server) handleRestartRecoveryList(req Request) error {
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	// Process completion obligations survive independently of loaded threads.
	// Discover them before the user opens any of those conversations.
	if s.rt.ProcessManager != nil {
		pending, err := s.rt.ProcessManager.PendingCompletions()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		for _, p := range pending {
			if p.OwnerKind != process.OwnerMainAgent || !p.StartedAt.Before(s.recoveryBootAt) {
				continue
			}
			meta, found, err := binding.Sessions.Find(p.OwnerID)
			if !found && err == nil {
				continue
			}
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if meta.ArchivedAt != nil || meta.Visibility == pluginhost.SessionVisibilityPlugin || meta.ParentID != "" {
				continue
			}
			// An answer may be committed before its delivery receipt. Do not
			// offer a completed obligation as unfinished work.
			loaded, err := s.loadPersistedThreadSnapshot(binding, p.OwnerID)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if processCompletionMarkerAnswered(loaded.history, p.ID) {
				continue
			}
			th := &threadState{ID: p.OwnerID, PersistHistory: true, state: binding}
			if _, err := s.pauseCompletionAfterRestart(th); err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
		}
	}
	// Worker results can be pending even when their parent is not loaded.
	// Inspect the durable delivery ledger without starting worker runtimes.
	sessions, err := binding.Sessions.List(0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	for _, meta := range sessions {
		if !meta.CreatedAt.Before(s.recoveryBootAt) || meta.ArchivedAt != nil || meta.Visibility == pluginhost.SessionVisibilityPlugin || meta.ParentID != "" || projectExecutionDisabled(meta.Source) {
			continue
		}
		store, err := s.agentThreadStore(binding, meta.ID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		stateDirs, err := s.threadArtifactStateDirs(meta.ID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		artifactDirs := make([]string, 0, len(stateDirs))
		for _, dir := range stateDirs {
			artifactDirs = append(artifactDirs, statepath.SessionArtifactDir(dir, meta.ID))
		}
		pending, err := agentcontrol.ReadPendingAgentCompletions(binding.Documents, meta.ID, store, artifactDirs)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		workers, err := store.ListThreads()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		interrupted := false
		for _, worker := range workers {
			if worker.ID != meta.ID && worker.Source.Kind != agentthread.SourceRoot && worker.UpdatedAt.Before(s.recoveryBootAt) && (worker.Status == agentthread.StatusRunning || worker.Status == agentthread.StatusPending) {
				interrupted = true
			}
		}
		harnessDirs := make([]string, 0, len(artifactDirs))
		for _, dir := range artifactDirs {
			harnessDirs = append(harnessDirs, filepath.Join(dir, "harness"))
		}
		queued, err := harness.NewDocumentStore(binding.Documents, meta.ID, harnessDirs...).ListQueueItems()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		for _, item := range queued {
			if item.Kind == "agent_spawn" && item.CreatedAt.Before(s.recoveryBootAt) {
				interrupted = true
			}
		}
		if len(pending) == 0 && !interrupted {
			continue
		}
		loaded, err := s.loadPersistedThreadSnapshot(binding, meta.ID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		reason := ""
		if interrupted {
			reason = "interrupted"
		}
		for _, result := range pending {
			if !agentCompletionMarkerAnswered(loaded.history, result.ResultID) {
				reason = "background_result"
				break
			}
		}
		if reason != "" {
			th := &threadState{ID: meta.ID, PersistHistory: true, state: binding}
			if _, err := s.holdRestartRecovery(th, reason); err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
		}
	}
	items := []RestartRecoveryItem{}
	query := persistence.Query{Namespace: restartRecoveryNamespace, Limit: 256}
	for {
		page, err := binding.Documents.Read(context.Background(), query)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		for _, doc := range page.Documents {
			var rec restartRecoveryRecord
			if err := json.Unmarshal(doc.Value, &rec); err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if rec.Dismissed || (rec.State != "pending" && !(rec.State == "running" && rec.UpdatedAt.Before(s.recoveryBootAt))) {
				continue
			}
			active, err := session.ThreadExecutionActive(s.rt.SessionDir, rec.ThreadID)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if active {
				continue
			}
			meta, found, err := binding.Sessions.Find(rec.ThreadID)
			if !found && err == nil {
				continue
			}
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if meta.ArchivedAt != nil || meta.Visibility == pluginhost.SessionVisibilityPlugin || meta.ParentID != "" || projectExecutionDisabled(meta.Source) {
				continue
			}
			loaded, err := s.loadPersistedThreadSnapshot(binding, rec.ThreadID)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			turns := turnsFromPersistedHistory(rec.ThreadID, loaded.displayHistory, time.Now().UTC(), s.participantResolver(binding))
			if len(turns) == 0 {
				continue
			}
			last := turns[len(turns)-1]
			if rec.State == "running" && last.Status == TurnStatusCompleted {
				continue
			}
			entry, cancel := s.threadEntryBuilder(binding)
			thread := entry(relocatedSessionMetadata(meta, s.registeredWorkspaces())).thread
			cancel()
			thread.Turns = []Turn{last}
			reason := rec.Reason
			if reason == "" {
				reason = "interrupted"
			}
			items = append(items, RestartRecoveryItem{Thread: thread, TurnID: last.ID, Reason: reason})
		}
		if page.NextCursor == "" {
			break
		}
		query.Cursor, query.AtRevision = page.NextCursor, &page.Revision
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Thread.UpdatedAt.After(items[j].Thread.UpdatedAt) })
	return s.writeResponse(req.ID, struct {
		Items []RestartRecoveryItem `json:"items"`
	}{items}, nil)
}

func (s *Server) handleRestartRecoveryDismiss(req Request) error {
	var params struct {
		ThreadIDs []string `json:"thread_ids"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	err = persistence.Update(context.Background(), binding.Documents, func(tx *persistence.Tx) error {
		for _, id := range params.ThreadIDs {
			var rec restartRecoveryRecord
			found, err := tx.Get(restartRecoveryNamespace, id, &rec)
			if err != nil {
				return err
			}
			if !found || !rec.AllowedAt.Before(s.recoveryBootAt) || (rec.State != "running" && rec.State != "pending") {
				continue
			}
			rec.Dismissed = true
			if err := tx.Put(restartRecoveryNamespace, id, rec); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		_ = s.writeNotification("thread/recoveryChanged", struct{}{})
	}
	return s.writeResponse(req.ID, struct{}{}, err)
}
