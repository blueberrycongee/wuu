package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
)

var errSideThreadExecutionBusy = errors.New("side thread is already running")

const sideThreadExecutionLeasePrefix = "side-thread:"

// The third-party model catalog contains many 32,768-token BYOK models. When
// a custom provider publishes no limits, side chat uses half of that common
// floor for input and leaves the other half for output and provider framing.
const unknownSideThreadInputBudget = 16_000

const sideThreadInterruptPollInterval = 100 * time.Millisecond

// A turn that completed in memory must never be lost to a transient store
// failure, so FinishTurn is retried within this bound. Retries stop early
// once the server is closing.
const (
	sideThreadFinishTurnAttempts      = 3
	sideThreadFinishTurnRetryInterval = 200 * time.Millisecond
)

type sideThreadTurn struct {
	cancel      context.CancelFunc
	lease       *session.ThreadExecutionLease
	interrupted atomic.Bool
}

type sideThreadMainSnapshot struct {
	capturedAt  time.Time
	history     []providers.ChatMessage
	running     bool
	provider    string
	model       string
	currentTurn *Turn
}

func (s *Server) handleSideThreadOpen(req Request) error {
	var params SideThreadOpenParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	res, err := s.openSideThread(strings.TrimSpace(params.MainThreadID))
	return s.writeResponse(req.ID, res, err)
}

func (s *Server) handleSideThreadGetHistory(req Request) error {
	var params SideThreadGetHistoryParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	res, err := s.getSideThreadHistory(strings.TrimSpace(params.MainThreadID))
	return s.writeResponse(req.ID, res, err)
}

