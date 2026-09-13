package appserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/compact"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type outboundNotification struct {
	method string
	params any
}

const (
	manualContextCompactionReason         = "manual"
	manualContextCompactionInProgressText = "Manual context compaction in progress."
	manualContextCompactionFailedText     = "Manual context compaction failed; history is unchanged."
)

func newThreadState(id string, history []providers.ChatMessage, rtProvider, model, cwd string, persistHistory bool, now time.Time) *threadState {
	return &threadState{
		ID:             id,
		History:        cloneHistory(history),
		historyHeadSeq: maxHistorySeq(history),
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
		ModelProvider:  rtProvider,
		Model:          model,
		CWD:            cwd,
		EngineID:       string(agentengine.EngineWuu),
		Turns:          turnsFromHistory(id, history, now),
		PersistHistory: persistHistory,
		toolItems:      make(map[string]string),
	}
}

func (th *threadState) snapshotLocked() Thread {
	return th.snapshotTurnsLocked(th.Turns)
}

func (th *threadState) snapshotTurnsLocked(turns []Turn) Thread {
	status := ThreadStatusIdle
	if th.running {
		status = ThreadStatusInProgress
	}
	return Thread{
		ID:              th.ID,
		Source:          th.Source,
		ParentID:        th.ParentID,
		AgentPath:       th.AgentPath,
		Preview:         firstNonEmpty(th.Title, threadPreview(th.History)),
		Title:           th.Title,
		ModelProvider:   th.ModelProvider,
		Model:           th.Model,
		ModelVariant:    th.ModelVariant,
		ModelEffort:     th.ModelEffort,
		PermissionMode:  th.PermissionMode,
		EngineID:        string(agentengine.NormalizeEngineID(th.EngineID)),
		CWD:             th.CWD,
		WorkspaceID:     th.WorkspaceID,
		WorkspaceKind:   th.WorkspaceKind,
		Status:          status,
		TreeInterrupted: th.workerTreeFrozen,
		// Plugin-visible sessions remain writable by their owning plugin, but the
		// user-facing conversation is an inspector and must not expose a composer.
		ReadOnly:              th.ReadOnly || th.Visibility == pluginhost.SessionVisibilityPlugin,
		Ephemeral:             th.Ephemeral,
		Pinned:                th.PinnedAt != nil,
		FolderID:              th.FolderID,
		Archived:              th.ArchivedAt != nil,
		ForkedFromID:          th.ForkedFromID,
		ForkedFromTurnID:      th.ForkedFromTurnID,
		ForkedFromItemID:      th.ForkedFromItemID,
		Worktree:              threadWorktreeInfo(th.WorktreePath, th.WorktreeBaseHEAD, th.WorktreeBaseRepo),
		CreatedAt:             th.CreatedAt,
		UpdatedAt:             th.UpdatedAt,
		LatestCompletedTurnID: latestCompletedTurnID(th.Turns),
		Turns:                 cloneTurns(turns),
	}
}

func latestCompletedTurnID(turns []Turn) string {
	for index := len(turns) - 1; index >= 0; index-- {
		if turns[index].Status != TurnStatusInProgress {
			return turns[index].ID
		}
	}
	return ""
}

func threadWorktreeInfo(path, baseHEAD, baseRepo string) *WorktreeInfo {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &WorktreeInfo{
		Path:     path,
		BaseHEAD: strings.TrimSpace(baseHEAD),
		BaseRepo: strings.TrimSpace(baseRepo),
	}
}

func (th *threadState) startTurnLocked(turnID string, userMsg providers.ChatMessage, now time.Time) Turn {
	th.currentTurn = turnID
	th.currentTurnKind = TurnKindUser
	th.currentTurnResumed = false
	th.running = true
	th.runningProviderName = th.ModelProvider
	th.runningModel = th.Model
	th.UpdatedAt = now
	th.pendingSteers = nil
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)

	userItem := chatMessageItem(th.nextItemIDLocked(turnID), userMsg)
	turn := Turn{
		ID:            turnID,
		ModelProvider: th.ModelProvider,
		Model:         th.Model,
		Items:         []ThreadItem{userItem},
		ItemsView:     TurnItemsViewFull,
		Status:        TurnStatusInProgress,
		StartedAt:     &now,
	}
	th.Turns = append(th.Turns, turn)
	return turn
}

// resumePersistedUserTurnLocked reopens the display turn for an idempotent
// synthetic user record that was durably appended before a process crashed.
// It avoids appending a second user bubble while allowing the provider work to
// resume under a fresh execution lease.
func (th *threadState) resumePersistedUserTurnLocked(clientID string, now time.Time) (Turn, bool) {
	clientID = strings.TrimSpace(clientID)
	if th == nil || clientID == "" {
		return Turn{}, false
	}
	for i := len(th.Turns) - 1; i >= 0; i-- {
		turn := th.Turns[i]
		found := false
		for _, item := range turn.Items {
			if strings.TrimSpace(item.SourceID) == clientID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		turn.Status = TurnStatusInProgress
		turn.CompletedAt = nil
		turn.Error = nil
		turn.ModelProvider = th.ModelProvider
		turn.Model = th.Model
		if turn.StartedAt == nil {
			turn.StartedAt = &now
		}
		th.Turns[i] = turn
		th.currentTurn = turn.ID
		th.currentTurnKind = TurnKindUser
		th.currentTurnResumed = true
		th.running = true
		th.runningProviderName = th.ModelProvider
		th.runningModel = th.Model
		th.UpdatedAt = now
		th.pendingSteers = nil
		th.nextItemIndex = maxTurnItemIndex(turn)
		th.activeAgentItemID = ""
		th.activeReasoningItemID = ""
		th.toolItems = make(map[string]string)
		return turn, true
	}
	return Turn{}, false
}

func (th *threadState) appendUserMessageTurnLocked(turnID string, userMsg providers.ChatMessage, now time.Time) Turn {
	th.currentTurn = turnID
	th.currentTurnResumed = false
	th.UpdatedAt = now
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)

	userItem := chatMessageItem(th.nextItemIDLocked(turnID), userMsg)
	turn := Turn{
		ID:          turnID,
		Items:       []ThreadItem{userItem},
		ItemsView:   TurnItemsViewFull,
		Status:      TurnStatusCompleted,
		StartedAt:   &now,
		CompletedAt: &now,
	}
	th.Turns = append(th.Turns, turn)
	return turn
}

func (th *threadState) startInternalTurnLocked(turnID string, now time.Time) Turn {
	return th.startInternalTurnWithKindLocked(turnID, TurnKindInternal, now)
}

func (th *threadState) startCompactTurnLocked(turnID string, displayMsg providers.ChatMessage, now time.Time) Turn {
	turn := th.startInternalTurnWithKindLocked(turnID, TurnKindCompact, now)
	if strings.EqualFold(displayMsg.Role, "user") && strings.TrimSpace(chatMessageDisplayContent(displayMsg)) != "" {
		item := chatMessageItem(th.nextItemIDLocked(turnID), displayMsg)
		turn.Items = append(turn.Items, item)
	}
	turn.Items = append(turn.Items, ThreadItem{
		ID:     th.nextItemIDLocked(turnID),
		Type:   ThreadItemContextCompaction,
		Status: ThreadItemStatusInProgress,
		Text:   manualContextCompactionInProgressText,
		Reason: manualContextCompactionReason,
	})
	th.replaceTurnLocked(turn)
	return turn
}

func (th *threadState) startInternalTurnWithKindLocked(turnID string, kind TurnKind, now time.Time) Turn {
	th.currentTurn = turnID
	th.currentTurnKind = kind
	th.currentTurnResumed = false
	th.running = true
	th.runningProviderName = th.ModelProvider
	th.runningModel = th.Model
	th.UpdatedAt = now
	th.pendingSteers = nil
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)

	turn := Turn{
		ID:            turnID,
		Kind:          kind,
		ModelProvider: th.ModelProvider,
		Model:         th.Model,
		Items:         []ThreadItem{},
		ItemsView:     TurnItemsViewFull,
		Status:        TurnStatusInProgress,
		StartedAt:     &now,
	}
	th.Turns = append(th.Turns, turn)
	return turn
}

