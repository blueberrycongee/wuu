package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/access"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func clientInputFingerprint(params pluginhost.SessionSendParams) string {
	value := struct {
		Input        pluginhost.SessionInput
		Relation     *providers.MessageRelation
		Cause        string
		DisableTools bool
		Presentation *pluginhost.SessionInputPresentation
	}{params.Input, params.Relation, params.Cause, params.DisableTools, params.Presentation}
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Server) ensureClientInputOwner(binding *runtime.State) (string, error) {
	s.clientInputMu.Lock()
	defer s.clientInputMu.Unlock()
	if s.closed.Load() {
		return "", errServerClosed
	}
	if s.clientInputLease == nil {
		owner := "client-input-owner-" + session.NewID()
		lease, acquired, err := binding.Sessions.TryAcquireThreadExecutionLease(s.rt.SessionDir, owner)
		if err != nil {
			return "", err
		}
		if !acquired {
			return "", errors.New("client input ownership unavailable")
		}
		s.clientInputLease = lease
		s.clientInputOwner = owner
	}
	return s.clientInputOwner, nil
}

func (s *Server) loadHeldClientInputs(binding *runtime.State, threadID string) ([]queuedTurn, error) {
	if !s.clientInputReceiptsEnabled() {
		return nil, nil
	}
	rows, err := binding.Sessions.ListUnsettledClientInputs(s.clientInputScope(threadID, ""))
	if err != nil {
		return nil, err
	}
	var turns []queuedTurn
	for _, row := range rows {
		if row.Receipt.ReceiptState != "accepted" || row.Receipt.ExecutionState != "held" {
			continue
		}
		entry, err := clientInputQueuedTurn(row)
		if err != nil {
			return nil, err
		}
		turns = append(turns, entry)
	}
	return turns, nil
}

func clientInputQueuedTurn(row session.AcceptedClientInput) (queuedTurn, error) {
	entry := queuedTurn{id: row.Receipt.QueueID, clientInput: &row}
	if err := json.Unmarshal(row.MessageJSON, &entry.msg); err != nil {
		return entry, err
	}
	if err := json.Unmarshal(row.RuntimeJSON, &entry.snapshot); err != nil {
		return entry, err
	}
	if entry.msg.Origin != "user" || entry.msg.Role != "user" || entry.msg.Hidden || entry.msg.ReadOnly || entry.msg.ClientID != row.Receipt.ClientID || entry.snapshot.PluginTurn == nil || entry.snapshot.PluginTurn.PluginID != row.Receipt.PluginID || entry.snapshot.PluginTurn.RequestID != row.Receipt.HostRequestID || entry.snapshot.PluginTurn.QueueID != row.Receipt.QueueID {
		return entry, errors.New("invalid authenticated accepted envelope")
	}
	return entry, nil
}

func clientInputSendResult(r providers.ClientInputReceipt) pluginhost.SessionSendResult {
	return pluginhost.SessionSendResult{State: r.ExecutionState, SessionID: r.SessionID, TurnID: r.HostTurnID, QueueID: r.QueueID, Receipt: &r}
}