func (s *Server) openSideThread(mainID string) (*SideThreadOpenResult, error) {
	if mainID == "" {
		return nil, errors.New("main_thread_id is required")
	}
	if s.sideThreadStore == nil {
		return &SideThreadOpenResult{Summary: nil}, nil
	}
	if err := s.requireMainThread(mainID); err != nil {
		return nil, err
	}
	if err := s.recoverStaleSideThread(mainID); err != nil {
		return nil, err
	}
	st, err := s.sideThreadStore.Load(mainID)
	if errors.Is(err, sidethread.ErrNotFound) {
		return &SideThreadOpenResult{Summary: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return &SideThreadOpenResult{Summary: sideThreadWireSummary(st, s.mainTaskSnapshot(mainID))}, nil
}

func (s *Server) getSideThreadHistory(mainID string) (*SideThreadGetHistoryResult, error) {
	if mainID == "" {
		return nil, errors.New("main_thread_id is required")
	}
	if s.sideThreadStore == nil {
		return nil, sidethread.ErrNotFound
	}
	if err := s.requireMainThread(mainID); err != nil {
		return nil, err
	}
	if err := s.recoverStaleSideThread(mainID); err != nil {
		return nil, err
	}
	st, err := s.sideThreadStore.Load(mainID)
	if err != nil {
		return nil, err
	}
	summary := sideThreadWireSummary(st, s.mainTaskSnapshot(mainID))
	return &SideThreadGetHistoryResult{
		Summary:  *summary,
		Messages: sideThreadWireMessages(st.Messages),
	}, nil
}

func sideThreadWireSummary(st *sidethread.SideThread, mainTask *MainTaskSnapshot) *SideThreadWireSummary {
	if st == nil {
		return nil
	}
	out := &SideThreadWireSummary{
		SideThreadID: st.SideThreadID,
		MainThreadID: st.MainThreadID,
		Status:       string(st.Status),
		Revision:     st.Revision,
		CreatedAt:    st.CreatedAt,
		UpdatedAt:    st.UpdatedAt,
	}
	if mainTask != nil {
		out.MainTaskSummary = &SideThreadMainTaskSummary{
			Running:         mainTask.Running,
			LastUserMessage: mainTask.LastUserMessage,
		}
	}
	return out
}

func sideThreadWireMessages(in []sidethread.Message) []SideThreadWireMessage {
	out := make([]SideThreadWireMessage, 0, len(in))
	for _, message := range in {
		out = append(out, sideThreadWireMessage(message))
	}
	return out
}

func sideThreadWireMessage(message sidethread.Message) SideThreadWireMessage {
	items := make([]ThreadItem, 0, len(message.Items))
	for _, item := range message.Items {
		items = append(items, sideThreadWireItem(item))
	}
	return SideThreadWireMessage{
		ID:           message.ID,
		SideThreadID: message.SideThreadID,
		Role:         string(message.Role),
		Text:         message.Text,
		Items:        items,
		Status:       string(message.Status),
		ErrorMessage: message.ErrorText,
		CreatedAt:    message.CreatedAt,
	}
}

func sideThreadWireItem(item sidethread.Item) ThreadItem {
	return ThreadItem{
		ID:           item.ID,
		SourceID:     item.SourceID,
		Type:         ThreadItemType(item.Type),
		Status:       ThreadItemStatus(item.Status),
		Terminal:     item.Terminal,
		Role:         item.Role,
		Text:         item.Text,
		Name:         item.Name,
		Arguments:    item.Arguments,
		Display:      cloneToolCallDisplay(item.Display),
		Result:       item.Result,
		ResultDetail: cloneToolResult(item.ResultDetail),
		Error:        item.Error,
	}
}

func sideThreadStoredItems(items []ThreadItem) []sidethread.Item {
	out := make([]sidethread.Item, 0, len(items))
	for _, item := range items {
		out = append(out, sidethread.Item{
			ID:           item.ID,
			SourceID:     item.SourceID,
			Type:         string(item.Type),
			Status:       string(item.Status),
			Terminal:     item.Terminal,
			Role:         item.Role,
			Text:         item.Text,
			Name:         item.Name,
			Arguments:    item.Arguments,
			Display:      cloneToolCallDisplay(item.Display),
			Result:       item.Result,
			ResultDetail: cloneToolResult(item.ResultDetail),
			Error:        item.Error,
		})
	}
	return out
}

type sideThreadItemProjector struct {
	thread *threadState
	turnID string
}

func newSideThreadItemProjector(sideThreadID, turnID, provider, model, cwd string, now time.Time) *sideThreadItemProjector {
	th := newThreadState(sideThreadID, nil, provider, model, cwd, false, now)
	th.startTurnLocked(turnID, providers.ChatMessage{Role: "user", Content: "side thread prompt"}, now)
	return &sideThreadItemProjector{thread: th, turnID: turnID}
}

func (p *sideThreadItemProjector) apply(event providers.StreamEvent, now time.Time) ([]ThreadItem, bool) {
	if p == nil || p.thread == nil {
		return nil, false
	}
	changed := len(p.thread.applyStreamEventLocked(p.turnID, event, now)) > 0
	return p.items(), changed
}

func (p *sideThreadItemProjector) finish(status TurnStatus, runErr error, now time.Time) []ThreadItem {
	if p == nil || p.thread == nil {
		return nil
	}
	p.thread.finishTurnLocked(p.turnID, status, runErr, now, "", "", false)
	return p.items()
}

func (p *sideThreadItemProjector) items() []ThreadItem {
	if p == nil || p.thread == nil || len(p.thread.Turns) == 0 {
		return nil
	}
	turn := p.thread.Turns[len(p.thread.Turns)-1]
	if len(turn.Items) <= 1 {
		return nil
	}
	out := make([]ThreadItem, 0, len(turn.Items)-1)
	for _, item := range turn.Items[1:] {
		out = append(out, cloneThreadItem(item))
	}
	return out
}

type MainTaskSnapshot struct {
	Running         bool
	LastUserMessage string
}

func (s *Server) mainTaskSnapshot(mainThreadID string) *MainTaskSnapshot {
	if s == nil || s.rt == nil || strings.TrimSpace(mainThreadID) == "" {
		return nil
	}
	snapshot := &MainTaskSnapshot{}
	s.mu.Lock()
	th := s.threads[mainThreadID]
	s.mu.Unlock()
	if th != nil {
		th.mu.Lock()
		snapshot.Running = th.running
		snapshot.LastUserMessage = lastUserMessage(th.History)
		if snapshot.LastUserMessage == "" && len(th.Turns) > 0 {
			snapshot.LastUserMessage = lastUserTurnItem(th.Turns[len(th.Turns)-1])
		}
		th.mu.Unlock()
		return snapshot
	}
	history, err := loadChatMessages(s.rt.SessionDir, mainThreadID)
	if err == nil {
		snapshot.LastUserMessage = lastUserMessage(history)
	}
	return snapshot
}

// mainThreadModelSelection resolves the pinned model selection of the
// conversation a side chat is attached to. It prefers the loaded thread state
// and falls back to the persisted session row, so a side chat opened on an idle
// (not currently loaded) thread still inherits the conversation's model instead of the
// workspace default. An empty selection means "use the workspace defaults",
// which NewSideThreadRunner treats as the plain workspace clone.
func (s *Server) mainThreadModelSelection(mainID string) runtime.ThreadModelSelection {
	if s == nil {
		return runtime.ThreadModelSelection{}
	}
	s.mu.Lock()
	th := s.threads[mainID]
	s.mu.Unlock()
	if th != nil {
		th.mu.Lock()
		sel := runtime.ThreadModelSelection{
			Provider:       strings.TrimSpace(th.ModelProvider),
			Model:          strings.TrimSpace(th.Model),
			Variant:        strings.TrimSpace(th.ModelVariant),
			Effort:         strings.TrimSpace(th.ModelEffort),
			Speed:          th.Speed,
			PermissionMode: strings.TrimSpace(th.PermissionMode),
		}
		th.mu.Unlock()
		if sel.Provider != "" && sel.Model != "" {
			return sel
		}
	}
	if s.rt == nil {
		return runtime.ThreadModelSelection{}
	}
	sess, ok, err := session.Find(s.rt.SessionDir, mainID)
	if err != nil || !ok {
		return runtime.ThreadModelSelection{}
	}
	selection := runtimeSelectionFromSession(sess)
	return runtime.ThreadModelSelection{
		Provider:       selection.Provider,
		Model:          selection.Model,
		Variant:        selection.Variant,
		Effort:         selection.Effort,
		Speed:          selection.Speed,
		PermissionMode: selection.PermissionMode,
	}
}

func (s *Server) mainThreadRoot(mainID string) string {
	if s == nil || s.rt == nil {
		return ""
	}
	s.mu.Lock()
	th := s.threads[mainID]
	s.mu.Unlock()
	if th != nil {
		th.mu.Lock()
		root := strings.TrimSpace(th.CWD)
		th.mu.Unlock()
		if root != "" {
			return root
		}
	}
	if sess, ok, err := session.Find(s.rt.SessionDir, mainID); err == nil && ok {
		if root := strings.TrimSpace(sess.CWD); root != "" {
			return root
		}
	}
	return s.rt.RootDir
}

func lastUserMessage(history []providers.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(history[i].Role), "user") {
			return strings.TrimSpace(firstNonEmpty(history[i].DisplayContent, history[i].Content))
		}
	}
	return ""
}

