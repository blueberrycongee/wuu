package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const driverExecutionHistoryRecord = "turn_execution"
const driverCancellationHistoryRecord = "turn_execution_cancel"
const durableRecoveryCapability = "durable-recovery"

type driverRecoveryAdmission struct {
	Binding        loopdriver.Binding `json:"binding"`
	CheckpointHash string             `json:"checkpoint_hash"`
	SourceTurnID   string             `json:"source_turn_id"`
}

func checkpointRecoveryHash(record session.DriverCheckpointRecord) string {
	raw, _ := json.Marshal(record)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// This is a host-authored admission fact in the existing conversation archive,
// not a second execution ledger. Drivers must explicitly opt in.
func recordDriverRecoveryAdmission(ctx context.Context, th *threadState, turnID string, binding loopdriver.Binding, descriptor loopdriver.Descriptor) error {
	if !th.PersistHistory || !slices.Contains(descriptor.Capabilities, durableRecoveryCapability) {
		return nil
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.interrupting {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	if err := th.state.Sessions.WithContext(ctx).AppendHistoryRecord(th.ID, session.HistoryRecord{Role: "meta", Content: driverExecutionHistoryRecord, ClientID: turnID, Cause: string(encoded), At: time.Now().UTC()}); err != nil {
		return err
	}
	th.driverRecoveryTurn = turnID
	return nil
}

// Called with th.mu held, before acknowledging business cancellation. A
// cancellation intent is not a terminal result: cleanup may still be running.
func recordDriverRecoveryCancellationLocked(th *threadState, turnID string) error {
	if !th.PersistHistory || th.driverRecoveryTurn != turnID || th.interrupting {
		return nil
	}
	return th.state.Sessions.AppendHistoryRecord(th.ID, session.HistoryRecord{Role: "meta", Content: driverCancellationHistoryRecord, ClientID: turnID, At: time.Now().UTC()})
}

func selectedRecoveryDriver(binding *runtime.State, pluginID, id string) bool {
	selected, found, err := binding.Sessions.LoadDriverBinding(id)
	return err == nil && found && selected.ProviderID == pluginID
}

// The token is only a compare-and-swap fence, never authority. Admission repeats
// every owner, policy, generation and checkpoint check while holding the lease.
func (s *Server) driverRecoveryCandidate(binding *runtime.State, pluginID, id, turnID string, leaseHeld bool) (*pluginhost.SessionDriverRecovery, error) {
	selected, found, err := binding.Sessions.LoadDriverBinding(id)
	if err != nil || !found {
		return nil, err
	}
	if selected.ProviderID != pluginID {
		return nil, nil
	}
	metadata, found, err := binding.Sessions.Find(id)
	if err != nil || !found {
		return nil, err
	}
	if metadata.ArchivedAt != nil || retiredSessionSource(metadata.Source) {
		return nil, nil
	}
	if err := s.requireUserSessionBinding(metadata); err != nil {
		return nil, err
	}
	if !leaseHeld {
		active, err := session.ThreadExecutionActive(s.rt.SessionDir, id)
		if err != nil || active {
			return nil, err
		}
	}
	currentControl, managed, err := binding.Sessions.ReadControl(id)
	if err != nil {
		return nil, err
	}
	var control *session.Control
	if managed && currentControl.State != session.ControlReleased {
		if currentControl.ManagerID != "plugin:"+pluginID || currentControl.State != session.ControlActive {
			return nil, nil
		}
		control = &currentControl
	}
	checkpoint, found, err := binding.Sessions.LoadDriverCheckpoint(id)
	if err != nil || !found {
		return nil, err
	}
	if checkpoint.DriverID != selected.DriverID || checkpoint.DriverVersion != selected.DriverVersion || checkpoint.ContractVersion != selected.ContractVersion {
		return nil, nil
	}
	history, err := binding.Sessions.LoadActiveHistoryRecords(id, true)
	if err != nil {
		return nil, err
	}
	var admission *session.HistoryRecord
	var terminal *session.HistoryRecord
	var continuation *driverRecoveryAdmission
	canceled := false
	admittedTurns := make(map[string]bool)
	for i := range history {
		rec := &history[i]
		if rec.Role == "meta" && rec.Content == driverExecutionHistoryRecord {
			admission = rec
			terminal = nil
			continuation = nil
			canceled = false
			clear(admittedTurns)
			var markerBinding loopdriver.Binding
			if json.Unmarshal([]byte(rec.Cause), &markerBinding) == nil && markerBinding.CanUpgradeTo(selected) {
				admittedTurns[rec.ClientID] = true
			}
		}
		if rec.Role == "meta" && rec.Content == turnContinueHistoryRecord {
			var proof driverRecoveryAdmission
			if json.Unmarshal([]byte(rec.Cause), &proof) == nil && proof.Binding.CanUpgradeTo(selected) && proof.CheckpointHash != "" && !canceled && admittedTurns[proof.SourceTurnID] {
				admission = rec
				terminal = nil
				continuation = &proof
				canceled = false
				admittedTurns[rec.ClientID] = true
			}
		}
		if rec.Role == "meta" && rec.Content == driverCancellationHistoryRecord && admission != nil && rec.ClientID == admission.ClientID {
			canceled = true
		}
		if rec.Role == "meta" && rec.Content == turnTerminalHistoryRecord && admission != nil {
			terminal = rec
		}
	}
	if canceled || admission == nil || admission.ClientID == "" || turnID != "" && !admittedTurns[turnID] {
		return nil, nil
	}
	var admitted loopdriver.Binding
	if continuation != nil {
		admitted = continuation.Binding
	} else if json.Unmarshal([]byte(admission.Cause), &admitted) != nil {
		return nil, nil
	}
	if !admitted.CanUpgradeTo(selected) {
		return nil, nil
	}
	if checkpoint.UpdatedAt.Before(admission.At) && (continuation == nil || continuation.CheckpointHash != checkpointRecoveryHash(checkpoint)) {
		return nil, nil
	}
	if terminal != nil {
		var cause TurnError
		if terminal.ClientID != admission.ClientID || json.Unmarshal([]byte(terminal.Cause), &cause) != nil || (cause.Interruption != "shutdown" && cause.Interruption != "process_lost") {
			return nil, nil
		}
	}
	// New input, an edit, or any newer physical turn invalidates an old proof,
	// even when that turn has not reached its driver yet.
	persisted, err := loadPersistedMessages(binding.Sessions, id, true)
	if err != nil {
		return nil, err
	}
	projected := turnsFromPersistedHistory(id, persisted, time.Now().UTC(), s.participantResolver(binding))
	if len(projected) == 0 || projected[len(projected)-1].ID != admission.ClientID {
		return nil, nil
	}
	raw, _ := json.Marshal([]any{selected, checkpoint, control, history[len(history)-1].Seq, admission.Seq})
	token := fmt.Sprintf("%x", sha256.Sum256(raw))
	sourceTurn := admission.ClientID
	if continuation != nil {
		sourceTurn = continuation.SourceTurnID
	}
	revision := int64(0)
	if control != nil {
		revision = control.Revision
	}
	return &pluginhost.SessionDriverRecovery{TurnID: admission.ClientID, CheckpointToken: token, ControlRevision: revision, SourceTurnID: sourceTurn}, nil
}

func (s *Server) recoverPluginSession(ctx context.Context, binding *runtime.State, pluginID string, params pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
	if params.RecoverAfterTurnID == "" || params.RecoveryCheckpointToken == "" || params.RequestID == "" || len(params.RequestID) > pluginhost.MaxSessionSendRequestIDBytes || strings.TrimSpace(params.Input.Prompt) != "" || len(params.Input.Attachments) > 0 || len(params.Input.ContextBlocks) > 0 || params.IfRunning != "" || params.ReplyToTurnID != "" || params.ResumeAccepted || params.ResumeReceiptID != "" || params.UserInputRequestID != "" || params.ToolPolicy != nil || params.DisableTools || params.Presentation != nil || params.Relation != nil || params.Cause != "" {
		return pluginhost.SessionSendResult{}, errors.New("driver recovery requires an exact turn and checkpoint token without input or overrides")
	}
	if _, delegated, scopeErr := executionRunScope(ctx, pluginID, params.SessionID); scopeErr != nil || delegated {
		return pluginhost.SessionSendResult{}, errors.New("delegated execution cannot recover another physical turn")
	}
	if !selectedRecoveryDriver(binding, pluginID, params.SessionID) {
		return pluginhost.SessionSendResult{}, errors.New("driver recovery caller does not own the selected driver")
	}
	candidate, err := s.driverRecoveryCandidate(binding, pluginID, params.SessionID, params.RecoverAfterTurnID, false)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if candidate == nil || candidate.CheckpointToken != params.RecoveryCheckpointToken {
		return pluginhost.SessionSendResult{}, errors.New("driver recovery proof is stale or unavailable")
	}
	th, err := s.ensureThreadLoaded(binding, params.SessionID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	control, err := s.pluginSessionControl(binding, pluginID, params.SessionID, params.ControlRevision)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	invocation, err := runtime.RetainInvocationState(ctx)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	defer invocation.Close()
	origin, err := invocation.RetainExecution()
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
	snapshot.AdmissionPrincipal, _ = principal.FromContext(ctx)
	snapshot.AdmissionDelegation = access.DelegationFromContext(ctx)
	snapshot.AdmissionAuthorization = runtime.CaptureExecutionAuthorization(ctx)
	snapshot.ResumeAfterTurnID = params.RecoverAfterTurnID
	snapshot.DriverRecoveryToken = params.RecoveryCheckpointToken
	snapshot.DriverRecoveryCaller = pluginID
	selected, _, err := binding.Sessions.LoadDriverBinding(params.SessionID)
	if err != nil {
		_ = origin.Close()
		return pluginhost.SessionSendResult{}, err
	}
	cp, _, err := binding.Sessions.LoadDriverCheckpoint(params.SessionID)
	if err != nil {
		_ = origin.Close()
		return pluginhost.SessionSendResult{}, err
	}
	recoveryProof, _ := json.Marshal(driverRecoveryAdmission{Binding: selected, CheckpointHash: checkpointRecoveryHash(cp), SourceTurnID: candidate.SourceTurnID})
	snapshot.DriverRecoveryRecord = string(recoveryProof)
	baseline, _, err := binding.Sessions.ReadControl(params.SessionID)
	if err != nil {
		_ = origin.Close()
		return pluginhost.SessionSendResult{}, err
	}
	baseline.SessionID = params.SessionID
	snapshot.ExecutionControlBaseline = &baseline
	snapshot.Control = control
	snapshot.PluginTurn = &pluginTurnReference{PluginID: pluginID, RequestID: params.RequestID, Origin: origin}
	started, ok, err := s.startSubmittedSessionTurn(ctx, th, providers.ChatMessage{}, snapshot)
	if err != nil || !ok {
		_ = origin.Close()
		if err == nil {
			err = errors.New("driver recovery admission is busy")
		}
		return pluginhost.SessionSendResult{}, err
	}
	return pluginhost.SessionSendResult{State: pluginhost.TurnLifecycleRunning, SessionID: th.ID, TurnID: started.turnID}, nil
}