func (th *threadState) startAgentTurnLocked(now time.Time) (Turn, bool) {
	if th.running && th.currentTurn != "" {
		if strings.TrimSpace(th.runningProviderName) == "" {
			th.runningProviderName = th.ModelProvider
		}
		if strings.TrimSpace(th.runningModel) == "" {
			th.runningModel = th.Model
		}
		turn := th.ensureTurnLocked(th.currentTurn, now)
		th.nextItemIndex = max(th.nextItemIndex, maxTurnItemIndex(turn))
		return turn, false
	}

	if len(th.Turns) == 0 {
		turnID := fmt.Sprintf("%s-turn-%04d", th.ID, 1)
		turn := Turn{
			ID:            turnID,
			ModelProvider: th.ModelProvider,
			Model:         th.Model,
			Items:         []ThreadItem{},
			ItemsView:     TurnItemsViewFull,
			Status:        TurnStatusInProgress,
			StartedAt:     &now,
		}
		th.Turns = append(th.Turns, turn)
		th.currentTurn = turnID
		th.currentTurnKind = TurnKindInternal
		th.currentTurnResumed = false
		th.running = true
		th.runningProviderName = th.ModelProvider
		th.runningModel = th.Model
		th.pendingSteers = nil
		th.nextItemIndex = 0
		return turn, true
	}

	index := len(th.Turns) - 1
	turn := th.Turns[index]
	started := turn.Status != TurnStatusInProgress
	turn.Status = TurnStatusInProgress
	if turn.StartedAt == nil {
		startedAt := th.CreatedAt
		if startedAt.IsZero() {
			startedAt = now
		}
		turn.StartedAt = &startedAt
	}
	turn.CompletedAt = nil
	turn.Error = nil
	turn.ModelProvider = th.ModelProvider
	turn.Model = th.Model
	th.Turns[index] = turn
	th.currentTurn = turn.ID
	th.currentTurnKind = TurnKindInternal
	th.currentTurnResumed = false
	th.running = true
	th.runningProviderName = th.ModelProvider
	th.runningModel = th.Model
	th.pendingSteers = nil
	th.nextItemIndex = max(th.nextItemIndex, maxTurnItemIndex(turn))
	if th.toolItems == nil {
		th.toolItems = make(map[string]string)
	}
	return turn, started
}

func (th *threadState) completeTurnLocked(turnID string, status TurnStatus, err error, now time.Time, finishReason, stopReason string, truncated bool) Turn {
	turn := th.finishTurnLocked(turnID, status, err, now, finishReason, stopReason, truncated)
	th.releaseTurnExecutionLocked(turnID)
	return turn
}

// finishTurnLocked records the terminal turn state while retaining the
// execution lease. Callers that still need the per-turn runtime (for example,
// to persist its trace or restore temporary callbacks) must finish that work
// before calling releaseTurnExecutionLocked.
func (th *threadState) finishTurnLocked(turnID string, status TurnStatus, err error, now time.Time, finishReason, stopReason string, truncated bool) Turn {
	th.UpdatedAt = now
	if th.activeAgentItemID != "" {
		if item, ok := th.itemLocked(turnID, th.activeAgentItemID); ok && status == TurnStatusCompleted {
			item.Status = ThreadItemStatusCompleted
			// Some engines finish after streaming the final text without
			// emitting a provider ChatMessage. The turn completion boundary is
			// still authoritative: that active assistant item is the final
			// answer and must be actionable in the renderer.
			item.Terminal = true
			item.FinishReason = finishReason
			item.StopReason = stopReason
			item.Truncated = truncated
			th.upsertItemLocked(turnID, item, now)
		}
		th.activeAgentItemID = ""
	}
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)

	turn := th.ensureTurnLocked(turnID, now)
	if turn.Kind == TurnKindCompact && status == TurnStatusFailed {
		th.finishPendingContextCompactionLocked(turnID, ThreadItemStatusFailed, manualContextCompactionFailedText, now)
		turn = th.ensureTurnLocked(turnID, now)
	}
	if item, ok := th.pendingStreamReconnectItemLocked(turnID, now); ok {
		// A turn that ends with a reconnect still pending freezes the row at
		// its last reported retry (e.g. interrupted at 2/5). A completed turn
		// should never carry one — the connected event removes it — but drop
		// it defensively so a successful recovery leaves no trace.
		if status == TurnStatusCompleted {
			th.removeItemLocked(turnID, item.ID, now)
		} else {
			item.Status = ThreadItemStatusFailed
			item.RetryAtMS = 0
			th.upsertItemLocked(turnID, item, now)
		}
		turn = th.ensureTurnLocked(turnID, now)
	}
	turn.Status = status
	if err != nil {
		turn.Error = &TurnError{Message: err.Error()}
	}
	turn.FinishReason = finishReason
	turn.StopReason = stopReason
	turn.Truncated = truncated
	turn.CompletedAt = &now
	if turn.AnswerReadyAt == nil && status == TurnStatusCompleted {
		answerReadyAt := now
		turn.AnswerReadyAt = &answerReadyAt
	}
	if turn.StartedAt != nil {
		duration := now.Sub(*turn.StartedAt).Milliseconds()
		turn.DurationMS = &duration
	}
	th.replaceTurnLocked(turn)
	return turn
}

func (th *threadState) releaseTurnExecutionLocked(turnID string) {
	if th.currentTurn != turnID {
		return
	}
	th.releaseThreadExecutionLeaseLocked()
	th.running = false
	th.currentTurn = ""
	th.currentTurnKind = ""
	th.currentExecutionRunID = ""
	th.currentTurnResumed = false
	th.runningProviderName = ""
	th.runningModel = ""
	th.cancel = nil
	th.completeAfterAnswerReadyCancel = false
	th.steerWake = nil
	th.steerWakeClosed = false
	th.activeSteerDocument = nil
	th.activeSteerContextSet = false
	th.steerDocumentOverrides = nil
	th.maybeReleasePluginGenerationExecutionLeaseLocked()
	th.notifyIdleLocked()
}

func (th *threadState) addIdleWaiterLocked() <-chan struct{} {
	ch := make(chan struct{})
	th.idleWaiters = append(th.idleWaiters, ch)
	return ch
}

func (th *threadState) notifyIdleLocked() {
	for _, ch := range th.idleWaiters {
		close(ch)
	}
	th.idleWaiters = nil
}

func threadCurrentTurnIsAnswerReady(th *threadState) bool {
	if th == nil {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	return currentTurnIsAnswerReadyLocked(th)
}

func currentTurnIsAnswerReadyLocked(th *threadState) bool {
	if th == nil || strings.TrimSpace(th.currentTurn) == "" {
		return false
	}
	turn, ok := turnByID(th.Turns, th.currentTurn)
	if !ok {
		return false
	}
	return turnIsAnswerReady(turn)
}

func turnIsAnswerReady(turn Turn) bool {
	if turn.AnswerReadyAt != nil {
		return true
	}
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage && item.Status == ThreadItemStatusCompleted && item.Terminal {
			return true
		}
	}
	return false
}

func applyTokenUsageToTurn(turn *Turn, usage providers.TokenUsage, contextTokens int, model string) {
	if turn == nil {
		return
	}
	turn.InputTokens = usage.InputTokens
	turn.OutputTokens = usage.OutputTokens
	turn.ContextTokens = contextTokens
	turn.CacheCreationTokens = usage.CacheCreationTokens
	turn.CacheReadTokens = usage.CacheReadTokens
	turn.UsageModel = strings.TrimSpace(model)
}

func applyTokenUsageMetasToTurns(turns []Turn, metas []persistedMessage) []Turn {
	if len(turns) == 0 || len(metas) == 0 {
		return turns
	}
	usages := make([]persistedMessage, 0, len(metas))
	for _, meta := range metas {
		if !strings.EqualFold(strings.TrimSpace(meta.Role), "meta") ||
			strings.TrimSpace(meta.Content) != "token_usage" {
			continue
		}
		if meta.InputTokens == 0 &&
			meta.OutputTokens == 0 &&
			meta.ContextTokens == 0 &&
			meta.CacheCreationTokens == 0 &&
			meta.CacheReadTokens == 0 {
			continue
		}
		usages = append(usages, meta)
	}
	if len(usages) == 0 {
		return turns
	}
	turnIndex := len(turns) - len(usages)
	usageIndex := 0
	if turnIndex < 0 {
		usageIndex = -turnIndex
		turnIndex = 0
	}
	for turnIndex < len(turns) && usageIndex < len(usages) {
		meta := usages[usageIndex]
		applyTokenUsageToTurn(&turns[turnIndex], providers.TokenUsage{
			InputTokens:         meta.InputTokens,
			OutputTokens:        meta.OutputTokens,
			CacheCreationTokens: meta.CacheCreationTokens,
			CacheReadTokens:     meta.CacheReadTokens,
		}, meta.ContextTokens, meta.Model)
		turnIndex++
		usageIndex++
	}
	return turns
}