func lastUserTurnItem(turn Turn) string {
	for i := len(turn.Items) - 1; i >= 0; i-- {
		if turn.Items[i].Type == ThreadItemUserMessage {
			return strings.TrimSpace(turn.Items[i].Text)
		}
	}
	return ""
}

func (s *Server) handleSideThreadSendMessage(req Request) error {
	var params SideThreadSendParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	start := make(chan struct{})
	res, err := s.sendSideThreadMessageWhenReady(strings.TrimSpace(params.MainThreadID), strings.TrimSpace(params.Prompt), start)
	writeErr := s.writeResponse(req.ID, res, err)
	// The response establishes the side-thread identity and running summary in
	// the renderer. Do not let a fast provider publish deltas or a terminal
	// event before that response has been written.
	close(start)
	return writeErr
}

func (s *Server) sendSideThreadMessage(mainID, prompt string) (*SideThreadSendResult, error) {
	start := make(chan struct{})
	close(start)
	return s.sendSideThreadMessageWhenReady(mainID, prompt, start)
}

func (s *Server) sendSideThreadMessageWhenReady(mainID, prompt string, start <-chan struct{}) (*SideThreadSendResult, error) {
	if mainID == "" {
		return nil, errors.New("main_thread_id is required")
	}
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if s == nil || s.rt == nil || s.sideThreadStore == nil {
		return nil, errors.New("side-thread feature unavailable")
	}
	if s.closed.Load() {
		return nil, errServerClosed
	}

	s.sideTurnMu.Lock()
	_, locallyRunning := s.sideTurns[mainID]
	s.sideTurnMu.Unlock()
	if locallyRunning {
		return nil, errSideThreadExecutionBusy
	}

	lease, err := s.acquireSideThreadExecutionLease(mainID)
	if err != nil {
		return nil, err
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			releaseSideThreadExecutionLease(mainID, lease)
		}
	}()

	lifecycleLease, err := s.acquireThreadLifecycleWriteLease(mainID)
	if err != nil {
		return nil, err
	}
	lifecycleLeaseOwned := true
	defer func() {
		if lifecycleLeaseOwned {
			releaseThreadLifecycleWriteLease(mainID, lifecycleLease)
		}
	}()
	snapshot, err := s.snapshotSideThreadMain(mainID)
	if err != nil {
		return nil, err
	}
	userMessageID, err := sidethread.NewMessageID()
	if err != nil {
		return nil, err
	}
	assistantMessageID, err := sidethread.NewMessageID()
	if err != nil {
		return nil, err
	}
	existing, err := s.sideThreadStore.Load(mainID)
	if err != nil && !errors.Is(err, sidethread.ErrNotFound) {
		return nil, err
	}
	sideThreadID := ""
	previewMessages := []sidethread.Message(nil)
	if err == nil {
		sideThreadID = strings.TrimSpace(existing.SideThreadID)
		previewMessages = append(previewMessages, existing.Messages...)
	}
	if sideThreadID == "" {
		sideThreadID, err = sidethread.NewSideThreadID()
		if err != nil {
			return nil, err
		}
	}
	runner, err := s.rt.NewSideThreadRunner(sideThreadID, s.mainThreadRoot(mainID), s.mainThreadModelSelection(mainID))
	if err != nil {
		return nil, err
	}
	previewMessages = append(previewMessages,
		sidethread.Message{ID: userMessageID, SideThreadID: sideThreadID, Role: sidethread.RoleUser, Text: prompt},
		sidethread.Message{ID: assistantMessageID, SideThreadID: sideThreadID, Role: sidethread.RoleAssistant, Status: sidethread.AssistantStreaming},
	)
	history, err := buildSideThreadRunHistory(runner, snapshot, previewMessages, assistantMessageID)
	if err != nil {
		return nil, err
	}
	st, err := s.sideThreadStore.BeginTurnWithSideThreadID(mainID, sideThreadID, prompt, userMessageID, assistantMessageID)
	if err != nil {
		return nil, err
	}
	releaseThreadLifecycleWriteLease(mainID, lifecycleLease)
	lifecycleLeaseOwned = false

	ctx, cancel := context.WithCancel(context.Background())
	turn := &sideThreadTurn{cancel: cancel, lease: lease}
	s.sideTurnMu.Lock()
	if s.closed.Load() {
		s.sideTurnMu.Unlock()
		cancel()
		return nil, s.abortAcceptedSideThread(mainID, userMessageID, assistantMessageID, errServerClosed)
	}
	if _, exists := s.sideTurns[mainID]; exists {
		s.sideTurnMu.Unlock()
		cancel()
		return nil, s.abortAcceptedSideThread(mainID, userMessageID, assistantMessageID, errSideThreadExecutionBusy)
	}
	s.sideTurns[mainID] = turn
	s.sideTurnMu.Unlock()

	if !s.startBackground(func() {
		s.runSideThreadTurn(ctx, start, mainID, st, assistantMessageID, turn, runner, history)
	}) {
		s.sideTurnMu.Lock()
		delete(s.sideTurns, mainID)
		s.sideTurnMu.Unlock()
		turn.cancel()
		return nil, s.abortAcceptedSideThread(mainID, userMessageID, assistantMessageID, errServerClosed)
	}
	leaseOwned = false

	return &SideThreadSendResult{
		UserMessageID: userMessageID,
		Summary:       *sideThreadWireSummary(st, s.mainTaskSnapshot(mainID)),
	}, nil
}