func (s *Server) acceptClientSessionInput(ctx context.Context, pluginID string, params pluginhost.SessionSendParams, metadata session.Session, th *threadState, msg providers.ChatMessage, snapshot turnRuntimeSnapshot, selection []runtime.ClientInputAttachment) (pluginhost.SessionSendResult, error) {
	owner, err := s.ensureClientInputOwner(th.state)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	queueID := session.NewID()
	snapshot.PluginTurn.QueueID = queueID
	baseline, found, err := th.state.Sessions.ReadControl(th.ID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if !found {
		baseline = session.Control{SessionID: th.ID}
	}
	snapshot.ExecutionControlBaseline = &baseline
	messageJSON, err := json.Marshal(msg)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	runtimeJSON, err := json.Marshal(snapshot)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	selectionJSON, err := json.Marshal(selection)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	row := session.AcceptedClientInput{Receipt: providers.ClientInputReceipt{RequestID: params.UserInputRequestID, HostRequestID: params.RequestID, ReceiptID: session.NewID(), ClientID: msg.ClientID, QueueID: queueID, SessionID: th.ID, PluginID: pluginID, Owner: metadata.Owner, UserSpaceID: metadata.UserSpaceID, ProductDomain: metadata.ProductDomain, AgentID: metadata.AgentID}, Fingerprint: clientInputFingerprint(params), MessageJSON: messageJSON, RuntimeJSON: runtimeJSON, SelectionJSON: selectionJSON, LiveOwner: owner}
	row, created, err := th.state.Sessions.AcceptClientInput(row, snapshot.Control, &baseline)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if created {
		return s.enqueueAcceptedClientInput(ctx, th, row)
	}
	return clientInputSendResult(row.Receipt), nil
}

func (s *Server) enqueueAcceptedClientInput(ctx context.Context, th *threadState, row session.AcceptedClientInput) (pluginhost.SessionSendResult, error) {
	entry, err := clientInputQueuedTurn(row)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	origin, err := s.retainPluginTurnOrigin(th.state, row.Receipt.PluginID, row.Receipt.HostRequestID)
	if err != nil {
		held, holdErr := th.state.Sessions.ChangeClientInput(row, "held", "", nil, nil, nil)
		if holdErr != nil {
			return pluginhost.SessionSendResult{}, errors.Join(err, holdErr)
		}
		return clientInputSendResult(held.Receipt), nil
	}
	entry.snapshot.PluginTurn.Origin = origin
	entry.snapshot.AdmissionPrincipal, _ = principal.FromContext(ctx)
	entry.snapshot.AdmissionDelegation = access.DelegationFromContext(ctx)
	entry.snapshot.AdmissionAuthorization = runtime.CaptureExecutionAuthorization(ctx)
	if !s.enqueueQueuedUserTurn(th.ID, entry) {
		_ = origin.Close()
		// Acceptance remains durable even when shutdown closes the local queue.
		if held, holdErr := th.state.Sessions.ChangeClientInput(row, "held", "", nil, nil, nil); holdErr == nil {
			row = held
		}
		return clientInputSendResult(row.Receipt), nil
	}
	_ = s.writeNotification(NotificationTurnQueued, TurnQueuedNotification{Queued: queuedTurnSummary(th.ID, entry)})
	s.tryDrainQueuedTurns(th.state, th.ID, true)
	latest, found, err := th.state.Sessions.ReadClientInput(s.clientInputScope(th.ID, row.Receipt.PluginID), row.Receipt.HostRequestID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if found {
		row = latest
	}
	return clientInputSendResult(row.Receipt), nil
}

func (s *Server) resumeAcceptedClientInput(ctx context.Context, pluginID string, params pluginhost.SessionSendParams, th *threadState, row session.AcceptedClientInput) (pluginhost.SessionSendResult, error) {
	if row.Receipt.ExecutionState != "held" {
		return clientInputSendResult(row.Receipt), nil
	}
	active := false
	for _, p := range s.rt.ExtensionSnapshot().ActivePlugins {
		if p.ID == pluginID {
			active = true
			break
		}
	}
	if !active {
		return pluginhost.SessionSendResult{}, errors.New("owning plugin is not active")
	}
	control, err := s.pluginSessionControl(th.state, pluginID, th.ID, params.ControlRevision)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if control != nil && control.State != session.ControlActive {
		return pluginhost.SessionSendResult{}, session.ErrControlChanged
	}
	baseline, found, err := th.state.Sessions.ReadControl(th.ID)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if !found {
		baseline = session.Control{SessionID: th.ID}
	}
	if found && baseline.State != session.ControlReleased && (baseline.ManagerID != "plugin:"+pluginID || baseline.State != session.ControlActive) {
		return pluginhost.SessionSendResult{}, session.ErrControlChanged
	}
	entry, err := clientInputQueuedTurn(row)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	entry.snapshot.Control = control
	entry.snapshot.ExecutionControlBaseline = &baseline
	snapshot, err := json.Marshal(entry.snapshot)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	owner, err := s.ensureClientInputOwner(th.state)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	row, err = th.state.Sessions.ChangeClientInput(row, "queued", owner, snapshot, control, &baseline)
	if err != nil {
		return pluginhost.SessionSendResult{}, err
	}
	return s.enqueueAcceptedClientInput(ctx, th, row)
}

// Recreate disposable working copies from the durable original selection only
// during authorized execution, never while inspecting history or receipts.
func (s *Server) materializeAcceptedClientInput(binding *runtime.State, entry *queuedTurn) error {
	metadata, found, err := binding.Sessions.Find(entry.clientInput.Receipt.SessionID)
	if err != nil {
		return err
	}
	r := entry.clientInput.Receipt
	if err := s.validateClientInputSpace(r.UserSpaceID, r.ProductDomain); err != nil {
		return err
	}
	if !found || metadata.Owner != r.Owner || metadata.UserSpaceID != r.UserSpaceID || metadata.ProductDomain != r.ProductDomain || metadata.AgentID != r.AgentID || metadata.ArchivedAt != nil {
		return errors.New("accepted input binding changed")
	}
	active := false
	for _, p := range s.rt.ExtensionSnapshot().ActivePlugins {
		if p.ID == r.PluginID {
			active = true
			break
		}
	}
	if !active {
		return errors.New("owning plugin is not active")
	}
	var selected []runtime.ClientInputAttachment
	if err := json.Unmarshal(entry.clientInput.SelectionJSON, &selected); err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}
	var received []TurnStartImage
	var files []TurnStartFile
	for _, file := range selected {
		switch file.MediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			received = append(received, TurnStartImage{MediaType: file.MediaType, Data: file.Data, Original: true})
		default:
			files = append(files, TurnStartFile{MediaType: file.MediaType, Data: file.Data, Filename: file.Filename})
		}
	}
	proof := runtime.ClientUserInput{RequestID: entry.clientInput.Receipt.RequestID, Prompt: entry.msg.DisplayContent, Attachments: selected}
	if err := runtime.ValidateClientUserInput(proof); err != nil {
		return fmt.Errorf("stored attachment integrity: %w", err)
	}
	images, err := normalizeTurnStartImages(received)
	if err != nil {
		return err
	}
	normalized, err := normalizeTurnStartFiles(files)
	if err != nil {
		return err
	}
	msg, err := literalUserMessageFromPrompt(proof.Prompt, images, normalized)
	if err != nil {
		return err
	}
	msg, err = s.persistInputWorkingCopies(context.Background(), entry.clientInput.Receipt.SessionID, msg, received, false)
	if err != nil {
		return err
	}
	msg.ClientID = entry.msg.ClientID
	msg.Origin = "user"
	msg.Relation = entry.msg.Relation
	msg.Cause = entry.msg.Cause
	msg.DisplayContent = proof.Prompt
	entry.msg = msg
	return nil
}