func (th *threadState) takePendingSteersLocked(turnID string, now time.Time) ([]providers.ChatMessage, []outboundNotification) {
	if len(th.pendingSteers) == 0 || turnID == "" || turnID != th.currentTurn {
		return nil, nil
	}
	steers := cloneHistory(th.pendingSteers)
	th.pendingSteers = nil
	var out []outboundNotification
	for i := range steers {
		if strings.TrimSpace(steers[i].Role) == "" {
			steers[i].Role = "user"
		}
		steers[i].Steered = true
		item := chatMessageItem(th.nextItemIDLocked(turnID), steers[i])
		if item.ID == "" {
			continue
		}
		th.upsertItemLocked(turnID, item, now)
		out = append(out, itemStarted(th.ID, turnID, item, now), itemCompleted(th.ID, turnID, item, now))
	}
	return steers, out
}

func (th *threadState) drainPendingSteersLocked() []providers.ChatMessage {
	if len(th.pendingSteers) == 0 {
		return nil
	}
	steers := cloneHistory(th.pendingSteers)
	th.pendingSteers = nil
	return steers
}

func (th *threadState) removePendingSteerLocked(id string) bool {
	_, removed := th.takePendingSteerLocked(id)
	return removed
}

func (th *threadState) takePendingSteerLocked(id string) (providers.ChatMessage, bool) {
	id = strings.TrimSpace(id)
	if id == "" || len(th.pendingSteers) == 0 {
		return providers.ChatMessage{}, false
	}
	next := th.pendingSteers[:0]
	var removed providers.ChatMessage
	found := false
	for _, msg := range th.pendingSteers {
		if !found && msg.ClientID == id {
			removed = msg
			found = true
			continue
		}
		next = append(next, msg)
	}
	th.pendingSteers = next
	return removed, found
}

