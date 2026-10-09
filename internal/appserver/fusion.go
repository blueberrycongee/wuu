package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const fusionSideSource = "fusion-side"

type FusionDelegation struct {
	DispatchID          string     `json:"dispatch_id"`
	SessionID           string     `json:"session_id"`
	TurnID              string     `json:"turn_id,omitempty"`
	State               string     `json:"state"`
	Error               *TurnError `json:"error,omitempty"`
	InputTokens         int        `json:"input_tokens,omitempty"`
	OutputTokens        int        `json:"output_tokens,omitempty"`
	CacheCreationTokens int        `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int        `json:"cache_read_tokens,omitempty"`
}

type FusionTurn struct {
	config.FusionSelection
	State       string             `json:"state"`
	SideID      string             `json:"side_id,omitempty"`
	Delegations []FusionDelegation `json:"delegations,omitempty"`
	Tasks       []FusionTaskView   `json:"tasks,omitempty"`
}

func activeFusionPair(metadata session.Session) *config.FusionSelection {
	if metadata.FusionEnabled {
		return metadata.Fusion
	}
	return nil
}

func (s *Server) fusionHandlerForThread(th *threadState) tools.FusionDelegateHandler {
	th.mu.Lock()
	enabled := th.Fusion != nil
	th.mu.Unlock()
	if !enabled {
		return nil
	}
	return s.fusionDelegateHandler(th.ID)
}

func (s *Server) updateThreadFusion(req Request, th *threadState, enabled bool) error {
	th.mu.Lock()
	pair, engine, persist := th.Fusion, th.EngineID, th.PersistHistory
	th.mu.Unlock()
	if !persist || agentengine.NormalizeEngineID(engine) != agentengine.EngineWuu {
		return s.writeResponse(req.ID, nil, errors.New("Fusion requires a persistent Wuu conversation"))
	}
	metadata, found, err := session.Find(s.rt.SessionDir, th.ID)
	if err != nil || !found {
		if err == nil {
			err = session.ErrSessionNotFound
		}
		return s.writeResponse(req.ID, nil, err)
	}
	if metadata.Fusion != nil {
		pair = metadata.Fusion
	}
	if enabled {
		if metadata.Source == projectSource || metadata.Source == projectSessionSource || metadata.Source == fusionSideSource || metadata.Visibility == sessionVisibilityPlugin {
			return s.writeResponse(req.ID, nil, errors.New("Fusion requires an ordinary user conversation"))
		}
		cfg, _, err := s.rt.LoadEffectiveConfig()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if pair == nil {
			selected, err := cfg.FusionPair()
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			pair = &selected
		}
		if err := cfg.ValidateFusionPair(*pair); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	} else {
		if pair == nil {
			return s.writeThreadModelSelectionResponse(req, th)
		}
		if err := s.stopFusionSides(th.ID); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	metadata, err = session.SetFusion(s.rt.SessionDir, th.ID, *pair, enabled)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	selection := runtimeSelectionFromSession(metadata)
	if err := s.updateThreadRuntimeForModelUpdate(th, selection.Provider, selection.Model, selection.Variant, selection.Effort, selection.Speed, selection.PermissionMode, selection.ApproveForMe); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	th.mu.Lock()
	th.Fusion = activeFusionPair(metadata)
	th.Instructions = effectiveSessionInstructions(metadata)
	if enabled && th.execRuntime != nil && th.execRuntime.StreamRunner != nil {
		th.execRuntime.StreamRunner.Tools = s.fusionToolPolicy(metadata, th.execRuntime.StreamRunner.Tools)
	}
	view := th.snapshotLocked()
	th.mu.Unlock()
	if err := s.notifyThreadUpdated(view); err != nil {
		return err
	}
	return s.writeThreadModelSelectionResponse(req, th)
}

func (s *Server) fusionSides(leadID string) ([]session.Session, error) {
	all, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return nil, err
	}
	var sides []session.Session
	for _, item := range all {
		if item.Source == fusionSideSource && item.ParentID == leadID && item.ArchivedAt == nil {
			sides = append(sides, item)
		}
	}
	return sides, nil
}

func (s *Server) fusionActor(leadID, sideID string) (session.Session, session.Control, error) {
	lead, found, err := session.Find(s.rt.SessionDir, leadID)
	if err != nil {
		return lead, session.Control{}, err
	}
	if !found || !lead.FusionEnabled || lead.Fusion == nil || lead.ArchivedAt != nil {
		return lead, session.Control{}, errors.New("Fusion conversation is inactive")
	}
	if sideID == "" {
		return lead, session.Control{}, nil
	}
	side, found, err := session.Find(s.rt.SessionDir, sideID)
	if err != nil {
		return lead, session.Control{}, err
	}
	if !found || side.Source != fusionSideSource || side.ParentID != leadID || side.ArchivedAt != nil {
		return lead, session.Control{}, errors.New("Sidekick is not in this Fusion conversation")
	}
	control, found, err := session.ReadControl(s.rt.SessionDir, sideID)
	if err != nil {
		return lead, control, err
	}
	if !found || control.ManagerID != leadID || control.State != session.ControlActive {
		return lead, control, session.ErrControlChanged
	}
	return lead, control, nil
}

func (s *Server) ensureFusionSide(ctx context.Context, lead session.Session, readOnly bool) (session.Session, session.Control, error) {
	leadID := lead.ID
	permissionMode, approveForMe := lead.PermissionMode, lead.ApproveForMe
	if readOnly {
		permissionMode, approveForMe = config.PermissionModeReadOnly, false
	}
	var err error
	s.sessionCreateMu.Lock()
	defer s.sessionCreateMu.Unlock()
	// Stop cancels the Lead before taking this creation lock. Recheck
	// admission here so a tool queued before Stop cannot create new work.
	if err := ctx.Err(); err != nil {
		return session.Session{}, session.Control{}, err
	}
	lead, _, err = s.fusionActor(leadID, "")
	if err != nil {
		return session.Session{}, session.Control{}, err
	}
	sides, err := s.fusionSides(leadID)
	var side session.Session
	if err == nil && len(sides) > 0 {
		side = sides[0]
	}
	if err == nil && side.ID == "" {
		_, err = s.createHostSessionThread("user", fusionSideSource, "", hostSessionCreateParams{
			RequestID: "fusion-side:" + leadID, Name: "Fusion Sidekick", Visibility: sessionVisibilityUser,
			ParentSessionID: leadID, ContextSource: sessionContextFresh, Workspace: "shared",
			Provider: lead.Fusion.Sidekick.Provider, Model: lead.Fusion.Sidekick.Model, Variant: lead.Fusion.Sidekick.Variant, Effort: lead.Fusion.Sidekick.Effort, PermissionMode: permissionMode,
		})
		if err == nil {
			sides, err = s.fusionSides(leadID)
			if len(sides) > 0 {
				side = sides[0]
			}
		}
	}
	if err != nil {
		return session.Session{}, session.Control{}, err
	}
	if side.ID == "" {
		return session.Session{}, session.Control{}, errors.New("Sidekick is archived; restore it before continuing Fusion")
	}
	control, found, err := session.ReadControl(s.rt.SessionDir, side.ID)
	if err == nil && !found {
		control, err = session.ChangeControl(s.rt.SessionDir, side.ID, leadID, session.ControlActive, 0)
	}
	if err != nil {
		return session.Session{}, session.Control{}, err
	}
	if control.ManagerID != leadID || control.State != session.ControlActive {
		return session.Session{}, session.Control{}, session.ErrControlChanged
	}
	workspaceChanged := side.CWD != lead.CWD || side.WorktreePath != lead.WorktreePath
	permissionsChanged := side.PermissionMode != permissionMode || side.ApproveForMe != approveForMe
	if workspaceChanged || permissionsChanged {
		sideThread, release, err := s.beginThreadRuntimeSelectionMutation(side.ID)
		if err == nil && workspaceChanged {
			side, err = session.UpdateWorkspaceBinding(s.rt.SessionDir, side.ID, lead.CWD, lead.WorktreePath, lead.WorktreeBaseHEAD, lead.WorktreeBaseRepo)
			if err == nil {
				view, enrichErr := s.threadAfterMetadataUpdate(side)
				if enrichErr != nil {
					providers.DebugLogf("enrich synchronized Sidekick %q: %v", side.ID, enrichErr)
				}
				if notifyErr := s.notifyThreadUpdated(view); notifyErr != nil {
					providers.DebugLogf("notify synchronized Sidekick %q: %v", side.ID, notifyErr)
				}
			}
		}
		if err == nil && permissionsChanged {
			err = s.updateThreadRuntimeForModelUpdate(sideThread, side.Provider, side.Model, side.Variant, side.Effort, side.Speed, permissionMode, approveForMe)
		}
		release()
		if err != nil {
			return session.Session{}, session.Control{}, err
		}
	}

	return side, control, nil
}

func (s *Server) fusionDelegateHandler(leadID string) tools.FusionDelegateHandler {
	return func(ctx context.Context, callID string, request tools.FusionDelegateRequest) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := request.Validate(); err != nil {
			return nil, err
		}
		lead, _, err := s.fusionActor(leadID, "")
		if err != nil {
			return nil, err
		}
		action := firstNonEmpty(strings.TrimSpace(request.Action), "delegate")
		if action == "stop" {
			task, err := s.fusionTaskForLead(leadID, request.TaskID)
			if err != nil {
				return nil, err
			}
			if err := s.stopFusionSidesWithReason(leadID, task.ID, task.Revision, request.Reason); err != nil {
				return nil, err
			}
			return s.inspectFusionTask(leadID, task.ID)
		}

		if action == "inspect" {
			return s.inspectFusionTask(leadID, request.TaskID)
		}
		if action == "review" && request.Verdict == "request_changes" {
			rows, err := session.ListFusionDispatches(s.rt.SessionDir, leadID)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if row.ClientID == "fusion:"+leadID+":"+callID && row.TaskID == request.TaskID && row.Kind == "review" {
					return s.inspectFusionTask(leadID, request.TaskID)
				}
			}
		}
		var task session.FusionTask
		if action == "delegate" {
			tasks, err := session.ListFusionTasks(s.rt.SessionDir, leadID)
			if err != nil {
				return nil, err
			}
			taskID := "fusion-task:" + leadID + ":" + callID
			for _, current := range tasks {
				if current.ID != taskID && (current.State == session.FusionTaskQueued || current.State == session.FusionTaskRunning || current.State == session.FusionTaskAwaitingReview) {
					return nil, fmt.Errorf("Fusion task %s is %s; update or review it before delegating another task", current.ID, current.State)
				}
			}
			readOnly := lead.PermissionMode == config.PermissionModeReadOnly || (request.ReadOnly != nil && *request.ReadOnly)
			side, _, err := s.ensureFusionSide(ctx, lead, readOnly)
			if err != nil {
				return nil, err
			}
			th, err := s.ensureThreadLoaded(leadID)
			if err != nil {
				return nil, err
			}
			th.mu.Lock()
			turnID := th.currentTurn
			th.mu.Unlock()
			task = session.FusionTask{ID: taskID, LeadID: leadID, SideID: side.ID, LeadTurnID: turnID, ReadOnly: readOnly}
		} else {
			task, err = s.fusionTaskForLead(leadID, request.TaskID)
			if err != nil {
				return nil, err
			}
			view, err := s.fusionTaskLiveView(task)
			if err != nil {
				return nil, err
			}
			task = view.FusionTask
			if action == "review" {
				if request.Verdict == "accept" && task.State == session.FusionTaskCompleted && task.ReportID == request.ReportID && task.ReportRevision == request.Revision && task.Revision == request.Revision {
					return view, nil
				}
				if task.ReportID != request.ReportID || task.ReportRevision != request.Revision || task.Revision != request.Revision || (task.State != session.FusionTaskAwaitingReview && task.State != session.FusionTaskFailed) {
					return nil, errors.New("Fusion report is stale or not ready for review; inspect the current task")
				}
				if request.Verdict == "accept" {
					if err := session.AcceptFusionReport(s.rt.SessionDir, task.ID, request.ReportID, request.Revision, request.Message); err != nil {
						return nil, err
					}
					s.publishFusionState(leadID)
					return s.inspectFusionTask(leadID, task.ID)
				}
			}
		}
		if action != "wait" {
			if action == "review" {
				if _, _, err := s.ensureFusionSide(ctx, lead, task.ReadOnly); err != nil {
					return nil, err
				}
			}
			_, control, err := s.fusionActor(leadID, task.SideID)
			if err != nil {
				return nil, err
			}
			delivery := "waiting"
			if action == "update" || (request.Block != nil && !*request.Block) {
				delivery = "background"
			}
			dispatch := session.FusionDispatch{ClientID: "fusion:" + leadID + ":" + callID, LeadID: leadID, LeadTurnID: task.LeadTurnID, SideID: task.SideID, TaskID: task.ID, Kind: action, Delivery: delivery}
			expected := task.Revision
			message := request.Message
			if action == "delegate" {
				message = fmt.Sprintf("Shared workspace: %s. Base reference: %s. Task permissions read_only=%t.\n\n%s", lead.CWD, lead.WorktreeBaseHEAD, task.ReadOnly, message)
			}
			task, err = session.AdmitFusionTask(s.rt.SessionDir, task, dispatch, session.InboxMessage{ClientID: dispatch.ClientID, SessionID: task.SideID, RelatedSessionID: leadID, Cause: "fusion", Content: message, Wake: true, Controls: []session.Control{control}}, expected)
			if err != nil {
				return nil, err
			}
		}
		metadata, found, err := session.Find(s.rt.SessionDir, task.SideID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, session.ErrSessionNotFound
		}
		if action == "wait" {
			// Transfer delivery back to the blocking caller before completion
			// can enqueue a background report. Settled receipts stay settled.
			if err := session.SetFusionDelivery(s.rt.SessionDir, task.LatestClientID, "waiting"); err != nil {
				return nil, err
			}
		}
		receipt, err := s.dispatchSessionInput(metadata, task.LatestClientID)
		if err != nil {
			return nil, err
		}
		if receipt.State == "running" {
			if err := session.MarkFusionTaskRunning(s.rt.SessionDir, task.ID, task.Revision); err != nil {
				return nil, err
			}
		}
		s.publishFusionState(leadID)
		if action == "update" || (request.Block != nil && !*request.Block) {
			view, err := s.fusionTaskLiveView(task)
			view.DeliveryState = receipt.DeliveryState
			return view, err
		}
		th, err := s.ensureThreadLoaded(leadID)
		if err != nil {
			return nil, err
		}
		// A handoff normally waits for evidence, cancellation or user steering.
		// Only an explicit timeout should spend another Lead inference on polling.
		result, err := s.waitSessionDispatch(ctx, metadata, tools.ProjectSessionRequest{TimeoutMS: request.TimeoutMS}, task.LatestClientID, 0, func() error {
			_, _, err := s.fusionActor(leadID, task.SideID)
			return err
		}, th.steerWaitInterrupt())
		if err != nil {
			_ = session.SetFusionDelivery(s.rt.SessionDir, task.LatestClientID, "background")
			return nil, err
		}
		waited := result.(managedSessionView)
		view, err := s.fusionTaskLiveView(task)
		if err != nil {
			return nil, err
		}
		view.TimedOut = waited.TimedOut
		if waited.State == "lead_input" {
			view.WaitStatus = "lead_input"
		} else if waited.TimedOut {
			view.WaitStatus = "timed_out"
		} else {
			view.WaitStatus = "ready"
		}
		delivery := "settled"
		if waited.TimedOut || waited.State == "lead_input" || view.Revision != task.Revision {
			delivery = "background"
		}
		if err := session.SetFusionDelivery(s.rt.SessionDir, task.LatestClientID, delivery); err != nil {
			return nil, err
		}
		if delivery == "background" {
			s.deliverFusionResults(leadID)
		}
		if action == "wait" && view.Report != nil {
			// Completion may have won the delivery transfer. The inbox already
			// owns that report, including when its notification is still queued.
			delivered, err := session.InboxHas(s.rt.SessionDir, "fusion-task-result:"+task.ID+":"+view.Report.ID)
			if err != nil {
				return nil, err
			}
			if delivered {
				view.Report.Output = ""
				view.WaitStatus = "report_delivered"
			}
		}
		s.publishFusionState(leadID)
		return view, nil
	}
}

func (s *Server) stopFusionSides(leadID string) error {
	return s.stopFusionSidesWithReason(leadID, "", 0, "Fusion stopped by user control")
}

func (s *Server) stopFusionSidesWithReason(leadID, taskID string, revision int, reason string) error {
	metadata, found, err := session.Find(s.rt.SessionDir, leadID)
	if err != nil {
		return err
	}
	if !found || metadata.Fusion == nil {
		return nil
	}
	s.sessionCreateMu.Lock()
	defer s.sessionCreateMu.Unlock()
	s.controlMu.Lock()
	if taskID != "" {
		stopped, err := session.CancelFusionTask(s.rt.SessionDir, leadID, taskID, revision, reason)
		s.controlMu.Unlock()
		if err != nil {
			return err
		}
		if !stopped {
			return nil
		}
	} else {
		err := session.CancelFusionTasks(s.rt.SessionDir, leadID, reason)
		s.controlMu.Unlock()
		if err != nil {
			return err
		}
	}
	sides, err := s.fusionSides(leadID)
	if err != nil {
		return err
	}
	for _, side := range sides {

		s.revokeSessionInputs(side.ID)
		s.publishSessionControl(side.ID)
		if err := s.interruptOwnedThreadExecution(side.ID); err != nil {
			return err
		}
	}
	s.publishFusionState(leadID)
	return nil
}

func (s *Server) afterFusionTurn(th *threadState, turn Turn, compactOnly bool) {
	if compactOnly {
		return
	}
	th.mu.Lock()
	source, enabled := th.Source, th.Fusion != nil
	th.mu.Unlock()
	if source == fusionSideSource {
		metadata, found, err := session.Find(s.rt.SessionDir, th.ID)
		if err == nil && found {
			s.deliverFusionResults(metadata.ParentID)
			s.publishFusionState(metadata.ParentID)
		}
		s.drainSessionInbox(th.ID)
	} else if enabled && turn.Status != TurnStatusInterrupted {
		s.deliverFusionResults(th.ID)
		s.drainSessionInbox(th.ID)
		s.publishFusionState(th.ID)
	}
}

func (s *Server) fusionDispatchTurn(dispatch session.FusionDispatch) (*Turn, error) {
	turns, err := s.loadDurableSessionTurns(dispatch.SideID)
	if err != nil {
		return nil, err
	}
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage && item.SourceID == dispatch.ClientID {
				return &turn, nil
			}
		}
	}
	return nil, nil
}

func (s *Server) deliverFusionResults(leadID string) {
	tasks, err := session.ListFusionTasks(s.rt.SessionDir, leadID)
	if err != nil {
		providers.DebugLogf("Fusion tasks: %v", err)
		return
	}
	for _, task := range tasks {
		s.deliverFusionTaskResult(task)
	}
	rows, err := session.ListFusionDispatches(s.rt.SessionDir, leadID)
	if err != nil {
		providers.DebugLogf("Fusion receipts: %v", err)
		return
	}
	for _, row := range rows {
		// Historical receipts retain their original delivery contract. New
		// tasks deliver one report for the actual execution, not every update.
		if row.TaskID != "" {
			continue
		}
		if row.Delivery != "background" {
			continue
		}
		turn, err := s.fusionDispatchTurn(row)
		if err != nil || turn == nil || turn.Status == TurnStatusInProgress {
			continue
		}
		if turn.Status == TurnStatusInterrupted || session.ValidateInboxControls(s.rt.SessionDir, row.ClientID) != nil {
			_ = session.SetFusionDelivery(s.rt.SessionDir, row.ClientID, "settled")
			continue
		}
		if _, _, err := s.fusionActor(leadID, row.SideID); err != nil {
			continue
		}
		content := fmt.Sprintf("Fusion Sidekick result for dispatch %s, turn %s (%s). Review the work and evidence before accepting.\n\n%s", row.ClientID, turn.ID, turn.Status, finalAnswerText(*turn))
		if turn.Error != nil {
			content += "\n\nFailure: " + turn.Error.Message
		}
		if err := session.EnqueueFusionResult(s.rt.SessionDir, row.ClientID, content); err != nil {
			if errors.Is(err, session.ErrControlChanged) {
				_ = session.SetFusionDelivery(s.rt.SessionDir, row.ClientID, "settled")
			} else {
				providers.DebugLogf("Fusion result: %v", err)
			}
			continue
		}
		s.drainSessionInbox(leadID)
	}
}

func (s *Server) startFusionRecovery() {
	all, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		providers.DebugLogf("Fusion recovery: %v", err)
		return
	}
	for _, metadata := range all {
		if metadata.FusionEnabled && metadata.ArchivedAt == nil {
			s.startBackground(s.recoverFusionInbox)
			return
		}
	}
}

func (s *Server) recoverFusionInbox() {
	all, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return
	}
	for _, lead := range all {
		if !lead.FusionEnabled || lead.ArchivedAt != nil {
			continue
		}
		root, workspaceID, err := s.sessionWorkspace(lead)
		if err != nil || !s.ownsSessionWorkspace(root, workspaceID) {
			continue
		}
		active, err := session.ThreadExecutionActive(s.rt.SessionDir, lead.ID)
		if err != nil {
			continue
		}
		rows, err := session.ListFusionDispatches(s.rt.SessionDir, lead.ID)
		if err != nil {
			continue
		}
		if !active {
			for _, row := range rows {
				if row.Delivery == "waiting" {
					_ = session.SetFusionDelivery(s.rt.SessionDir, row.ClientID, "background")
				}
			}
		}
		sides, err := s.fusionSides(lead.ID)
		if err != nil {
			continue
		}
		for _, side := range sides {
			if err := s.recoverFusionSide(side.ID, rows); err != nil {
				providers.DebugLogf("Fusion recovery for Sidekick %q: %v", side.ID, err)
			}
		}
		s.deliverFusionResults(lead.ID)
		s.drainSessionInbox(lead.ID)
		for _, side := range sides {
			s.drainSessionInbox(side.ID)
		}
	}
}

func (s *Server) recoverFusionSide(sideID string, rows []session.FusionDispatch) error {
	// Hold execution ownership through the history write. A probe alone could
	// race another host admitting a turn after we observe the Sidekick idle.
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, sideID)
	if err != nil || !acquired {
		return err
	}
	defer lease.Release()
	for _, row := range rows {
		if row.SideID != sideID || row.TaskID == "" {
			continue
		}
		turn, err := s.fusionDispatchTurn(row)
		if err != nil {
			return err
		}
		// Unconsumed inputs remain eligible for ordinary inbox delivery.
		if turn == nil || turn.Status != TurnStatusInProgress {
			continue
		}
		if err := session.AppendHistoryRecord(s.rt.SessionDir, sideID, session.HistoryRecord{
			Role: "meta", Content: turnTerminalHistoryRecord,
			DisplayContent: "Fusion execution interrupted because the previous app server exited; inspect the work before retrying",
			ClientID:       turn.ID, StopReason: string(TurnStatusInterrupted), At: time.Now().UTC(),
			InputTokens: turn.InputTokens, OutputTokens: turn.OutputTokens,
			CacheReadTokens: turn.CacheReadTokens, CacheCreationTokens: turn.CacheCreationTokens,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Projection uses host receipts and exact terminal evidence, never tool JSON.
func (s *Server) fusionTurnStates(leadID string, pair *config.FusionSelection) (map[string]*FusionTurn, error) {
	states := make(map[string]*FusionTurn)
	if pair == nil {
		return states, nil
	}
	rows, err := session.ListFusionDispatches(s.rt.SessionDir, leadID)
	if err != nil {
		return nil, err
	}
	seenTurns := make(map[string]bool)
	for _, row := range rows {
		state := states[row.LeadTurnID]
		if state == nil {
			state = &FusionTurn{FusionSelection: *pair, State: "lead", SideID: row.SideID}
			states[row.LeadTurnID] = state
		}
		delegation := FusionDelegation{DispatchID: row.ClientID, SessionID: row.SideID, State: "queued"}
		turn, err := s.fusionDispatchTurn(row)
		if err != nil {
			return nil, err
		}
		if turn != nil {
			delegation.TurnID = turn.ID
			delegation.State = string(turn.Status)
			delegation.Error = turn.Error
			if !seenTurns[turn.ID] {
				delegation.InputTokens = turn.InputTokens
				delegation.OutputTokens = turn.OutputTokens
				delegation.CacheReadTokens = turn.CacheReadTokens
				delegation.CacheCreationTokens = turn.CacheCreationTokens
				seenTurns[turn.ID] = true
			}
		} else if row.Delivery == "settled" && session.ValidateInboxControls(s.rt.SessionDir, row.ClientID) != nil {
			delegation.State = string(TurnStatusInterrupted)
		}
		state.State = "lead"
		if delegation.State == "queued" || delegation.State == string(TurnStatusInProgress) {
			state.State = "sidekick"
		}
		if delegation.State == string(TurnStatusFailed) {
			state.State = "failed"
		}
		state.Delegations = append(state.Delegations, delegation)
	}
	tasks, err := session.ListFusionTasks(s.rt.SessionDir, leadID)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		view, err := s.fusionTaskView(task)
		if err != nil {
			return nil, err
		}
		state := states[task.LeadTurnID]
		if state == nil {
			state = &FusionTurn{FusionSelection: *pair, State: "lead", SideID: task.SideID}
			states[task.LeadTurnID] = state
		}
		state.Tasks = append(state.Tasks, view)
	}
	return states, nil
}

func (s *Server) publishFusionState(leadID string) {
	th := s.thread(leadID)
	if th == nil {
		return
	}
	metadata, found, err := session.Find(s.rt.SessionDir, leadID)
	if err != nil || !found {
		return
	}
	states, err := s.fusionTurnStates(leadID, metadata.Fusion)
	if err != nil {
		return
	}
	for _, state := range states {
		for index := range state.Tasks {
			if err := s.enrichFusionTaskLive(&state.Tasks[index]); err != nil {
				providers.DebugLogf("Fusion live state: %v", err)
			}
		}
	}
	th.mu.Lock()
	for index := range th.Turns {
		if state := states[th.Turns[index].ID]; state != nil {
			th.Turns[index].Fusion = state
		}
	}
	view := th.snapshotLocked()
	th.mu.Unlock()
	_ = s.notifyThreadUpdated(view)
}
