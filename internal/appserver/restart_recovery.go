package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const restartRecoveryNamespace = "wuu.restart_recovery.v1"
const restartRecoveryBootNamespace = "wuu.restart_recovery_boot.v1"

// The boot boundary is shared by workspace app-servers. Opening another
// workspace or replacing a renderer must not revoke this boot's user choices.
type restartRecoveryRecord struct {
	ThreadID       string    `json:"thread_id"`
	TurnID         string    `json:"turn_id"`
	State          string    `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	Dismissed      bool      `json:"dismissed,omitempty"`
	UnexpectedExit bool      `json:"unexpected_exit,omitempty"`
	AllowedAt      time.Time `json:"allowed_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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
			s.previousRecoveryBootAt = s.recoveryBootAt
			if err := tx.Put(restartRecoveryBootNamespace, "previous", s.previousRecoveryBootAt); err != nil {
				return err
			}
			s.recoveryBootAt = time.Now().UTC()
			return tx.Put(restartRecoveryBootNamespace, "boot", s.recoveryBootAt)
		}
		_, err = tx.Get(restartRecoveryBootNamespace, "previous", &s.previousRecoveryBootAt)
		return err
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
			rec.UnexpectedExit = false
			rec.AllowedAt = rec.UpdatedAt
		} else if state == "pending" {
			rec.Reason = "interrupted"
			rec.UnexpectedExit = true
		} else {
			rec.UnexpectedExit = false
		}
		return tx.Put(restartRecoveryNamespace, th.ID, rec)
	})
}

// Keep historical completions paused until the user starts new work, without
// presenting those old results as interrupted conversations.
func (s *Server) pauseCompletionAfterRestart(th *threadState) (bool, error) {
	if !th.PersistHistory || th.Visibility == pluginhost.SessionVisibilityPlugin || th.ParentID != "" {
		return false, nil
	}
	meta, found, err := th.state.Sessions.Find(th.ID)
	if err != nil {
		return true, err
	}
	if found && !meta.CreatedAt.Before(s.recoveryBootAt) {
		return false, nil
	}
	view, err := th.state.Documents.Read(context.Background(), persistence.Query{Namespace: restartRecoveryNamespace, Keys: []string{th.ID}})
	if err != nil {
		return true, err
	}
	if len(view.Documents) == 0 {
		return true, nil
	}
	var rec restartRecoveryRecord
	if err := json.Unmarshal(view.Documents[0].Value, &rec); err != nil {
		return true, err
	}
	return rec.AllowedAt.Before(s.recoveryBootAt), nil
}

func (s *Server) handleRestartRecoveryList(ctx context.Context, req Request) error {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.sessionOperationCtx, cancel)
	defer func() { stop(); cancel() }()
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	var candidates []restartRecoveryRecord
	err = persistence.Update(ctx, binding.Documents, func(tx *persistence.Tx) error {
		candidates = nil
		query := persistence.Query{Namespace: restartRecoveryNamespace, Limit: 256}
		for {
			page, err := tx.ReadView(query)
			if err != nil {
				return err
			}
			for _, doc := range page.Documents {
				var rec restartRecoveryRecord
				if err := json.Unmarshal(doc.Value, &rec); err != nil {
					return err
				}
				if rec.Dismissed || s.previousRecoveryBootAt.IsZero() || rec.AllowedAt.Before(s.previousRecoveryBootAt) || !rec.AllowedAt.Before(s.recoveryBootAt) ||
					(rec.State != "running" && !(rec.State == "pending" && rec.UnexpectedExit && rec.Reason == "interrupted")) {
					continue
				}
				candidates = append(candidates, rec)
			}
			if page.NextCursor == "" {
				return nil
			}
			query.Cursor, query.AtRevision = page.NextCursor, &page.Revision
		}
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	items := []RestartRecoveryItem{}
	entry, cancelEntry := s.threadEntryBuilder(binding)
	defer cancelEntry()
	for _, rec := range candidates {
		active, err := session.ThreadExecutionActive(s.rt.SessionDir, rec.ThreadID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if active {
			continue
		}
		meta, found, err := binding.Sessions.Find(rec.ThreadID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if !found || !restartRecoverySessionEligible(meta) {
			continue
		}
		history, err := loadPersistedMessages(binding.Sessions.WithContext(ctx), rec.ThreadID, true)
		if err != nil {
			if errors.Is(err, session.ErrSessionNotFound) {
				continue
			}
			return s.writeResponse(req.ID, nil, err)
		}
		turns := turnsFromPersistedHistory(rec.ThreadID, history, time.Now().UTC(), s.participantResolver(binding))
		if len(turns) == 0 {
			continue
		}
		last := turns[len(turns)-1]
		// A terminal can be persisted before the recovery record is updated.
		// A replayed input may retain its existing display turn ID.
		settled := false
		if rec.State == "running" {
			for _, message := range history {
				if strings.EqualFold(message.Role, "meta") && message.Content == turnTerminalHistoryRecord &&
					(message.ClientID == rec.TurnID || message.ClientID == last.ID) &&
					!message.At.Before(rec.AllowedAt) && message.At.Before(s.recoveryBootAt) {
					settled = true
					break
				}
			}
		}
		if settled {
			continue
		}
		// A user may start, dismiss, or finish work while history is loading.
		view, err := binding.Documents.Read(ctx, persistence.Query{Namespace: restartRecoveryNamespace, Keys: []string{rec.ThreadID}})
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if len(view.Documents) == 0 {
			continue
		}
		var current restartRecoveryRecord
		if err := json.Unmarshal(view.Documents[0].Value, &current); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if current.Dismissed || current.State != rec.State || current.TurnID != rec.TurnID || current.Reason != rec.Reason ||
			!current.AllowedAt.Equal(rec.AllowedAt) || !current.UpdatedAt.Equal(rec.UpdatedAt) || current.UnexpectedExit != rec.UnexpectedExit {
			continue
		}
		active, err = session.ThreadExecutionActive(s.rt.SessionDir, rec.ThreadID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if active {
			continue
		}
		meta, found, err = binding.Sessions.Find(rec.ThreadID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if !found || !restartRecoverySessionEligible(meta) {
			continue
		}
		last.Status = TurnStatusInterrupted
		thread := entry(relocatedSessionMetadata(meta, s.registeredWorkspaces())).thread
		thread.Turns = []Turn{last}
		items = append(items, RestartRecoveryItem{Thread: thread, TurnID: last.ID, Reason: "interrupted"})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Thread.UpdatedAt.After(items[j].Thread.UpdatedAt) })
	return s.writeResponse(req.ID, struct {
		Items []RestartRecoveryItem `json:"items"`
	}{items}, nil)
}

func restartRecoverySessionEligible(meta session.Session) bool {
	return meta.ArchivedAt == nil && meta.Visibility != pluginhost.SessionVisibilityPlugin && meta.ParentID == "" && !projectExecutionDisabled(meta.Source)
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