func acceptedInputView(row session.AcceptedClientInput) (*pluginhost.SessionAcceptedInput, error) {
	if row.Receipt.ReceiptState != "accepted" || row.Receipt.ExecutionState == "cancelled" {
		return nil, nil
	}
	var msg providers.ChatMessage
	if err := json.Unmarshal(row.MessageJSON, &msg); err != nil {
		return nil, err
	}
	var attachments []runtime.ClientInputAttachment
	if err := json.Unmarshal(row.SelectionJSON, &attachments); err != nil {
		return nil, err
	}
	result := &pluginhost.SessionAcceptedInput{Receipt: row.Receipt, Prompt: msg.DisplayContent, Relation: msg.Relation}
	for _, a := range attachments {
		result.Attachments = append(result.Attachments, pluginhost.SessionInputAttachment{SHA256: a.SHA256, Filename: a.Filename, MediaType: a.MediaType})
	}
	return result, nil
}

func (s *Server) validateClientInputSpace(space, domain string) error {
	if !s.clientInputReceiptsEnabled() || !s.rt.UserBinding().Matches(userspace.Binding{UserSpaceID: space, ProductDomain: domain}) {
		return errors.New("accepted input is outside this host user space")
	}
	return nil
}

func (s *Server) clientInputReceiptsEnabled() bool {
	if s.rt == nil || s.rt.SessionDir == "" {
		return false
	}
	b := s.rt.UserBinding()
	return b.IsUser() && b.ProductDomain == "personal" && b.Validate() == nil
}

func (s *Server) clientInputScope(sessionID, pluginID string) session.ClientInputScope {
	return session.ClientInputScope{Binding: s.rt.UserBinding(), SessionID: sessionID, PluginID: pluginID}
}