// abortAcceptedSideThread rolls a persisted-but-never-launched turn back out
// of the durable record and surfaces cause to the RPC caller, so the renderer
// runs its send failure path instead of treating the message as delivered.
func (s *Server) abortAcceptedSideThread(mainID, userMessageID, assistantMessageID string, cause error) error {
	rollbackErr := s.sideThreadStore.RollbackTurn(mainID, userMessageID, assistantMessageID)
	if rollbackErr == nil {
		return cause
	}
	// The turn cannot be removed; settle it as an explicit failure so it is
	// never mistaken for a normal answer-less exchange.
	if _, _, finishErr := s.sideThreadStore.FinishTurn(mainID, assistantMessageID, "", sidethread.StatusFailed, cause.Error()); finishErr != nil && !errors.Is(finishErr, sidethread.ErrNotFound) {
		return errors.Join(cause, rollbackErr, finishErr)
	}
	return errors.Join(cause, rollbackErr)
}

func (s *Server) runSideThreadTurn(
	ctx context.Context,
	start <-chan struct{},
	mainID string,
	initial *sidethread.SideThread,
	assistantMessageID string,
	turn *sideThreadTurn,
	runner *agent.StreamRunner,
	history []providers.ChatMessage,
) {
	defer func() {
		turn.cancel()
		s.sideTurnMu.Lock()
		if s.sideTurns[mainID] == turn {
			delete(s.sideTurns, mainID)
		}
		s.sideTurnMu.Unlock()
		releaseSideThreadExecutionLease(mainID, turn.lease)
	}()
	if start != nil {
		select {
		case <-start:
		case <-ctx.Done():
		}
	}

	var (
		result   agent.LoopResult
		runErr   error
		streamed strings.Builder
	)
	projector := newSideThreadItemProjector(
		initial.SideThreadID,
		assistantMessageID,
		runner.ProviderName,
		runner.Model,
		s.mainThreadRoot(mainID),
		time.Now().UTC(),
	)
	if ctx.Err() == nil {
		s.notifySideThreadStatus(initial, s.mainTaskSnapshot(mainID))
		watchDone := make(chan struct{})
		go s.watchSideThreadInterrupt(ctx, mainID, turn, watchDone)
		result, runErr = runner.RunWithCallback(ctx, history, func(event providers.StreamEvent) {
			if turn.interrupted.Load() || ctx.Err() != nil {
				return
			}
			if items, changed := projector.apply(event, time.Now().UTC()); changed {
				s.notifySideThreadEvent(SideThreadEventNotification{
					Type:         "items",
					SideThreadID: initial.SideThreadID,
					MainThreadID: mainID,
					Revision:     initial.Revision,
					MessageID:    assistantMessageID,
					Items:        items,
				})
			}
			if event.Type == providers.EventContentDelta && event.Content != "" {
				streamed.WriteString(event.Content)
				s.notifySideThreadEvent(SideThreadEventNotification{
					Type:         "delta",
					SideThreadID: initial.SideThreadID,
					MainThreadID: mainID,
					Revision:     initial.Revision,
					MessageID:    assistantMessageID,
					TextDelta:    event.Content,
				})
			}
		})
		turn.cancel()
		<-watchDone
	} else {
		runErr = ctx.Err()
	}

	status := sidethread.StatusCompleted
	errorText := ""
	if turn.interrupted.Load() || errors.Is(runErr, context.Canceled) {
		status = sidethread.StatusInterrupted
	} else if runErr != nil {
		status = sidethread.StatusFailed
		errorText = runErr.Error()
	}
	text := strings.TrimSpace(result.Content)
	if status == sidethread.StatusInterrupted || text == "" {
		text = strings.TrimSpace(streamed.String())
	}
	turnStatus := TurnStatusCompleted
	if status == sidethread.StatusInterrupted {
		turnStatus = TurnStatusInterrupted
	} else if status == sidethread.StatusFailed {
		turnStatus = TurnStatusFailed
	}
	items := projector.finish(turnStatus, runErr, time.Now().UTC())
	st, message, finishErr := s.persistSideThreadFinish(mainID, assistantMessageID, text, items, status, errorText)
	if finishErr != nil && !errors.Is(finishErr, sidethread.ErrNotFound) {
		persistFailure := fmt.Sprintf("persist side thread reply: %v", finishErr)
		if errorText != "" {
			persistFailure = errorText + "; " + persistFailure
		}
		errorText = persistFailure
		if status != sidethread.StatusInterrupted {
			// The record must not stay running: lazy recovery would settle it
			// as a normal empty interruption even though this turn completed
			// in memory. An explicit failure is the terminal state recovery
			// leaves alone.
			st, message, finishErr = s.persistSideThreadFinish(mainID, assistantMessageID, text, items, sidethread.StatusFailed, errorText)
		}
	}
	if errors.Is(finishErr, sidethread.ErrNotFound) {
		// The side thread was deleted while the turn ran; there is no record
		// left to settle.
		providers.DebugLogf("finish side thread %q: %v", mainID, finishErr)
		return
	}
	if finishErr != nil {
		s.notifySideThreadFinishFailure(initial, mainID, assistantMessageID, text, errorText)
		return
	}
	s.notifySideThreadEvent(SideThreadEventNotification{
		Type:         "message",
		SideThreadID: st.SideThreadID,
		MainThreadID: mainID,
		Revision:     st.Revision,
		Message:      ptrSideThreadWireMessage(sideThreadWireMessage(message)),
	})
	if st.Status == sidethread.StatusFailed {
		s.notifySideThreadEvent(SideThreadEventNotification{
			Type:         "error",
			SideThreadID: st.SideThreadID,
			MainThreadID: mainID,
			Revision:     st.Revision,
			MessageID:    assistantMessageID,
			ErrorMessage: message.ErrorText,
		})
	}
	s.notifySideThreadStatus(st, s.mainTaskSnapshot(mainID))
}