func maxTurnItemIndex(turn Turn) int {
	maxIndex := len(turn.Items)
	for _, item := range turn.Items {
		_, suffix, ok := strings.Cut(item.ID, "-item-")
		if !ok {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(suffix, "%d", &n); err == nil && n > maxIndex {
			maxIndex = n
		}
	}
	return maxIndex
}

func (th *threadState) applyStreamEventLocked(turnID string, ev providers.StreamEvent, now time.Time) []outboundNotification {
	var out []outboundNotification
	switch ev.Type {
	case providers.EventLifecycle:
		if ev.Lifecycle == nil {
			return nil
		}
		switch ev.Lifecycle.Phase {
		case providers.StreamPhaseReconnecting:
			out = append(out, th.upsertStreamReconnectItemLocked(turnID, ev.Lifecycle, now)...)
			if !ev.Lifecycle.ResetPartial {
				return out
			}
			removed := make(map[string]struct{})
			for sourceID, itemID := range th.toolItems {
				item, ok := th.itemLocked(turnID, itemID)
				if !ok {
					delete(th.toolItems, sourceID)
					continue
				}
				if item.Status != ThreadItemStatusInProgress {
					continue
				}
				delete(th.toolItems, sourceID)
				if _, seen := removed[item.ID]; seen {
					continue
				}
				if th.removeItemLocked(turnID, item.ID, now) {
					removed[item.ID] = struct{}{}
					out = append(out, itemRemoved(th.ID, turnID, item.ID, now))
				}
			}
			return out
		case providers.StreamPhaseConnected:
			// A successful (re)connect erases the reconnect row: recovery
			// leaves no trace in the message flow.
			if item, ok := th.pendingStreamReconnectItemLocked(turnID, now); ok {
				if th.removeItemLocked(turnID, item.ID, now) {
					out = append(out, itemRemoved(th.ID, turnID, item.ID, now))
				}
			}
			return out
		case providers.StreamPhaseFailed:
			return th.settleStreamReconnectItemLocked(turnID, ev.Lifecycle, now)
		}
		return nil
	case providers.EventContentDelta:
		if ev.Content == "" {
			return nil
		}
		// Streaming text usually has unknown phase. If the provider exposes
		// Codex-style phase metadata on the active output item, keep it.
		item, started := th.ensureActiveAgentItemLocked(turnID, now)
		if ev.Phase == providers.MessagePhaseFinalAnswer {
			item.Terminal = true
		}
		if started {
			out = append(out, itemStarted(th.ID, turnID, item, now))
		}
		item.Text += ev.Content
		th.upsertItemLocked(turnID, item, now)
		out = append(out, outboundNotification{
			method: NotificationAgentMessageDelta,
			params: AgentMessageDeltaNotification{
				ThreadID: th.ID,
				TurnID:   turnID,
				ItemID:   item.ID,
				Delta:    ev.Content,
			},
		})
	case providers.EventContentReplace:
		if th.activeAgentItemID == "" && ev.Content == "" {
			return nil
		}
		item, started := th.ensureActiveAgentItemLocked(turnID, now)
		if ev.Phase == providers.MessagePhaseFinalAnswer {
			item.Terminal = true
		}
		if started {
			out = append(out, itemStarted(th.ID, turnID, item, now))
		}
		item.Text = ev.Content
		th.upsertItemLocked(turnID, item, now)
		out = append(out, outboundNotification{
			method: NotificationAgentMessageReplace,
			params: AgentMessageReplaceNotification{
				ThreadID: th.ID,
				TurnID:   turnID,
				ItemID:   item.ID,
				Text:     ev.Content,
			},
		})
	case providers.EventThinkingDelta:
		if ev.Content == "" {
			return nil
		}
		item, started := th.ensureActiveReasoningItemLocked(turnID, now)
		if started {
			out = append(out, itemStarted(th.ID, turnID, item, now))
		}
		item.Text += ev.Content
		th.upsertItemLocked(turnID, item, now)
		out = append(out, outboundNotification{
			method: NotificationReasoningDelta,
			params: ReasoningDeltaNotification{
				ThreadID: th.ID,
				TurnID:   turnID,
				ItemID:   item.ID,
				Delta:    ev.Content,
			},
		})
	case providers.EventThinkingReplace:
		if th.activeReasoningItemID == "" && ev.Content == "" {
			return nil
		}
		item, started := th.ensureActiveReasoningItemLocked(turnID, now)
		if started {
			out = append(out, itemStarted(th.ID, turnID, item, now))
		}
		item.Text = ev.Content
		th.upsertItemLocked(turnID, item, now)
		out = append(out, outboundNotification{
			method: NotificationReasoningReplace,
			params: ReasoningReplaceNotification{
				ThreadID: th.ID,
				TurnID:   turnID,
				ItemID:   item.ID,
				Text:     ev.Content,
			},
		})
	case providers.EventThinkingDone:
		if th.activeReasoningItemID == "" {
			return nil
		}
		item, ok := th.itemLocked(turnID, th.activeReasoningItemID)
		if !ok {
			return nil
		}
		item.Status = ThreadItemStatusCompleted
		th.upsertItemLocked(turnID, item, now)
		th.activeReasoningItemID = ""
		out = append(out, itemCompleted(th.ID, turnID, item, now))
	case providers.EventToolUseStart:
		if ev.ToolCall == nil {
			return nil
		}
		out = append(out, th.completeActiveAgentItemLocked(turnID, now, false)...)
		item := th.toolItemFromCallLocked(turnID, *ev.ToolCall, now)
		out = append(out, itemStarted(th.ID, turnID, item, now))
	case providers.EventToolUseDelta:
		if ev.Content == "" {
			return nil
		}
		item, ok := th.latestToolItemLocked(turnID)
		if !ok {
			return nil
		}
		item.Arguments += ev.Content
		th.upsertItemLocked(turnID, item, now)
		out = append(out, outboundNotification{
			method: NotificationToolCallDelta,
			params: ToolCallDeltaNotification{
				ThreadID: th.ID,
				TurnID:   turnID,
				ItemID:   item.ID,
				Delta:    ev.Content,
			},
		})
	case providers.EventToolUseEnd:
		if ev.ToolCall == nil {
			return nil
		}
		item := th.toolItemFromCallLocked(turnID, *ev.ToolCall, now)
		if ev.ToolResult != "" || ev.ToolResultDetail != nil {
			item.Result += ev.ToolResult
			item.ResultDetail = cloneToolResult(ev.ToolResultDetail)
			item.Status = ThreadItemStatusCompleted
			th.upsertItemLocked(turnID, item, now)
			out = append(out, outboundNotification{
				method: NotificationToolCallOutput,
				params: ToolCallOutputNotification{
					ThreadID: th.ID,
					TurnID:   turnID,
					ItemID:   item.ID,
					Delta:    ev.ToolResult,
					Detail:   cloneToolResult(ev.ToolResultDetail),
				},
			})
			out = append(out, itemCompleted(th.ID, turnID, item, now))
		}
	case providers.EventMessage:
		if ev.Message == nil {
			return nil
		}
		if ev.Message.Hidden || compact.IsInternalContextMessage(*ev.Message) {
			return nil
		}
		out = append(out, th.applyMessageItemLocked(turnID, *ev.Message, now)...)
	case providers.EventCompact:
		if ev.CompactPhase == providers.CompactPhaseStarted {
			item, alreadyPending := th.pendingContextCompactionItemLocked(turnID, now)
			if alreadyPending {
				return nil
			}
			item = ThreadItem{
				ID:     th.nextItemIDLocked(turnID),
				Type:   ThreadItemContextCompaction,
				Status: ThreadItemStatusInProgress,
				Reason: ev.CompactReason,
			}
			th.upsertItemLocked(turnID, item, now)
			out = append(out, itemStarted(th.ID, turnID, item, now))
			return out
		}
		item, replacesPending := th.pendingContextCompactionItemLocked(turnID, now)
		if !replacesPending {
			item.ID = th.nextItemIDLocked(turnID)
			item.Type = ThreadItemContextCompaction
		}
		item.Status = contextCompactionStatusForContent(ev.Content)
		if ev.CompactPhase == providers.CompactPhaseFailed {
			item.Status = ThreadItemStatusFailed
		}
		item.Text = ev.Content
		item.Reason = ev.CompactReason
		item.Summary = ev.CompactSummary
		th.upsertItemLocked(turnID, item, now)
		if replacesPending {
			out = append(out, itemCompleted(th.ID, turnID, item, now))
		} else {
			out = append(out, itemStarted(th.ID, turnID, item, now), itemCompleted(th.ID, turnID, item, now))
		}
	case providers.EventError:
		if th.completeAfterAnswerReadyCancel && !th.interrupting && currentTurnIsAnswerReadyLocked(th) {
			return nil
		}
		msg := "stream error"
		if ev.Error != nil {
			msg = ev.Error.Error()
		}
		item := ThreadItem{
			ID:     th.nextItemIDLocked(turnID),
			Type:   ThreadItemError,
			Status: ThreadItemStatusFailed,
			Error:  msg,
		}
		th.upsertItemLocked(turnID, item, now)
		out = append(out, itemStarted(th.ID, turnID, item, now), itemCompleted(th.ID, turnID, item, now))
	}
	return out
}

func (th *threadState) pendingContextCompactionItemLocked(turnID string, now time.Time) (ThreadItem, bool) {
	turn := th.ensureTurnLocked(turnID, now)
	for i := len(turn.Items) - 1; i >= 0; i-- {
		item := turn.Items[i]
		if item.Type == ThreadItemContextCompaction && item.Status == ThreadItemStatusInProgress {
			return item, true
		}
	}
	return ThreadItem{}, false
}

func (th *threadState) finishPendingContextCompactionLocked(turnID string, status ThreadItemStatus, text string, now time.Time) {
	item, ok := th.pendingContextCompactionItemLocked(turnID, now)
	if !ok {
		item = ThreadItem{
			ID:   th.nextItemIDLocked(turnID),
			Type: ThreadItemContextCompaction,
		}
	}
	item.Status = status
	item.Text = text
	if strings.TrimSpace(item.Reason) == "" {
		item.Reason = manualContextCompactionReason
	}
	th.upsertItemLocked(turnID, item, now)
}

func contextCompactionStatusForContent(content string) ThreadItemStatus {
	if isCompactFailureNoticeContent(content) {
		return ThreadItemStatusFailed
	}
	return ThreadItemStatusCompleted
}

// pendingStreamReconnectItemLocked finds the turn's in-progress
// stream_reconnect item, mirroring pendingContextCompactionItemLocked.
func (th *threadState) pendingStreamReconnectItemLocked(turnID string, now time.Time) (ThreadItem, bool) {
	turn := th.ensureTurnLocked(turnID, now)
	for i := len(turn.Items) - 1; i >= 0; i-- {
		item := turn.Items[i]
		if item.Type == ThreadItemStreamReconnect && item.Status == ThreadItemStatusInProgress {
			return item, true
		}
	}
	return ThreadItem{}, false
}

// upsertStreamReconnectItemLocked creates or refreshes the turn's reconnect
// row from a reconnecting lifecycle event. The item ID stays stable across
// retries so the renderer updates one row instead of stacking one per
// attempt; each update is re-notified as item/started (the renderer upserts
// on both started and completed).
func (th *threadState) upsertStreamReconnectItemLocked(turnID string, lc *providers.StreamLifecycle, now time.Time) []outboundNotification {
	item, pending := th.pendingStreamReconnectItemLocked(turnID, now)
	if !pending {
		item = ThreadItem{
			ID:     th.nextItemIDLocked(turnID),
			Type:   ThreadItemStreamReconnect,
			Status: ThreadItemStatusInProgress,
		}
	}
	item.Text = lc.Reason
	item.Reason = lc.FailureCategory
	item.RetryCount = lc.RetryCount
	item.MaxRetries = lc.MaxRetries
	if lc.RetryIn > 0 {
		item.RetryAtMS = now.Add(lc.RetryIn).UnixMilli()
	} else {
		item.RetryAtMS = 0
	}
	th.upsertItemLocked(turnID, item, now)
	return []outboundNotification{itemStarted(th.ID, turnID, item, now)}
}

// settleStreamReconnectItemLocked freezes the reconnect row as failed with
// its final retry counters once the provider reports it gave up.
func (th *threadState) settleStreamReconnectItemLocked(turnID string, lc *providers.StreamLifecycle, now time.Time) []outboundNotification {
	item, pending := th.pendingStreamReconnectItemLocked(turnID, now)
	if !pending {
		// No reconnect cycle was reported, so there is no row to settle.
		// Terminal non-retryable failures (replay-unsafe stops, budget
		// limits, …) keep their ordinary turn-level notice instead.
		return nil
	}
	item.Status = ThreadItemStatusFailed
	item.Text = lc.Reason
	item.Reason = lc.FailureCategory
	if lc.RetryCount > 0 {
		item.RetryCount = lc.RetryCount
	}
	if lc.MaxRetries > 0 {
		item.MaxRetries = lc.MaxRetries
	}
	item.RetryAtMS = 0
	th.upsertItemLocked(turnID, item, now)
	if pending {
		return []outboundNotification{itemCompleted(th.ID, turnID, item, now)}
	}
	return []outboundNotification{itemStarted(th.ID, turnID, item, now), itemCompleted(th.ID, turnID, item, now)}
}

func isCompactFailureNoticeContent(content string) bool {
	normalized := strings.TrimSpace(strings.TrimLeft(content, "✦*• \t"))
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(lower, "manual context compaction failed") ||
		strings.HasPrefix(lower, "context compaction failed") ||
		strings.HasPrefix(lower, "proactive compact failed") ||
		strings.HasPrefix(lower, "context-overflow compact failed") ||
		strings.HasPrefix(lower, "fresh context could not be installed") ||
		strings.HasPrefix(lower, "compact failed")
}

func (th *threadState) applyMessageItemLocked(turnID string, msg providers.ChatMessage, now time.Time) []outboundNotification {
	switch msg.Role {
	case "assistant":
		if strings.TrimSpace(msg.Content) == "" && strings.TrimSpace(msg.ReasoningContent) == "" {
			return nil
		}
		var out []outboundNotification
		if strings.TrimSpace(msg.ReasoningContent) != "" && th.activeReasoningItemID == "" && !th.hasReasoningTextLocked(turnID, msg.ReasoningContent) {
			item := ThreadItem{
				ID:       th.nextItemIDLocked(turnID),
				Seq:      msg.Seq,
				SourceID: msg.ProviderItemID,
				Type:     ThreadItemReasoning,
				Status:   ThreadItemStatusCompleted,
				Text:     msg.ReasoningContent,
			}
			th.upsertItemLocked(turnID, item, now)
			out = append(out, itemStarted(th.ID, turnID, item, now), itemCompleted(th.ID, turnID, item, now))
		}
		if strings.TrimSpace(msg.Content) == "" {
			return out
		}
		if th.activeAgentItemID == "" && th.hasAgentTextLocked(turnID, msg.Content) {
			return out
		}
		terminal := assistantMessageTerminal(msg)
		item, started := th.ensureActiveAgentItemLocked(turnID, now)
		if started {
			out = append(out, itemStarted(th.ID, turnID, item, now))
		}
		item.Text = msg.Content
		item.Seq = msg.Seq
		item.SourceID = msg.ProviderItemID
		item.Terminal = terminal
		item.Status = ThreadItemStatusCompleted
		item.FinishReason = string(msg.FinishReason)
		item.StopReason = msg.StopReason
		item.Truncated = msg.Truncated
		th.upsertItemLocked(turnID, item, now)
		if terminal {
			turn := th.ensureTurnLocked(turnID, now)
			if turn.AnswerReadyAt == nil {
				answerReadyAt := now
				turn.AnswerReadyAt = &answerReadyAt
				th.replaceTurnLocked(turn)
			}
		}
		th.activeAgentItemID = ""
		out = append(out, itemCompleted(th.ID, turnID, item, now))
		return out
	case "tool":
		if msg.ToolCallID == "" {
			return nil
		}
		item, ok := th.itemLocked(turnID, th.toolItems[msg.ToolCallID])
		if !ok {
			item = ThreadItem{
				ID:     th.nextItemIDLocked(turnID),
				Type:   ThreadItemToolCall,
				Status: ThreadItemStatusInProgress,
			}
			th.toolItems[msg.ToolCallID] = item.ID
		} else if item.Status == ThreadItemStatusCompleted {
			// EventToolUseEnd already finalized this item with a
			// truncated copy of the same result. Upgrade to the
			// full-fidelity message content, but do not append or
			// announce a second completion.
			if len(msg.Content) > len(item.Result) {
				item.Result = msg.Content
			}
			if msg.ToolResult != nil {
				item.ResultDetail = cloneToolResult(msg.ToolResult)
			}
			th.upsertItemLocked(turnID, item, now)
			return nil
		}
		item.Result += msg.Content
		if msg.ToolResult != nil {
			item.ResultDetail = cloneToolResult(msg.ToolResult)
		}
		item.Status = ThreadItemStatusCompleted
		th.upsertItemLocked(turnID, item, now)
		return []outboundNotification{itemCompleted(th.ID, turnID, item, now)}
	default:
		return nil
	}
}

func (th *threadState) ensureActiveAgentItemLocked(turnID string, now time.Time) (ThreadItem, bool) {
	if th.activeAgentItemID != "" {
		if item, ok := th.itemLocked(turnID, th.activeAgentItemID); ok {
			return item, false
		}
	}
	item := ThreadItem{
		ID:     th.nextItemIDLocked(turnID),
		Type:   ThreadItemAgentMessage,
		Status: ThreadItemStatusInProgress,
		Role:   "assistant",
	}
	th.activeAgentItemID = item.ID
	th.upsertItemLocked(turnID, item, now)
	return item, true
}

func (th *threadState) completeActiveAgentItemLocked(turnID string, now time.Time, terminal bool) []outboundNotification {
	if th.activeAgentItemID == "" {
		return nil
	}
	item, ok := th.itemLocked(turnID, th.activeAgentItemID)
	th.activeAgentItemID = ""
	if !ok {
		return nil
	}
	item.Terminal = terminal
	item.Status = ThreadItemStatusCompleted
	th.upsertItemLocked(turnID, item, now)
	return []outboundNotification{itemCompleted(th.ID, turnID, item, now)}
}

func (th *threadState) ensureActiveReasoningItemLocked(turnID string, now time.Time) (ThreadItem, bool) {
	if th.activeReasoningItemID != "" {
		if item, ok := th.itemLocked(turnID, th.activeReasoningItemID); ok {
			return item, false
		}
	}
	item := ThreadItem{
		ID:     th.nextItemIDLocked(turnID),
		Type:   ThreadItemReasoning,
		Status: ThreadItemStatusInProgress,
	}
	th.activeReasoningItemID = item.ID
	th.upsertItemLocked(turnID, item, now)
	return item, true
}

func (th *threadState) toolItemFromCallLocked(turnID string, call providers.ToolCall, now time.Time) ThreadItem {
	id := strings.TrimSpace(call.ID)
	if id == "" {
		id = fmt.Sprintf("tool-%d", len(th.toolItems)+1)
	}
	if itemID := th.toolItems[id]; itemID != "" {
		if item, ok := th.itemLocked(turnID, itemID); ok {
			if id != "" {
				item.SourceID = id
			}
			if strings.TrimSpace(call.Name) != "" {
				item.Name = call.Name
			}
			if call.Arguments != "" {
				item.Arguments = call.Arguments
			}
			if call.Display != nil {
				item.Display = cloneToolCallDisplay(call.Display)
			}
			th.upsertItemLocked(turnID, item, now)
			return item
		}
	}
	item := ThreadItem{
		ID:        th.nextItemIDLocked(turnID),
		SourceID:  id,
		Type:      ThreadItemToolCall,
		Status:    ThreadItemStatusInProgress,
		Name:      call.Name,
		Arguments: call.Arguments,
		Display:   cloneToolCallDisplay(call.Display),
	}
	th.toolItems[id] = item.ID
	th.upsertItemLocked(turnID, item, now)
	return item
}

func (th *threadState) latestToolItemLocked(turnID string) (ThreadItem, bool) {
	turn := th.ensureTurnLocked(turnID, time.Now())
	for i := len(turn.Items) - 1; i >= 0; i-- {
		if turn.Items[i].Type == ThreadItemToolCall && turn.Items[i].Status == ThreadItemStatusInProgress {
			return turn.Items[i], true
		}
	}
	return ThreadItem{}, false
}

func (th *threadState) hasReasoningTextLocked(turnID, text string) bool {
	turn := th.ensureTurnLocked(turnID, time.Now())
	for _, item := range turn.Items {
		if item.Type == ThreadItemReasoning && item.Text == text {
			return true
		}
	}
	return false
}

func (th *threadState) hasAgentTextLocked(turnID, text string) bool {
	turn := th.ensureTurnLocked(turnID, time.Now())
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage && item.Text == text {
			return true
		}
	}
	return false
}

