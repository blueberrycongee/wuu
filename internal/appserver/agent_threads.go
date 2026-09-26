package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func (s *Server) forwardAgentNotifications(threadID string, control *agentcontrol.AgentControl, ch <-chan subagent.Notification, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			if subagent.IsTerminal(n.Status) {
				// This is an idempotent fallback for controls that completed before
				// the reliable terminal finalizer was attached. In the normal path
				// AgentControl already ran the same finalizer synchronously while it
				// still owned the cross-process worker execution lease.
				if err := s.finalizeAgentTerminal(threadID, control, n); err != nil {
					providers.DebugLogf("finalize terminal agent %s fallback: %v", n.AgentID, err)
				}
				continue
			}
			now := time.Now().UTC()
			if n.Status == subagent.StatusRunning {
				_, turn, started := s.ensureLiveAgentThread(threadID, control, n.Snapshot, now)
				if started {
					_ = s.writeNotification(NotificationTurnStarted, TurnStartedNotification{
						ThreadID: n.Snapshot.ID,
						Turn:     turn,
					})
				}
			}
			_ = s.writeNotification(NotificationAgentUpdated, AgentUpdatedNotification{
				ThreadID: threadID,
				Agent:    s.agentFromSnapshot(control, n.Snapshot),
			})
		}
	}
}

type agentTerminalFinalizationKey struct {
	threadID    string
	agentID     string
	status      subagent.Status
	completedAt int64
}

const (
	participantRunCompletionMaxAttempts = 3
	participantRunCompletionRetryDelay  = 10 * time.Millisecond
)

type participantRunCompleter func(sessDir, participantID, agentID, outcome, summary string) error

func (s *Server) finalizeAgentTerminal(threadID string, control *agentcontrol.AgentControl, n subagent.Notification) error {
	return s.finalizeAgentTerminalWithCompleter(threadID, control, n, session.CompleteParticipantRun)
}

func (s *Server) finalizeAgentTerminalWithCompleter(threadID string, control *agentcontrol.AgentControl, n subagent.Notification, complete participantRunCompleter) error {
	if s == nil || !subagent.IsTerminal(n.Status) {
		return nil
	}
	agentID := strings.TrimSpace(n.Snapshot.ID)
	if agentID == "" {
		agentID = strings.TrimSpace(n.AgentID)
	}
	key := agentTerminalFinalizationKey{
		threadID:    strings.TrimSpace(threadID),
		agentID:     agentID,
		status:      n.Status,
		completedAt: n.Snapshot.CompletedAt.UnixNano(),
	}
	s.agentTerminalFinalizationMu.Lock()
	if s.agentTerminalFinalizations == nil {
		s.agentTerminalFinalizations = make(map[agentTerminalFinalizationKey]struct{})
	}
	if _, finalized := s.agentTerminalFinalizations[key]; finalized {
		s.agentTerminalFinalizationMu.Unlock()
		return nil
	}
	s.agentTerminalFinalizationMu.Unlock()

	participantID := strings.TrimSpace(n.Snapshot.ParticipantID)
	if participantID != "" {
		if agentID == "" {
			return errors.New("complete participant run: agent id is required")
		}
		summary := participantRunSummary(n.Snapshot.Result, n.Snapshot.Description)
		if n.Snapshot.Error != nil {
			summary = participantRunSummary(n.Snapshot.Error.Error(), n.Snapshot.Description)
		}
		if s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
			return errors.New("complete participant run: session store is unavailable")
		}
		if complete == nil {
			return errors.New("complete participant run: completer is unavailable")
		}
		var err error
		for attempt := 1; attempt <= participantRunCompletionMaxAttempts; attempt++ {
			err = complete(
				s.rt.SessionDir,
				participantID,
				agentID,
				string(n.Status),
				summary,
			)
			if err == nil {
				break
			}
			if attempt == participantRunCompletionMaxAttempts || s.closed.Load() {
				return fmt.Errorf("complete participant run %s/%s after %d attempt(s): %w", participantID, agentID, attempt, err)
			}
			time.Sleep(participantRunCompletionRetryDelay)
		}
	}

	// Commit the in-memory dedupe only after the durable core succeeds. Two
	// concurrent replays may both issue the idempotent SQLite update, but only
	// the winner below is allowed to publish terminal side effects.
	s.agentTerminalFinalizationMu.Lock()
	if _, finalized := s.agentTerminalFinalizations[key]; finalized {
		s.agentTerminalFinalizationMu.Unlock()
		return nil
	}
	s.agentTerminalFinalizations[key] = struct{}{}
	s.agentTerminalFinalizationMu.Unlock()

	closed := s.closed.Load()
	if !closed {
		now := time.Now().UTC()
		_ = s.writeNotification(NotificationAgentUpdated, AgentUpdatedNotification{
			ThreadID: threadID,
			Agent:    s.agentFromSnapshot(control, n.Snapshot),
		})
		s.completeLiveAgentThread(threadID, control, n.Snapshot, now)
	}
	if closed || n.Snapshot.Type == agentcontrol.FusionSidekickType {
		// Fusion delivers through its foreground delegation result. Starting a
		// second root turn here would race the Lead's review and cancellation.
		return nil
	}
	if s.isRootAgentSnapshot(control, threadID, n.Snapshot) {
		mailboxMessage := agentcontrol.NewAgentMailboxMessage(n.Snapshot)
		if control != nil {
			mailboxMessage = control.AgentMailboxMessage(n.Snapshot)
		}
		_ = s.writeNotification(NotificationAgentMailbox, AgentMailboxNotification{
			ThreadID: threadID,
			Message:  mailboxMessage,
		})
		if control != nil {
			resultID := control.AgentResultDeliveryID(n.Snapshot)
			msg := control.AgentCompletionChatMessage(n.Snapshot, agentthread.RootPath)
			consumer, _ := control.AgentResultDeliveryConsumer(resultID)
			if consumer == "" {
				if !s.steerAgentCompletion(threadID, resultID, msg) {
					s.enqueueAgentCompletionTurn(threadID, n.Snapshot.ID, resultID, msg, &n.Snapshot)
				}
			}
		}
	}
	return nil
}