// persistSideThreadFinish commits the terminal assistant message, retrying
// transient store failures within sideThreadFinishTurnAttempts. Retries stop
// early on server close so shutdown is not delayed, and on ErrNotFound
// because a deleted record never comes back.
func (s *Server) persistSideThreadFinish(mainID, assistantMessageID, text string, items []ThreadItem, status sidethread.Status, errorText string) (*sidethread.SideThread, sidethread.Message, error) {
	for attempt := 1; ; attempt++ {
		st, message, err := s.sideThreadStore.FinishTurnWithItems(mainID, assistantMessageID, text, sideThreadStoredItems(items), status, errorText)
		if err == nil || errors.Is(err, sidethread.ErrNotFound) || attempt >= sideThreadFinishTurnAttempts || s.closed.Load() {
			return st, message, err
		}
		providers.DebugLogf("finish side thread %q (attempt %d/%d): %v", mainID, attempt, sideThreadFinishTurnAttempts, err)
		time.Sleep(sideThreadFinishTurnRetryInterval)
	}
}

// notifySideThreadFinishFailure reports a turn whose terminal state could not
// be persisted. The renderer must stop streaming and show a real failure that
// keeps the in-memory reply, even though the durable record is out of reach.
func (s *Server) notifySideThreadFinishFailure(initial *sidethread.SideThread, mainID, assistantMessageID, text, errorText string) {
	failed := sidethread.Message{
		ID:           assistantMessageID,
		SideThreadID: initial.SideThreadID,
		Role:         sidethread.RoleAssistant,
		Text:         text,
		Status:       sidethread.AssistantFailed,
		ErrorText:    errorText,
		CreatedAt:    time.Now().UTC(),
	}
	s.notifySideThreadEvent(SideThreadEventNotification{
		Type:         "message",
		SideThreadID: initial.SideThreadID,
		MainThreadID: mainID,
		Revision:     initial.Revision,
		Message:      ptrSideThreadWireMessage(sideThreadWireMessage(failed)),
	})
	s.notifySideThreadEvent(SideThreadEventNotification{
		Type:         "error",
		SideThreadID: initial.SideThreadID,
		MainThreadID: mainID,
		Revision:     initial.Revision,
		MessageID:    assistantMessageID,
		ErrorMessage: errorText,
	})
	settled := *initial
	settled.Status = sidethread.StatusFailed
	settled.UpdatedAt = failed.CreatedAt
	s.notifySideThreadStatus(&settled, s.mainTaskSnapshot(mainID))
}

func (s *Server) watchSideThreadInterrupt(ctx context.Context, mainID string, turn *sideThreadTurn, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(sideThreadInterruptPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := s.sideThreadStore.Load(mainID)
			if err != nil && !errors.Is(err, sidethread.ErrNotFound) {
				providers.DebugLogf("observe side thread %q: %v", mainID, err)
				continue
			}
			if errors.Is(err, sidethread.ErrNotFound) || st.Status != sidethread.StatusRunning {
				turn.interrupted.Store(true)
				turn.cancel()
				return
			}
		}
	}
}

func ptrSideThreadWireMessage(message SideThreadWireMessage) *SideThreadWireMessage {
	return &message
}

func (s *Server) handleSideThreadInterrupt(req Request) error {
	var params SideThreadInterruptParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	res, err := s.interruptSideThread(strings.TrimSpace(params.MainThreadID))
	return s.writeResponse(req.ID, res, err)
}

func (s *Server) interruptSideThread(mainID string) (*SideThreadInterruptResult, error) {
	if mainID == "" || s == nil || s.sideThreadStore == nil {
		return &SideThreadInterruptResult{Ok: false}, nil
	}
	s.sideTurnMu.Lock()
	turn := s.sideTurns[mainID]
	if turn != nil {
		turn.interrupted.Store(true)
		turn.cancel()
	}
	s.sideTurnMu.Unlock()
	st, changed, err := s.sideThreadStore.Interrupt(mainID)
	if errors.Is(err, sidethread.ErrNotFound) {
		return &SideThreadInterruptResult{Ok: false}, nil
	}
	if err != nil {
		return nil, err
	}
	if changed {
		s.notifySideThreadStatus(st, s.mainTaskSnapshot(mainID))
	}
	return &SideThreadInterruptResult{Ok: changed || turn != nil}, nil
}

func (s *Server) handleSideThreadReset(req Request) error {
	var params SideThreadResetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	res, err := s.resetSideThread(strings.TrimSpace(params.MainThreadID))
	return s.writeResponse(req.ID, res, err)
}