func (th *threadState) ensureTurnLocked(turnID string, now time.Time) Turn {
	for _, turn := range th.Turns {
		if turn.ID == turnID {
			return turn
		}
	}
	turn := Turn{
		ID:            turnID,
		ModelProvider: th.ModelProvider,
		Model:         th.Model,
		Items:         []ThreadItem{},
		ItemsView:     TurnItemsViewFull,
		Status:        TurnStatusInProgress,
		StartedAt:     &now,
	}
	th.Turns = append(th.Turns, turn)
	return turn
}

func (th *threadState) replaceTurnLocked(turn Turn) {
	for i := range th.Turns {
		if th.Turns[i].ID == turn.ID {
			th.Turns[i] = turn
			return
		}
	}
	th.Turns = append(th.Turns, turn)
}

func (th *threadState) itemLocked(turnID, itemID string) (ThreadItem, bool) {
	if itemID == "" {
		return ThreadItem{}, false
	}
	turn := th.ensureTurnLocked(turnID, time.Now())
	for _, item := range turn.Items {
		if item.ID == itemID {
			return item, true
		}
	}
	return ThreadItem{}, false
}

func (th *threadState) upsertItemLocked(turnID string, item ThreadItem, now time.Time) {
	turn := th.ensureTurnLocked(turnID, now)
	for i := range turn.Items {
		if turn.Items[i].ID == item.ID {
			turn.Items[i] = item
			th.replaceTurnLocked(turn)
			th.UpdatedAt = now
			return
		}
	}
	turn.Items = append(turn.Items, item)
	th.replaceTurnLocked(turn)
	th.UpdatedAt = now
}