func (s *Server) forwardAgentStreamNotifications(threadID string, control *agentcontrol.AgentControl, ch <-chan subagent.StreamNotification, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			now := time.Now().UTC()
			th, turn, started := s.ensureLiveAgentThread(threadID, control, n.Snapshot, now)
			if th == nil {
				continue
			}
			if started {
				_ = s.writeNotification(NotificationTurnStarted, TurnStartedNotification{
					ThreadID: th.ID,
					Turn:     turn,
				})
			}
			th.mu.Lock()
			batch := th.applyStreamEventLocked(turn.ID, n.Event, now)
			th.mu.Unlock()
			s.notifyOutboundBatch(batch)
			_ = s.writeNotification(NotificationTurnEvent, TurnEventNotification{
				ThreadID: th.ID,
				TurnID:   turn.ID,
				Event:    sanitizeStreamEvent(n.Event),
			})
		}
	}
}

func (s *Server) isRootAgentSnapshot(control *agentcontrol.AgentControl, threadID string, snap subagent.SubAgentSnapshot) bool {
	parentID := strings.TrimSpace(snap.ParentID)
	if parentID == "" {
		return true
	}
	if control != nil && parentID == control.SessionID() {
		return true
	}
	return parentID == strings.TrimSpace(threadID)
}

func isRunningSubAgentStatus(status subagent.Status) bool {
	switch status {
	case subagent.StatusRunning, subagent.StatusPending, subagent.StatusQueued:
		return true
	default:
		return false
	}
}

func (s *Server) ensureLiveAgentThread(rootThreadID string, control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot, now time.Time) (*threadState, Turn, bool) {
	if strings.TrimSpace(snap.ID) == "" {
		return nil, Turn{}, false
	}
	th, created := s.ensureAgentThreadState(rootThreadID, control, snap, now)
	if th == nil {
		return nil, Turn{}, false
	}
	th.mu.Lock()
	s.applyAgentSnapshotLocked(th, rootThreadID, snap, now)
	turn, started := th.startAgentTurnLocked(now)
	if created {
		started = true
	}
	th.mu.Unlock()
	return th, turn, started
}

func (s *Server) ensureAgentThreadState(rootThreadID string, control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot, now time.Time) (*threadState, bool) {
	if th := s.thread(snap.ID); th != nil {
		return th, false
	}

	history := s.agentSnapshotHistory(control, snap)
	providerName, inheritedModel := s.rt.ProviderName, s.rt.Model
	if root := s.thread(rootThreadID); root != nil {
		root.mu.Lock()
		providerName = firstNonEmpty(root.ModelProvider, providerName)
		inheritedModel = firstNonEmpty(root.Model, inheritedModel)
		root.mu.Unlock()
	}
	if pinnedProvider, _ := parseParticipantModelPin(snap.ModelPin); strings.TrimSpace(pinnedProvider) != "" {
		providerName = strings.TrimSpace(pinnedProvider)
	}
	th := newThreadState(snap.ID, history, providerName, firstNonEmpty(snap.Model, inheritedModel), firstNonEmpty(snapCWD(rootThreadID, s), s.rt.RootDir), false, now)
	th.ParentID = strings.TrimSpace(rootThreadID)
	th.AgentPath = snap.AgentPath
	th.ReadOnly = true
	th.CreatedAt = firstNonZeroTime(snap.StartedAt, now)
	th.UpdatedAt = firstNonZeroTime(snap.ActivityAt, snap.CompletedAt, snap.StartedAt, now)

	s.mu.Lock()
	if existing := s.threads[snap.ID]; existing != nil {
		s.mu.Unlock()
		return existing, false
	}
	s.threads[snap.ID] = th
	s.mu.Unlock()
	return th, true
}