// resetSideThread drops the side thread's own history. Every send already
// snapshots the live main thread, so the next side message rebases onto the
// latest main history simply because no stale side conversation remains.
func (s *Server) resetSideThread(mainID string) (*SideThreadResetResult, error) {
	if mainID == "" {
		return nil, errors.New("main_thread_id is required")
	}
	if s == nil || s.sideThreadStore == nil {
		return &SideThreadResetResult{Ok: false}, nil
	}
	if err := s.requireMainThread(mainID); err != nil {
		return nil, err
	}
	// Stop an in-flight reply before deleting: the interrupted flag keeps its
	// remaining deltas from re-populating the cleared panel, and its finish
	// settles as ErrNotFound on the removed record.
	s.sideTurnMu.Lock()
	if turn := s.sideTurns[mainID]; turn != nil {
		turn.interrupted.Store(true)
		turn.cancel()
	}
	s.sideTurnMu.Unlock()
	st, err := s.sideThreadStore.Load(mainID)
	if errors.Is(err, sidethread.ErrNotFound) {
		return &SideThreadResetResult{Ok: true}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.sideThreadStore.Delete(mainID); err != nil {
		return nil, err
	}
	s.notifySideThreadEvent(SideThreadEventNotification{
		Type:         "reset",
		SideThreadID: st.SideThreadID,
		MainThreadID: mainID,
	})
	return &SideThreadResetResult{Ok: true}, nil
}

func (s *Server) notifySideThreadStatus(st *sidethread.SideThread, mainTask *MainTaskSnapshot) {
	if st == nil {
		return
	}
	event := SideThreadEventNotification{
		Type:         "status",
		SideThreadID: st.SideThreadID,
		MainThreadID: st.MainThreadID,
		Summary:      sideThreadWireSummary(st, mainTask),
	}
	s.notifySideThreadEvent(event)
}

func (s *Server) notifySideThreadEvent(event SideThreadEventNotification) {
	_ = s.writeNotification(NotificationSideThreadEvent, event)
}

func (s *Server) requireMainThread(mainID string) error {
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return errors.New("session directory is required")
	}
	_, ok, err := session.Find(s.rt.SessionDir, mainID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("main thread %q not found: %w", mainID, session.ErrSessionNotFound)
	}
	return nil
}

func (s *Server) snapshotSideThreadMain(mainID string) (sideThreadMainSnapshot, error) {
	if err := s.requireMainThread(mainID); err != nil {
		return sideThreadMainSnapshot{}, err
	}
	snapshot := sideThreadMainSnapshot{capturedAt: time.Now().UTC()}
	s.mu.Lock()
	th := s.threads[mainID]
	s.mu.Unlock()
	if th == nil {
		history, err := loadChatMessages(s.rt.SessionDir, mainID)
		if err != nil {
			return sideThreadMainSnapshot{}, err
		}
		snapshot.history = history
		snapshot.provider = s.rt.ProviderName
		snapshot.model = s.rt.Model
		return snapshot, nil
	}

	th.mu.Lock()
	snapshot.history = cloneHistory(th.History)
	snapshot.running = th.running
	snapshot.provider = firstNonEmpty(th.runningProviderName, th.ModelProvider)
	snapshot.model = firstNonEmpty(th.runningModel, th.Model)
	if th.running {
		for i := len(th.Turns) - 1; i >= 0; i-- {
			if th.Turns[i].ID == th.currentTurn {
				turns := cloneTurns(th.Turns[i : i+1])
				snapshot.currentTurn = &turns[0]
				break
			}
		}
	}
	th.mu.Unlock()
	return snapshot, nil
}