func (th *threadState) removeItemLocked(turnID, itemID string, now time.Time) bool {
	turn := th.ensureTurnLocked(turnID, now)
	for i := range turn.Items {
		if turn.Items[i].ID != itemID {
			continue
		}
		turn.Items = append(turn.Items[:i], turn.Items[i+1:]...)
		th.replaceTurnLocked(turn)
		th.UpdatedAt = now
		return true
	}
	return false
}

func (th *threadState) nextItemIDLocked(turnID string) string {
	th.nextItemIndex++
	return fmt.Sprintf("%s-item-%d", turnID, th.nextItemIndex)
}

// streamReconnectItemForPersistLocked returns the turn's latest
// stream_reconnect item in any status, for terminal persistence. The caller
// must hold th.mu; unlike ensureTurnLocked it never creates a turn.
func streamReconnectItemForPersistLocked(th *threadState, turnID string) (ThreadItem, bool) {
	for _, turn := range th.Turns {
		if turn.ID != turnID {
			continue
		}
		for i := len(turn.Items) - 1; i >= 0; i-- {
			if turn.Items[i].Type == ThreadItemStreamReconnect {
				return turn.Items[i], true
			}
		}
	}
	return ThreadItem{}, false
}

func itemStarted(threadID, turnID string, item ThreadItem, at time.Time) outboundNotification {
	return outboundNotification{
		method: NotificationItemStarted,
		params: ItemStartedNotification{
			ThreadID:    threadID,
			TurnID:      turnID,
			Item:        item,
			StartedAtMS: at.UnixMilli(),
		},
	}
}

func itemCompleted(threadID, turnID string, item ThreadItem, at time.Time) outboundNotification {
	return outboundNotification{
		method: NotificationItemCompleted,
		params: ItemCompletedNotification{
			ThreadID:      threadID,
			TurnID:        turnID,
			Item:          item,
			CompletedAtMS: at.UnixMilli(),
		},
	}
}

func itemRemoved(threadID, turnID, itemID string, at time.Time) outboundNotification {
	return outboundNotification{
		method: NotificationItemRemoved,
		params: ItemRemovedNotification{
			ThreadID:    threadID,
			TurnID:      turnID,
			ItemID:      itemID,
			RemovedAtMS: at.UnixMilli(),
		},
	}
}

func turnsFromHistory(threadID string, history []providers.ChatMessage, now time.Time) []Turn {
	return projectHistory(threadID, history, now).Turns
}

type historyItemAddress struct {
	TurnID string
	ItemID string
}

type historyItemOrigin struct {
	Item       ThreadItem
	StartIndex int
	EndIndex   int
	Complete   bool
}

type historyProjection struct {
	Turns       []Turn
	ItemOrigins map[historyItemAddress]historyItemOrigin
	TurnSpans   map[string]historyItemOrigin
}

type projectedToolBatch struct {
	items    []historyItemAddress
	required map[string]struct{}
	seen     map[string]struct{}
}

func (b *projectedToolBatch) markSeen(callID string) bool {
	if b == nil {
		return true
	}
	if _, ok := b.required[callID]; ok {
		b.seen[callID] = struct{}{}
	}
	return len(b.seen) == len(b.required)
}

func projectHistory(threadID string, history []providers.ChatMessage, now time.Time) historyProjection {
	persisted := make([]persistedMessage, 0, len(history))
	for _, msg := range history {
		persisted = append(persisted, persistedMessageFromChatMessage(msg))
	}
	return projectPersistedHistory(threadID, persisted, now, nil)
}

type participantSummaryResolver func(string) (participant.Summary, bool)

func turnsFromPersistedHistory(threadID string, history []persistedMessage, now time.Time, resolve participantSummaryResolver) []Turn {
	return projectPersistedHistory(threadID, history, now, resolve).Turns
}