func (s *Server) agentSnapshotHistory(control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot) []providers.ChatMessage {
	if control != nil && control.Manager() != nil {
		if history, ok := control.Manager().History(snap.ID); ok && len(history) > 0 {
			return history
		}
	}
	history := make([]providers.ChatMessage, 0, 2)
	if strings.TrimSpace(snap.Description) != "" {
		history = append(history, providers.ChatMessage{Role: "user", Content: snap.Description})
	}
	if strings.TrimSpace(snap.Result) != "" {
		history = append(history, providers.ChatMessage{Role: "assistant", Content: snap.Result})
	} else if snap.Error != nil {
		history = append(history, providers.ChatMessage{Role: "assistant", Content: "Worker failed: " + snap.Error.Error()})
	}
	return history
}

func (s *Server) applyAgentSnapshotLocked(th *threadState, rootThreadID string, snap subagent.SubAgentSnapshot, now time.Time) {
	th.ParentID = strings.TrimSpace(rootThreadID)
	th.AgentPath = snap.AgentPath
	th.ReadOnly = true
	if !snap.StartedAt.IsZero() {
		th.CreatedAt = snap.StartedAt
	}
	switch snap.Status {
	case subagent.StatusRunning, subagent.StatusPending, subagent.StatusQueued:
		th.UpdatedAt = now
	case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled, subagent.StatusInterrupted:
		th.UpdatedAt = firstNonZeroTime(snap.CompletedAt, now)
	default:
		th.UpdatedAt = now
	}
}

func (s *Server) completeLiveAgentThread(rootThreadID string, control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot, now time.Time) {
	th, turn, status, turnErr := s.syncFinalAgentThread(rootThreadID, control, snap, now)
	if th == nil {
		return
	}
	if status == TurnStatusFailed || status == TurnStatusInterrupted {
		structured := BuildTurnError(turnErr, th.ModelProvider)
		turn.Error = &structured
		th.mu.Lock()
		th.replaceTurnLocked(turn)
		th.mu.Unlock()
		_ = s.writeNotification(NotificationTurnError, TurnErrorNotification{
			ThreadID:   th.ID,
			TurnID:     turn.ID,
			Error:      structured.Message,
			Code:       structured.Code,
			Category:   string(structured.Category),
			Provider:   structured.Provider,
			StatusCode: structured.StatusCode,
			Turn:       turn,
		})
		return
	}
	_ = s.writeNotification(NotificationTurnCompleted, TurnCompletedNotification{
		ThreadID: th.ID,
		Turn:     turn,
		Content:  agentResultPreview(control, snap),
	})
}

func (s *Server) syncFinalAgentThread(rootThreadID string, control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot, now time.Time) (*threadState, Turn, TurnStatus, error) {
	th, _ := s.ensureAgentThreadState(rootThreadID, control, snap, now)
	if th == nil {
		return nil, Turn{}, "", nil
	}
	history := s.agentSnapshotHistory(control, snap)
	status, turnErr := turnStatusForSubAgentSnapshot(snap)

	th.mu.Lock()
	s.applyAgentSnapshotLocked(th, rootThreadID, snap, now)
	if len(history) > 0 {
		th.History = history
	}
	turnID := th.currentTurn
	if turnID == "" && len(th.Turns) > 0 {
		turnID = th.Turns[len(th.Turns)-1].ID
	}
	if turnID == "" {
		turn, _ := th.startAgentTurnLocked(now)
		turnID = turn.ID
	}
	turn := th.completeTurnLocked(turnID, status, turnErr, now, "", "", false)
	th.mu.Unlock()
	return th, turn, status, turnErr
}

func turnStatusForSubAgentSnapshot(snap subagent.SubAgentSnapshot) (TurnStatus, error) {
	switch snap.Status {
	case subagent.StatusFailed:
		if snap.Error != nil {
			return TurnStatusFailed, snap.Error
		}
		return TurnStatusFailed, errors.New("worker failed")
	case subagent.StatusCancelled:
		return TurnStatusInterrupted, context.Canceled
	case subagent.StatusInterrupted:
		if snap.Error != nil {
			return TurnStatusInterrupted, snap.Error
		}
		return TurnStatusInterrupted, errors.New("worker interrupted")
	default:
		return TurnStatusCompleted, nil
	}
}