func buildSideThreadRunHistory(runner *agent.StreamRunner, main sideThreadMainSnapshot, messages []sidethread.Message, activeAssistantID string) ([]providers.ChatMessage, error) {
	if runner == nil {
		return nil, errors.New("side thread runner is required")
	}
	systemPrompt := strings.TrimSpace(runner.SystemPrompt)
	activeUserIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].ID != activeAssistantID && messages[i].Role == sidethread.RoleUser && strings.TrimSpace(messages[i].Text) != "" {
			activeUserIndex = i
			break
		}
	}
	if activeUserIndex < 0 {
		return nil, errors.New("active side thread user message is missing")
	}

	latestMainUserIndex := -1
	for i := len(main.history) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(main.history[i].Role), "user") && strings.TrimSpace(firstNonEmpty(main.history[i].DisplayContent, main.history[i].Content)) != "" {
			latestMainUserIndex = i
			break
		}
	}
	latestMainUser := ""
	if latestMainUserIndex >= 0 {
		latestMainUser = "## Latest main-task user message\n" + strings.TrimSpace(firstNonEmpty(main.history[latestMainUserIndex].DisplayContent, main.history[latestMainUserIndex].Content))
	} else if main.currentTurn != nil {
		latestMainUser = "## Latest main-task user message\n" + lastUserTurnItem(*main.currentTurn)
	}
	liveSnapshot := renderSideThreadLiveSnapshot(main)
	activeUser := strings.TrimSpace(messages[activeUserIndex].Text)

	inputBudget := sideThreadInputBudget(runner)
	contextHeading := "# Main task context"
	fixedHistory := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Name: "main-task-snapshot", Content: contextHeading + "\n\n"},
		{Role: "user", Name: "side-thread:" + messages[activeUserIndex].SideThreadID},
	}
	contentBudget := inputBudget - contextbudget.EstimateMessagesTokens(fixedHistory)
	separatorTokens := contextbudget.EstimateTokens("\n\n")
	if latestMainUser != "" {
		contentBudget -= separatorTokens
	}
	if liveSnapshot != "" {
		contentBudget -= separatorTokens
	}
	if contentBudget <= 0 {
		return nil, fmt.Errorf("side thread model input budget %d is too small for its system prompt", inputBudget)
	}
	activeUserTokens := contextbudget.EstimateTokens(activeUser)
	if activeUserTokens > contentBudget {
		return nil, fmt.Errorf("side thread prompt requires %d tokens but only %d fit in the model input budget", activeUserTokens, contentBudget)
	}
	allocations := fairTokenAllocations([]string{latestMainUser, liveSnapshot}, contentBudget-activeUserTokens)
	latestMainUser = truncateToTokenBudget(latestMainUser, allocations[0])
	liveSnapshot = truncateToTokenBudget(liveSnapshot, allocations[1])

	used := activeUserTokens + contextbudget.EstimateTokens(latestMainUser) + contextbudget.EstimateTokens(liveSnapshot)
	remaining := max(0, contentBudget-used)
	mainEntries := sideThreadMainHistoryEntries(main.history, latestMainUserIndex)
	sideEntries := sideThreadPriorHistoryEntries(messages, activeAssistantID, activeUserIndex)
	sideHeadingTokens := contextbudget.EstimateTokens("## Earlier side conversation\n\n")
	sideBudget := max(0, remaining-sideHeadingTokens)
	selectedSide, sideRemaining := selectRecentTextEntries(sideEntries, sideBudget)
	if len(selectedSide) > 0 {
		remaining = sideRemaining
	}
	selectedMain, _ := selectRecentTextEntries(mainEntries, remaining)

	var contextMessage strings.Builder
	contextMessage.WriteString(contextHeading + "\n\n")
	for _, entry := range selectedMain {
		contextMessage.WriteString(entry)
		contextMessage.WriteString("\n\n")
	}
	if latestMainUser != "" {
		contextMessage.WriteString(latestMainUser)
		contextMessage.WriteString("\n\n")
	}
	if liveSnapshot != "" {
		contextMessage.WriteString(liveSnapshot)
		contextMessage.WriteString("\n\n")
	}
	if len(selectedSide) > 0 {
		contextMessage.WriteString("## Earlier side conversation\n\n")
		for _, entry := range selectedSide {
			contextMessage.WriteString(entry)
			contextMessage.WriteString("\n\n")
		}
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Name: "main-task-snapshot", Content: strings.TrimSpace(contextMessage.String())},
		{Role: "user", Name: "side-thread:" + messages[activeUserIndex].SideThreadID, Content: activeUser},
	}
	if estimated := contextbudget.EstimateMessagesTokens(history); estimated > inputBudget {
		return nil, fmt.Errorf("side thread context estimate %d exceeds model input budget %d", estimated, inputBudget)
	}
	return history, nil
}

func sideThreadInputBudget(runner *agent.StreamRunner) int {
	if runner == nil {
		return unknownSideThreadInputBudget
	}
	var candidates []int
	if runner.MaxInputTokens > 0 {
		candidates = append(candidates, runner.MaxInputTokens)
	}
	if runner.CompactThresholdTokens > 0 {
		candidates = append(candidates, runner.CompactThresholdTokens)
	}
	if runner.ContextWindowOverride > 0 {
		usable := runner.ContextWindowOverride - max(0, runner.OutputReserveTokens)
		if usable > 0 {
			candidates = append(candidates, usable)
		}
	}
	if len(candidates) == 0 {
		return unknownSideThreadInputBudget
	}
	budget := candidates[0]
	for _, candidate := range candidates[1:] {
		budget = min(budget, candidate)
	}
	return budget
}

func fairTokenAllocations(texts []string, budget int) []int {
	allocations := make([]int, len(texts))
	demand := make([]int, len(texts))
	pending := make([]int, 0, len(texts))
	for i, text := range texts {
		if tokens := contextbudget.EstimateTokens(text); tokens > 0 {
			demand[i] = tokens
			pending = append(pending, i)
		}
	}
	remaining := max(0, budget)
	for len(pending) > 0 && remaining > 0 {
		share := remaining / len(pending)
		if share == 0 {
			for _, index := range pending {
				if remaining == 0 {
					break
				}
				allocations[index]++
				remaining--
			}
			break
		}
		settled := false
		unsettled := pending[:0]
		for _, index := range pending {
			if demand[index] > share {
				unsettled = append(unsettled, index)
				continue
			}
			allocations[index] = demand[index]
			remaining -= demand[index]
			settled = true
		}
		pending = unsettled
		if settled {
			continue
		}
		for _, index := range pending {
			allocations[index] = share
			remaining -= share
		}
		for _, index := range pending {
			if remaining == 0 {
				break
			}
			allocations[index]++
			remaining--
		}
		break
	}
	return allocations
}

func truncateToTokenBudget(text string, budget int) string {
	text = strings.TrimSpace(text)
	if text == "" || budget <= 0 {
		return ""
	}
	if contextbudget.EstimateTokens(text) <= budget {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if contextbudget.EstimateTokens(string(runes[:mid])) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:low]))
}