func projectPersistedHistory(threadID string, history []persistedMessage, now time.Time, resolve participantSummaryResolver) historyProjection {
	_ = resolve
	var turns []Turn
	var current *Turn
	itemIndex := 0
	toolItems := make(map[string]int)
	toolBatches := make(map[string]*projectedToolBatch)
	var pendingCompactions []ThreadItem
	itemOrigins := make(map[historyItemAddress]historyItemOrigin)
	turnSpans := make(map[string]historyItemOrigin)
	turnStartedAt := make(map[string]time.Time)
	nextItemID := func(turnID string) string {
		itemIndex++
		return fmt.Sprintf("%s-item-%d", turnID, itemIndex)
	}
	recordOrigin := func(turnID string, item ThreadItem, historyIndex int, complete bool) {
		if turnID == "" || item.ID == "" || historyIndex < 0 {
			return
		}
		origin := historyItemOrigin{Item: item, StartIndex: historyIndex, EndIndex: historyIndex, Complete: complete}
		itemOrigins[historyItemAddress{TurnID: turnID, ItemID: item.ID}] = origin
		span, ok := turnSpans[turnID]
		if !ok || historyIndex < span.StartIndex {
			span.StartIndex = historyIndex
		}
		if !ok || historyIndex > span.EndIndex {
			span.EndIndex = historyIndex
		}
		span.Complete = true
		turnSpans[turnID] = span
	}
	updateOriginEnd := func(address historyItemAddress, historyIndex int, complete bool) {
		origin, ok := itemOrigins[address]
		if !ok {
			return
		}
		if historyIndex > origin.EndIndex {
			origin.EndIndex = historyIndex
		}
		origin.Complete = complete
		itemOrigins[address] = origin
		span := turnSpans[address.TurnID]
		if historyIndex > span.EndIndex {
			span.EndIndex = historyIndex
		}
		turnSpans[address.TurnID] = span
	}
	appendItem := func(item ThreadItem, historyIndex int, complete bool) {
		if current == nil || item.ID == "" {
			return
		}
		current.Items = append(current.Items, item)
		recordOrigin(current.ID, item, historyIndex, complete)
	}
	for historyIndex, rec := range history {
		if rec.Hidden {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
			if current != nil && rec.Content == "token_usage" {
				setProjectedTurnTiming(current, turnStartedAt[current.ID], rec.At)
			}
			if current != nil && rec.Content == turnTerminalHistoryRecord && (strings.TrimSpace(rec.ClientID) == "" || rec.ClientID == current.ID) {
				status, ok := parseTurnTerminalStatus(rec.StopReason)
				if !ok {
					continue
				}
				at := rec.At
				if at.IsZero() {
					at = now
				}
				sourceID := turnTerminalItemSourceID(rec.ClientID)
				items := current.Items[:0]
				for _, item := range current.Items {
					if item.Type != ThreadItemError || item.SourceID != sourceID {
						items = append(items, item)
					}
				}
				current.Items = items
				current.Status = status
				current.Error = nil
				setProjectedTurnTiming(current, turnStartedAt[current.ID], at)
				if status == TurnStatusCompleted {
					continue
				}
				message := strings.TrimSpace(rec.DisplayContent)
				if message == "" {
					message = "turn start aborted"
				}
				current.Error = &TurnError{Message: message}
				appendItem(ThreadItem{
					ID:       nextItemID(current.ID),
					SourceID: sourceID,
					Type:     ThreadItemError,
					Status:   ThreadItemStatusFailed,
					Error:    message,
				}, -1, true)
			}
			if current != nil && rec.Content == streamReconnectHistoryRecord && (strings.TrimSpace(rec.ClientID) == "" || rec.ClientID == current.ID) {
				sourceID := streamReconnectItemSourceID(rec.ClientID)
				items := current.Items[:0]
				for _, item := range current.Items {
					if item.Type != ThreadItemStreamReconnect || item.SourceID != sourceID {
						items = append(items, item)
					}
				}
				current.Items = items
				appendItem(ThreadItem{
					ID:         nextItemID(current.ID),
					SourceID:   sourceID,
					Type:       ThreadItemStreamReconnect,
					Status:     ThreadItemStatusFailed,
					Text:       strings.TrimSpace(rec.DisplayContent),
					Reason:     rec.Cause,
					RetryCount: rec.RetryCount,
					MaxRetries: rec.MaxRetries,
				}, -1, true)
			}
			continue
		}
		msg := chatMessageFromPersistedMessage(rec)
		if compact.IsInternalContextMessage(msg) {
			continue
		}
		if msg.Role == "system" {
			if compact.IsConversationSummaryContent(msg.Content) {
				pendingCompactions = append(pendingCompactions, ThreadItem{
					Seq:     msg.Seq,
					Type:    ThreadItemContextCompaction,
					Status:  ThreadItemStatusCompleted,
					Text:    "Compacted history",
					Summary: compact.SummaryBodyFromContent(msg.Content),
				})
			}
			continue
		}
		if msg.Role == "user" && !isToolResultMessage(msg) {
			if msg.Steered && current != nil {
				appendItem(chatMessageItem(nextItemID(current.ID), msg), historyIndex, true)
				continue
			}
			turnID := fmt.Sprintf("%s-turn-%04d", threadID, len(turns)+1)
			itemIndex = 0
			toolItems = make(map[string]int)
			toolBatches = make(map[string]*projectedToolBatch)
			turn := Turn{
				ID:        turnID,
				Items:     []ThreadItem{},
				ItemsView: TurnItemsViewFull,
				Status:    TurnStatusCompleted,
			}
			turnStartedAt[turnID] = rec.At
			userItem := chatMessageItem(nextItemID(turnID), msg)
			turn.Items = append(turn.Items, userItem)
			for _, item := range pendingCompactions {
				item.ID = nextItemID(turnID)
				turn.Items = append(turn.Items, item)
			}
			pendingCompactions = nil
			turns = append(turns, turn)
			current = &turns[len(turns)-1]
			recordOrigin(turnID, userItem, historyIndex, true)
			continue
		}
		if current == nil {
			continue
		}
		switch msg.Role {
		case "assistant":
			if strings.TrimSpace(msg.ReasoningContent) != "" {
				appendItem(ThreadItem{
					ID:       nextItemID(current.ID),
					Seq:      msg.Seq,
					SourceID: msg.ProviderItemID,
					Type:     ThreadItemReasoning,
					Status:   ThreadItemStatusCompleted,
					Text:     msg.ReasoningContent,
				}, historyIndex, true)
			}
			if strings.TrimSpace(msg.Content) != "" {
				appendItem(ThreadItem{
					ID:           nextItemID(current.ID),
					Seq:          msg.Seq,
					SourceID:     msg.ProviderItemID,
					Type:         ThreadItemAgentMessage,
					Status:       ThreadItemStatusCompleted,
					Terminal:     assistantMessageTerminal(msg),
					Role:         "assistant",
					Text:         msg.Content,
					FinishReason: string(msg.FinishReason),
					StopReason:   msg.StopReason,
					Truncated:    msg.Truncated,
				}, historyIndex, true)
			}
			if msg.FinishReason != "" || msg.StopReason != "" || msg.Truncated {
				current.FinishReason = string(msg.FinishReason)
				current.StopReason = msg.StopReason
				current.Truncated = msg.Truncated
			}
			batch := &projectedToolBatch{required: make(map[string]struct{}), seen: make(map[string]struct{})}
			for _, call := range msg.ToolCalls {
				item := ThreadItem{
					ID:        nextItemID(current.ID),
					Seq:       msg.Seq,
					SourceID:  call.ID,
					Type:      ThreadItemToolCall,
					Status:    ThreadItemStatusCompleted,
					Name:      call.Name,
					Arguments: call.Arguments,
					Display:   cloneToolCallDisplay(call.Display),
				}
				callID := strings.TrimSpace(call.ID)
				if callID != "" {
					toolItems[call.ID] = len(current.Items)
					batch.required[callID] = struct{}{}
				}
				appendItem(item, historyIndex, callID == "")
				if callID != "" {
					batch.items = append(batch.items, historyItemAddress{TurnID: current.ID, ItemID: item.ID})
				}
			}
			for callID := range batch.required {
				toolBatches[callID] = batch
			}
		case "tool":
			if idx, ok := toolItems[msg.ToolCallID]; ok && idx >= 0 && idx < len(current.Items) {
				current.Items[idx].Result += msg.Content
				if msg.ToolResult != nil {
					current.Items[idx].ResultDetail = cloneToolResult(msg.ToolResult)
				}
				current.Items[idx].Status = ThreadItemStatusCompleted
				batch := toolBatches[msg.ToolCallID]
				complete := batch.markSeen(msg.ToolCallID)
				for _, address := range batch.items {
					updateOriginEnd(address, historyIndex, complete)
				}
				continue
			}
			appendItem(ThreadItem{
				ID:           nextItemID(current.ID),
				Seq:          msg.Seq,
				Type:         ThreadItemToolCall,
				Status:       ThreadItemStatusCompleted,
				Result:       msg.Content,
				ResultDetail: cloneToolResult(msg.ToolResult),
			}, historyIndex, true)
		default:
			item := chatMessageItem(nextItemID(current.ID), msg)
			appendItem(item, historyIndex, true)
		}
	}
	return historyProjection{Turns: turns, ItemOrigins: itemOrigins, TurnSpans: turnSpans}
}

func setProjectedTurnTiming(turn *Turn, startedAt, completedAt time.Time) {
	if turn == nil || completedAt.IsZero() {
		return
	}
	completed := completedAt
	turn.CompletedAt = &completed
	turn.StartedAt = nil
	turn.DurationMS = nil
	if !startedAt.IsZero() && !completed.Before(startedAt) {
		started := startedAt
		turn.StartedAt = &started
		duration := completed.Sub(started).Milliseconds()
		turn.DurationMS = &duration
	}
}

func parseTurnTerminalStatus(raw string) (TurnStatus, bool) {
	switch TurnStatus(strings.ToLower(strings.TrimSpace(raw))) {
	case TurnStatusCompleted:
		return TurnStatusCompleted, true
	case TurnStatusInterrupted:
		return TurnStatusInterrupted, true
	case "", TurnStatusFailed:
		return TurnStatusFailed, true
	default:
		return "", false
	}
}

func turnTerminalItemSourceID(clientID string) string {
	return turnTerminalHistoryRecord + ":" + strings.TrimSpace(clientID)
}

func streamReconnectItemSourceID(clientID string) string {
	return streamReconnectHistoryRecord + ":" + strings.TrimSpace(clientID)
}

func pendingCompactionsContainReason(items []ThreadItem, reason string) bool {
	for _, item := range items {
		if item.Reason == reason {
			return true
		}
	}
	return false
}

func chatMessageItem(id string, msg providers.ChatMessage) ThreadItem {
	switch msg.Role {
	case "user":
		return ThreadItem{
			ID:               id,
			Seq:              msg.Seq,
			SourceID:         msg.ClientID,
			Type:             ThreadItemUserMessage,
			Status:           ThreadItemStatusCompleted,
			Role:             "user",
			Text:             chatMessageDisplayContent(msg),
			ContentParts:     append([]providers.MessageContentPart(nil), msg.ContentParts...),
			InputText:        chatMessageInputText(msg),
			Images:           threadItemImages(msg.Images),
			Files:            threadItemFiles(msg.Files),
			Name:             strings.TrimSpace(msg.Name),
			ReadOnly:         msg.ReadOnly,
			Origin:           strings.TrimSpace(msg.Origin),
			OriginID:         strings.TrimSpace(msg.OriginID),
			Cause:            strings.TrimSpace(msg.Cause),
			PresentationKind: strings.TrimSpace(msg.PresentationKind),
			RelatedSessionID: strings.TrimSpace(msg.RelatedSessionID),
		}
	case "assistant":
		if strings.TrimSpace(msg.Content) != "" {
			return ThreadItem{
				ID:           id,
				Seq:          msg.Seq,
				SourceID:     msg.ProviderItemID,
				Type:         ThreadItemAgentMessage,
				Status:       ThreadItemStatusCompleted,
				Terminal:     assistantMessageTerminal(msg),
				Role:         "assistant",
				Text:         msg.Content,
				FinishReason: string(msg.FinishReason),
				StopReason:   msg.StopReason,
				Truncated:    msg.Truncated,
			}
		}
		if strings.TrimSpace(msg.ReasoningContent) != "" {
			return ThreadItem{
				ID:       id,
				Seq:      msg.Seq,
				SourceID: msg.ProviderItemID,
				Type:     ThreadItemReasoning,
				Status:   ThreadItemStatusCompleted,
				Text:     msg.ReasoningContent,
			}
		}
	case "tool":
		return ThreadItem{
			ID:           id,
			Seq:          msg.Seq,
			SourceID:     msg.ToolCallID,
			Type:         ThreadItemToolCall,
			Status:       ThreadItemStatusCompleted,
			Result:       msg.Content,
			ResultDetail: cloneToolResult(msg.ToolResult),
		}
	}
	return ThreadItem{}
}