func snapCWD(rootThreadID string, s *Server) string {
	if s == nil {
		return ""
	}
	if root := s.thread(rootThreadID); root != nil {
		root.mu.Lock()
		defer root.mu.Unlock()
		return root.CWD
	}
	return ""
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func (s *Server) agentFromSnapshot(control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot) Agent {
	ref := agentcontrol.AgentResultReference{}
	if control != nil {
		ref = control.AgentResultReference(snap)
	} else {
		preview, truncated := agentcontrol.AgentResultPreview(snap.Result)
		ref = agentcontrol.AgentResultReference{Preview: preview, Bytes: len([]byte(snap.Result)), Truncated: truncated}
	}
	out := Agent{
		ID:                  snap.ID,
		Type:                snap.Type,
		TaskName:            snap.TaskName,
		AgentProfile:        snap.AgentProfile,
		AgentPath:           snap.AgentPath,
		ParentID:            snap.ParentID,
		Description:         snap.Description,
		Status:              string(snap.Status),
		ModelAlias:          snap.ModelAlias,
		ModelAliasFallback:  snap.ModelAliasFallback,
		Provider:            snap.ResolvedProvider,
		Model:               snap.ResolvedModel,
		APIModel:            snap.ResolvedAPIModel,
		Effort:              snap.ResolvedEffort,
		Variant:             snap.ResolvedVariant,
		Result:              ref.Preview,
		ResultPath:          ref.Path,
		ResultBytes:         ref.Bytes,
		ResultTruncated:     ref.Truncated,
		InputTokens:         snap.InputTokens,
		OutputTokens:        snap.OutputTokens,
		CacheCreationTokens: snap.CacheCreationTokens,
		CacheReadTokens:     snap.CacheReadTokens,
		StartedAt:           snap.StartedAt,
		CompletedAt:         snap.CompletedAt,
		Participant:         s.participantSummaryForSnapshot(snap),
	}
	if snap.Error != nil {
		out.Error = snap.Error.Error()
	}
	return out
}

// participantSummaryForSnapshot resolves the participant identity for a
// sub-agent snapshot. Store hits are cached (ephemeral participants are
// immutable after creation). Any miss — empty ParticipantID on legacy
// snapshots, missing store row, or lookup error — falls back to a summary
// synthesized from the snapshot's type/task name so display attribution is
// never absent.
func (s *Server) participantSummaryForSnapshot(snap subagent.SubAgentSnapshot) *participant.Summary {
	if summary, ok := s.resolveParticipantSummary(snap.ParticipantID); ok {
		return &summary
	}
	fallback := participant.Summary{
		ID:   strings.TrimSpace(snap.ParticipantID),
		Name: participant.DeriveEphemeralName(snap.TaskName, snap.Type),
		Kind: string(participant.KindEphemeral),
		Role: snap.Type,
	}
	return &fallback
}

// resolveParticipantSummary looks up a participant summary by ID through
// the in-memory cache, falling back to the session store on miss.
func (s *Server) resolveParticipantSummary(id string) (participant.Summary, bool) {
	id = strings.TrimSpace(id)
	if id == "" || s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return participant.Summary{}, false
	}

	s.participantMu.Lock()
	summary, ok := s.participantSummaryCache[id]
	s.participantMu.Unlock()
	if ok {
		return summary, true
	}

	p, err := session.GetParticipant(s.rt.SessionDir, id)
	if err != nil {
		return participant.Summary{}, false
	}
	summary = p.Summary()
	// Avatar rides on the cached summary so the disk read happens once
	// per participant per cache generation (invalidateParticipantSummary
	// covers profile saves, retires, and roster edits). Capped — see
	// participantSummaryAvatarMaxBytes.
	summary.AvatarImage = participantSummaryAvatarDataURL(p.Workspace)

	s.participantMu.Lock()
	if s.participantSummaryCache == nil {
		s.participantSummaryCache = make(map[string]participant.Summary)
	}
	s.participantSummaryCache[id] = summary
	s.participantMu.Unlock()
	return summary, true
}

func agentResultPreview(control *agentcontrol.AgentControl, snap subagent.SubAgentSnapshot) string {
	if control == nil {
		preview, _ := agentcontrol.AgentResultPreview(snap.Result)
		return preview
	}
	return control.AgentResultReference(snap).Preview
}