func selectRecentTextEntries(entries []string, budget int) ([]string, int) {
	if budget <= 0 || len(entries) == 0 {
		return nil, max(0, budget)
	}
	selected := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0 && budget > 0; i-- {
		entry := strings.TrimSpace(entries[i])
		if entry == "" {
			continue
		}
		tokens := contextbudget.EstimateTokens(entry + "\n\n")
		if tokens > budget {
			contentBudget := budget - contextbudget.EstimateTokens("\n\n")
			entry = truncateToTokenBudget(entry, contentBudget)
			tokens = contextbudget.EstimateTokens(entry + "\n\n")
		}
		if entry == "" {
			break
		}
		selected = append(selected, entry)
		budget -= tokens
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected, max(0, budget)
}

func sideThreadMainHistoryEntries(history []providers.ChatMessage, skipIndex int) []string {
	entries := make([]string, 0, len(history))
	for i, message := range history {
		if i == skipIndex {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(firstNonEmpty(message.DisplayContent, message.Content))
		if content == "" || (role == "system" && !compact.IsConversationSummaryContent(content)) {
			continue
		}
		switch role {
		case "user", "assistant", "tool", "system":
			entries = append(entries, fmt.Sprintf("[%s]\n%s", strings.ToUpper(role), content))
		}
	}
	return entries
}

func sideThreadPriorHistoryEntries(messages []sidethread.Message, activeAssistantID string, activeUserIndex int) []string {
	entries := make([]string, 0, len(messages))
	for i, message := range messages {
		if i == activeUserIndex || message.ID == activeAssistantID || strings.TrimSpace(message.Text) == "" {
			continue
		}
		if message.Role != sidethread.RoleUser && message.Role != sidethread.RoleAssistant {
			continue
		}
		entries = append(entries, fmt.Sprintf("[%s]\n%s", strings.ToUpper(string(message.Role)), strings.TrimSpace(message.Text)))
	}
	return entries
}

func renderSideThreadLiveSnapshot(snapshot sideThreadMainSnapshot) string {
	var body strings.Builder
	body.WriteString("## Live main-task state\n")
	fmt.Fprintf(&body, "Captured at: %s\n", snapshot.capturedAt.Format(time.RFC3339Nano))
	if snapshot.running {
		body.WriteString("State: running\n")
	} else {
		body.WriteString("State: idle\n")
	}
	if snapshot.provider != "" || snapshot.model != "" {
		fmt.Fprintf(&body, "Provider/model: %s/%s\n", snapshot.provider, snapshot.model)
	}
	if snapshot.currentTurn != nil {
		fmt.Fprintf(&body, "Current turn status: %s\n", snapshot.currentTurn.Status)
		body.WriteString("Most recent items first:\n")
		for i := len(snapshot.currentTurn.Items) - 1; i >= 0; i-- {
			renderSideThreadLiveItem(&body, snapshot.currentTurn.Items[i])
		}
	}
	return body.String()
}

func renderSideThreadLiveItem(body *strings.Builder, item ThreadItem) {
	if body == nil {
		return
	}
	label := string(item.Type)
	if item.Name != "" {
		label += " " + item.Name
	}
	fmt.Fprintf(body, "\n[%s; %s]\n", label, item.Status)
	for _, text := range []string{item.Text, item.Arguments, item.Result, item.Error, item.Reason} {
		if strings.TrimSpace(text) != "" {
			body.WriteString(strings.TrimSpace(text))
			body.WriteByte('\n')
		}
	}
}

func (s *Server) acquireSideThreadExecutionLease(mainID string) (*session.ThreadExecutionLease, error) {
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return nil, errors.New("session directory is required for side thread execution")
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, sideThreadExecutionLeasePrefix+mainID)
	if err != nil {
		return nil, fmt.Errorf("acquire side thread execution lease for %q: %w", mainID, err)
	}
	if !acquired {
		return nil, errSideThreadExecutionBusy
	}
	return lease, nil
}

func releaseSideThreadExecutionLease(mainID string, lease *session.ThreadExecutionLease) {
	if lease == nil {
		return
	}
	if err := lease.Release(); err != nil {
		providers.DebugLogf("release side thread execution lease for %q: %v", mainID, err)
	}
}

func (s *Server) recoverSideThreadsOnBoot() {
	if s == nil || s.sideThreadStore == nil {
		return
	}
	recovered, err := s.sideThreadStore.RecoverRunning()
	if err != nil {
		providers.DebugLogf("recover side threads: %v", err)
	}
	if recovered > 0 {
		providers.DebugLogf("recovered %d interrupted side thread(s)", recovered)
	}
}

// recoverStaleSideThread settles a running record only when no process owns
// its execution lease. This lazy check covers an owner crash while another
// app-server remains alive, a case where workspace boot recovery does not run.
func (s *Server) recoverStaleSideThread(mainID string) error {
	st, err := s.sideThreadStore.Load(mainID)
	if errors.Is(err, sidethread.ErrNotFound) || (err == nil && st.Status != sidethread.StatusRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, sideThreadExecutionLeasePrefix+mainID)
	if err != nil {
		return fmt.Errorf("inspect side thread execution lease for %q: %w", mainID, err)
	}
	if !acquired {
		return nil
	}
	defer releaseSideThreadExecutionLease(mainID, lease)
	_, _, err = s.sideThreadStore.Interrupt(mainID)
	if errors.Is(err, sidethread.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Server) cancelSideThreads() {
	if s == nil {
		return
	}
	s.sideTurnMu.Lock()
	turns := make(map[string]*sideThreadTurn, len(s.sideTurns))
	for mainID, turn := range s.sideTurns {
		turn.interrupted.Store(true)
		turn.cancel()
		turns[mainID] = turn
	}
	s.sideTurnMu.Unlock()
	for mainID := range turns {
		_, _, _ = s.sideThreadStore.Interrupt(mainID)
	}
}