func chatMessageFromPersistedMessage(rec persistedMessage) providers.ChatMessage {
	msg := providers.ChatMessage{
		Seq:                  rec.Seq,
		Role:                 strings.ToLower(strings.TrimSpace(rec.Role)),
		Name:                 rec.Name,
		ClientID:             rec.ClientID,
		Content:              rec.Content,
		DisplayContent:       rec.DisplayContent,
		Origin:               rec.Origin,
		OriginID:             rec.OriginID,
		Cause:                rec.Cause,
		PresentationKind:     rec.PresentationKind,
		RelatedSessionID:     rec.RelatedSessionID,
		ReadOnly:             rec.ReadOnly,
		Phase:                providers.NormalizeMessagePhase(rec.Phase),
		Hidden:               rec.Hidden,
		ProviderItemID:       rec.ProviderItemID,
		ProviderItemProvider: rec.Provider,
		ProviderItemModel:    rec.ProviderItemModel,
		Steered:              rec.Steered,
		ReasoningContent:     rec.ReasoningContent,
		ReasoningBlocks:      append([]providers.ReasoningBlock(nil), rec.ReasoningBlocks...),
		ContentParts:         append([]providers.MessageContentPart(nil), rec.ContentParts...),
		ToolCallID:           rec.ToolCallID,
		ToolInvocationID:     rec.ToolInvocationID,
		ToolResultKind:       providers.NormalizeToolCallKind(rec.ToolResultKind),
		ToolResult:           cloneToolResult(rec.ToolResult),
		FinishReason:         providers.FinishReason(strings.TrimSpace(rec.FinishReason)),
		StopReason:           strings.ToLower(strings.TrimSpace(rec.StopReason)),
		Truncated:            rec.Truncated,
		DiscoveredTools:      providers.CloneLoadableToolDefinitions(rec.DiscoveredTools),
	}
	for _, image := range rec.Images {
		if strings.TrimSpace(image.Data) == "" {
			continue
		}
		msg.Images = append(msg.Images, providers.InputImage{
			MediaType: image.MediaType,
			Data:      image.Data,
			Width:     image.Width,
			Height:    image.Height,
		})
	}
	for _, file := range rec.Files {
		if strings.TrimSpace(file.Data) == "" {
			continue
		}
		msg.Files = append(msg.Files, providers.InputFile{
			MediaType: file.MediaType,
			Data:      file.Data,
			Filename:  file.Filename,
		})
	}
	for _, tc := range rec.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
			ID:                   tc.ID,
			ProviderItemID:       tc.ProviderItemID,
			ProviderItemProvider: tc.ProviderItemProvider,
			ProviderItemModel:    tc.ProviderItemModel,
			Name:                 tc.Name,
			Arguments:            tc.Arguments,
			Kind:                 providers.NormalizeToolCallKind(tc.Kind),
			Display:              cloneToolCallDisplay(tc.Display),
		})
	}
	return msg
}

func isToolResultMessage(msg providers.ChatMessage) bool {
	return msg.Role == "tool" || msg.ToolCallID != ""
}

func assistantMessageTerminal(msg providers.ChatMessage) bool {
	if msg.Phase != "" {
		return msg.Phase == providers.MessagePhaseFinalAnswer
	}
	return len(msg.ToolCalls) == 0
}

func threadPreview(history []providers.ChatMessage) string {
	for _, msg := range history {
		if !isThreadTitleUserMessage(msg) {
			continue
		}
		if strings.TrimSpace(chatMessageDisplayContent(msg)) != "" {
			return strings.TrimSpace(chatMessageDisplayContent(msg))
		}
		if len(msg.Images) > 0 {
			if len(msg.Images) == 1 {
				return "[Image #1]"
			}
			return fmt.Sprintf("[%d images]", len(msg.Images))
		}
		if len(msg.Files) > 0 {
			if len(msg.Files) == 1 {
				return filePreview(msg.Files[0], 1)
			}
			return fmt.Sprintf("[%d files]", len(msg.Files))
		}
	}
	return ""
}

func isThreadTitleUserMessage(msg providers.ChatMessage) bool {
	if msg.Hidden || msg.Role != "user" || isToolResultMessage(msg) {
		return false
	}
	if compact.IsInternalContextMessage(msg) {
		return false
	}
	content := chatMessageDisplayContent(msg)
	return !wuucontext.IsSystemReminder(msg.Name, content) &&
		!wuucontext.IsAgentNotification(msg.Name, content) &&
		!wuucontext.IsProcessNotification(msg.Name, content)
}

func chatMessageDisplayContent(msg providers.ChatMessage) string {
	if strings.TrimSpace(msg.DisplayContent) != "" {
		return msg.DisplayContent
	}
	return msg.Content
}

// chatMessageInputText exposes the raw user input only when it differs from
// the displayed bubble text. This keeps the public thread item small for
// ordinary user messages while letting plugin-generated wake messages reveal
// the prompt they actually delivered.
func chatMessageInputText(msg providers.ChatMessage) string {
	content := strings.TrimSpace(msg.Content)
	if content == "" || content == strings.TrimSpace(chatMessageDisplayContent(msg)) {
		return ""
	}
	return msg.Content
}

func threadItemImages(images []providers.InputImage) []ThreadItemImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]ThreadItemImage, 0, len(images))
	for _, image := range images {
		data := strings.TrimSpace(image.Data)
		if data == "" {
			continue
		}
		mediaType := strings.TrimSpace(image.MediaType)
		if mediaType == "" {
			mediaType = "image/png"
		}
		out = append(out, ThreadItemImage{
			MediaType: mediaType,
			Data:      data,
		})
	}
	return out
}

func cloneTurns(turns []Turn) []Turn {
	out := make([]Turn, len(turns))
	for i, turn := range turns {
		out[i] = turn
		out[i].Items = make([]ThreadItem, len(turn.Items))
		for j, item := range turn.Items {
			out[i].Items[j] = cloneThreadItem(item)
		}
	}
	return out
}

func threadItemFiles(files []providers.InputFile) []ThreadItemFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]ThreadItemFile, 0, len(files))
	for _, file := range files {
		data := strings.TrimSpace(file.Data)
		if data == "" {
			continue
		}
		mediaType := strings.TrimSpace(file.MediaType)
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		out = append(out, ThreadItemFile{
			MediaType: mediaType,
			Data:      data,
			Filename:  strings.TrimSpace(file.Filename),
		})
	}
	return out
}

func filePreview(file providers.InputFile, index int) string {
	name := strings.TrimSpace(file.Filename)
	if name != "" {
		return "[" + name + "]"
	}
	mediaType := strings.TrimSpace(file.MediaType)
	if mediaType == "" {
		mediaType = "file"
	}
	return fmt.Sprintf("[File #%d: %s]", index, mediaType)
}

func cloneThreadItem(item ThreadItem) ThreadItem {
	item.Images = append([]ThreadItemImage(nil), item.Images...)
	item.Files = append([]ThreadItemFile(nil), item.Files...)
	item.ContentParts = append([]providers.MessageContentPart(nil), item.ContentParts...)
	item.Display = cloneToolCallDisplay(item.Display)
	item.ResultDetail = cloneToolResult(item.ResultDetail)
	return item
}

func cloneToolResult(result *toolresult.Result) *toolresult.Result {
	if result == nil {
		return nil
	}
	clone := result.Clone()
	return &clone
}

func cloneToolCallDisplay(display *providers.ToolCallDisplay) *providers.ToolCallDisplay {
	return display.Clone()
}
