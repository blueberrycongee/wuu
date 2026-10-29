package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// Summary queries and recovery never hydrate the original attachment payloads.
// Both records commit with history admission in the selected provider transaction.
type clientInputDocument struct {
	Receipt     providers.ClientInputReceipt
	Fingerprint string
	LiveOwner   string
	Order       int64
}
type clientInputPayload struct {
	OriginalText  *string `json:"OriginalText,omitempty"`
	MessageJSON   []byte
	RuntimeJSON   []byte
	SelectionJSON []byte
}

func clientInputDocumentKey(sessionID, clientID string) string {
	return documentPrefix(sessionID) + clientID
}

func clientInputScopeMatchesDocument(tx *persistence.Tx, scope ClientInputScope, row clientInputDocument) (bool, error) {
	r := row.Receipt
	if r.UserSpaceID != scope.Binding.UserSpaceID || r.ProductDomain != "personal" || scope.SessionID != "" && r.SessionID != scope.SessionID || scope.PluginID != "" && r.PluginID != scope.PluginID || scope.LiveOwner != "" && row.LiveOwner != scope.LiveOwner {
		return false, nil
	}
	sess, found, err := getSessionDocument(tx, r.SessionID)
	return found && sess.UserSpaceID == scope.Binding.UserSpaceID && sess.ProductDomain == "personal", err
}

func loadClientInputPayloadDocument(tx *persistence.Tx, row clientInputDocument) (AcceptedClientInput, error) {
	var payload clientInputPayload
	found, err := tx.Get(documentNamespace("client_input_payloads"), clientInputDocumentKey(row.Receipt.SessionID, row.Receipt.ClientID), &payload)
	if err != nil {
		return AcceptedClientInput{}, err
	}
	if !found {
		return AcceptedClientInput{}, persistence.ErrCorrupt
	}
	return AcceptedClientInput{Receipt: row.Receipt, Fingerprint: row.Fingerprint, LiveOwner: row.LiveOwner, MessageJSON: payload.MessageJSON, RuntimeJSON: payload.RuntimeJSON, SelectionJSON: payload.SelectionJSON, OriginalText: payload.OriginalText}, nil
}

func readClientInputByRequestDocument(tx *persistence.Tx, scope ClientInputScope, hostRequestID string) (clientInputDocument, bool, error) {
	rows, err := readDocumentRows[clientInputDocument](tx, "client_input_receipts", documentPrefix(scope.SessionID))
	if err != nil {
		return clientInputDocument{}, false, err
	}
	for _, row := range rows {
		if row.Receipt.HostRequestID != hostRequestID {
			continue
		}
		matches, err := clientInputScopeMatchesDocument(tx, scope, row)
		if err != nil {
			return clientInputDocument{}, false, err
		}
		if matches {
			return row, true, nil
		}
	}
	return clientInputDocument{}, false, nil
}

func (s *documentStore) ReadClientInput(scope ClientInputScope, hostRequestID string) (AcceptedClientInput, bool, error) {
	if err := scope.validate(true); err != nil {
		return AcceptedClientInput{}, false, err
	}
	var result AcceptedClientInput
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		result, found = AcceptedClientInput{}, false
		row, exists, err := readClientInputByRequestDocument(tx, scope, hostRequestID)
		if err != nil || !exists {
			return err
		}
		result, err = loadClientInputPayloadDocument(tx, row)
		found = err == nil
		return err
	})
	return result, found, err
}

func (s *documentStore) ListUnsettledClientInputs(scope ClientInputScope) ([]AcceptedClientInput, error) {
	if err := scope.validate(true); err != nil {
		return nil, err
	}
	var result []AcceptedClientInput
	err := s.transaction(func(tx *persistence.Tx) error {
		result = nil
		rows, err := readDocumentRows[clientInputDocument](tx, "client_input_receipts", documentPrefix(scope.SessionID))
		if err != nil {
			return err
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Order < rows[j].Order })
		for _, row := range rows {
			switch row.Receipt.ExecutionState {
			case "queued", "held", "running":
			default:
				continue
			}
			matches, err := clientInputScopeMatchesDocument(tx, scope, row)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			input, err := loadClientInputPayloadDocument(tx, row)
			if err != nil {
				return err
			}
			result = append(result, input)
		}
		return nil
	})
	return result, err
}

func validateClientInputBindingDocument(tx *persistence.Tx, r AcceptedClientInput) error {
	binding := userspace.Binding{UserSpaceID: r.Receipt.UserSpaceID, ProductDomain: r.Receipt.ProductDomain}
	if binding.Validate() != nil || !binding.IsUser() || binding.ProductDomain != "personal" {
		return errors.New("accepted input requires Personal binding")
	}
	sess, err := requireSessionDocument(tx, r.Receipt.SessionID)
	if err != nil {
		return err
	}
	if sess.Owner != r.Receipt.Owner || sess.UserSpaceID != r.Receipt.UserSpaceID || sess.ProductDomain != r.Receipt.ProductDomain || sess.AgentID != r.Receipt.AgentID || sess.ArchivedAt != nil {
		return errors.New("accepted input session ownership or binding changed")
	}
	return nil
}

func validateHistoryControlDocument(tx *persistence.Tx, id string, control, baseline *Control) error {
	if control != nil {
		if control.SessionID != id {
			return ErrControlChanged
		}
		if err := validateControlDocument(tx, *control); err != nil {
			return err
		}
	}
	if baseline != nil {
		if baseline.SessionID != id {
			return ErrControlChanged
		}
		if err := validateContinuationDocument(tx, *baseline); err != nil {
			return err
		}
	}
	return nil
}

// Receipts require the exact saved admission; first-manager adoption is not an
// exception to the client's original owner/control authority.
func validateClientInputControlDocument(tx *persistence.Tx, r AcceptedClientInput) error {
	var snapshot struct {
		Control                  *Control
		ExecutionControlBaseline *Control
	}
	if err := json.Unmarshal(r.RuntimeJSON, &snapshot); err != nil {
		return err
	}
	baseline := snapshot.ExecutionControlBaseline
	if baseline == nil || baseline.SessionID != r.Receipt.SessionID {
		return ErrControlChanged
	}
	current := Control{SessionID: r.Receipt.SessionID}
	if _, err := tx.Get(documentNamespace("session_controls"), r.Receipt.SessionID, &current); err != nil {
		return err
	}
	if current != *baseline {
		return ErrControlChanged
	}
	return validateHistoryControlDocument(tx, r.Receipt.SessionID, snapshot.Control, baseline)
}

func saveClientInputDocument(tx *persistence.Tx, r AcceptedClientInput) error {
	key := clientInputDocumentKey(r.Receipt.SessionID, r.Receipt.ClientID)
	var row clientInputDocument
	found, err := tx.Get(documentNamespace("client_input_receipts"), key, &row)
	if err != nil {
		return err
	}
	if !found {
		return ErrClientInputSettled
	}
	row.Receipt, row.Fingerprint, row.LiveOwner = r.Receipt, r.Fingerprint, r.LiveOwner
	if err := tx.Put(documentNamespace("client_input_receipts"), key, row); err != nil {
		return err
	}
	return tx.Put(documentNamespace("client_input_payloads"), key, clientInputPayload{OriginalText: r.OriginalText, MessageJSON: r.MessageJSON, RuntimeJSON: r.RuntimeJSON, SelectionJSON: r.SelectionJSON})
}

func (s *documentStore) AcceptClientInput(input AcceptedClientInput, control, baseline *Control) (AcceptedClientInput, bool, error) {
	acceptedAt := time.Now().UTC().Format(time.RFC3339Nano)
	var result AcceptedClientInput
	var fresh bool
	err := s.transaction(func(tx *persistence.Tx) error {
		result, fresh = input, false
		result.OriginalText = nil
		if err := validateClientInputBindingDocument(tx, input); err != nil {
			return err
		}
		if err := validateClientInputControlDocument(tx, input); err != nil {
			return err
		}
		if err := validateHistoryControlDocument(tx, input.Receipt.SessionID, control, baseline); err != nil {
			return err
		}
		rows, err := readDocumentRows[clientInputDocument](tx, "client_input_receipts", "")
		if err != nil {
			return err
		}
		scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: input.Receipt.UserSpaceID, ProductDomain: input.Receipt.ProductDomain}, SessionID: input.Receipt.SessionID, PluginID: input.Receipt.PluginID}
		for _, row := range rows {
			old, r := row.Receipt, input.Receipt
			if old.PluginID == r.PluginID && old.HostRequestID == r.HostRequestID {
				matches, err := clientInputScopeMatchesDocument(tx, scope, row)
				if err != nil {
					return err
				}
				if !matches || row.Fingerprint != input.Fingerprint || old.RequestID != r.RequestID {
					return ErrClientInputConflict
				}
				result, err = loadClientInputPayloadDocument(tx, row)
				return err
			}
			if old.SessionID == r.SessionID && old.ClientID == r.ClientID || old.QueueID == r.QueueID || old.SessionID == r.SessionID && old.PluginID == r.PluginID && old.RequestID == r.RequestID {
				return ErrClientInputConflict
			}
		}
		r := &result.Receipt
		if r.RequestID == "" || r.HostRequestID == "" || r.PluginID == "" || r.ClientID == "" || r.ReceiptID == "" || r.QueueID == "" || input.Fingerprint == "" || len(input.MessageJSON) == 0 || len(input.RuntimeJSON) == 0 || input.LiveOwner == "" {
			return errors.New("incomplete accepted client input")
		}
		r.AcceptedAt, r.Revision, r.ReceiptState, r.ExecutionState = acceptedAt, 1, "accepted", "queued"
		var order int64
		if _, err := tx.Get(documentNamespace("sequences"), "client_inputs", &order); err != nil {
			return err
		}
		order++
		if err := tx.Put(documentNamespace("sequences"), "client_inputs", order); err != nil {
			return err
		}
		key := clientInputDocumentKey(r.SessionID, r.ClientID)
		if err := tx.Put(documentNamespace("client_input_receipts"), key, clientInputDocument{result.Receipt, result.Fingerprint, result.LiveOwner, order}); err != nil {
			return err
		}
		if err := tx.Put(documentNamespace("client_input_payloads"), key, clientInputPayload{OriginalText: result.OriginalText, MessageJSON: result.MessageJSON, RuntimeJSON: result.RuntimeJSON, SelectionJSON: result.SelectionJSON}); err != nil {
			return err
		}
		fresh = true
		return nil
	})
	return result, fresh, err
}

func (s *documentStore) ChangeClientInput(expected AcceptedClientInput, state, owner string, runtimeJSON []byte, control, baseline *Control) (AcceptedClientInput, error) {
	scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: expected.Receipt.UserSpaceID, ProductDomain: expected.Receipt.ProductDomain}, SessionID: expected.Receipt.SessionID, PluginID: expected.Receipt.PluginID}
	if err := scope.validate(true); err != nil {
		return expected, err
	}
	var result AcceptedClientInput
	err := s.transaction(func(tx *persistence.Tx) error {
		result = expected
		var row clientInputDocument
		found, err := tx.Get(documentNamespace("client_input_receipts"), clientInputDocumentKey(expected.Receipt.SessionID, expected.Receipt.ClientID), &row)
		if err != nil {
			return err
		}
		if !found {
			return ErrClientInputSettled
		}
		matches, err := clientInputScopeMatchesDocument(tx, scope, row)
		if err != nil {
			return err
		}
		if !matches {
			return ErrClientInputSettled
		}
		result, err = loadClientInputPayloadDocument(tx, row)
		if err != nil {
			return err
		}
		r := &result.Receipt
		if r.Revision != expected.Receipt.Revision || r.ReceiptState != "accepted" || r.ExecutionState == "cancelled" {
			return ErrClientInputSettled
		}
		if state != "held" {
			if err := validateClientInputBindingDocument(tx, result); err != nil {
				return err
			}
		}
		if state == "queued" {
			if err := validateHistoryControlDocument(tx, r.SessionID, control, baseline); err != nil {
				return err
			}
		}
		if state != "queued" && state != "held" && state != "cancelled" {
			return errors.New("invalid accepted input transition")
		}
		r.ExecutionState, r.Revision, result.LiveOwner = state, r.Revision+1, owner
		if runtimeJSON != nil {
			result.RuntimeJSON = runtimeJSON
		}
		if state == "queued" {
			if err := validateClientInputControlDocument(tx, result); err != nil {
				return err
			}
		}
		if state == "cancelled" {
			result.MessageJSON, result.RuntimeJSON, result.SelectionJSON, result.OriginalText = nil, nil, nil, nil
		}
		return saveClientInputDocument(tx, result)
	})
	return result, err
}

func validateAcceptedClientInputDocument(tx *persistence.Tx, id string, rec HistoryRecord) error {
	sess, err := requireSessionDocument(tx, id)
	if err != nil {
		return err
	}
	if sess.UserSpaceID == "" || sess.ProductDomain != "personal" {
		if rec.AcceptedClientInput != nil {
			return ErrClientInputSettled
		}
		return nil
	}
	var row clientInputDocument
	found, err := tx.Get(documentNamespace("client_input_receipts"), clientInputDocumentKey(id, rec.ClientID), &row)
	if err != nil {
		return err
	}
	expected := rec.AcceptedClientInput
	if expected == nil {
		if found {
			return ErrClientInputSettled
		}
		return nil
	}
	if !found || expected.Receipt.SessionID != id || expected.Receipt.ClientID != rec.ClientID {
		return ErrClientInputSettled
	}
	if err := validateClientInputBindingDocument(tx, *expected); err != nil {
		return err
	}
	scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: sess.UserSpaceID, ProductDomain: sess.ProductDomain}, SessionID: id, PluginID: expected.Receipt.PluginID}
	matches, err := clientInputScopeMatchesDocument(tx, scope, row)
	if err != nil {
		return err
	}
	if !matches {
		return ErrClientInputSettled
	}
	r, err := loadClientInputPayloadDocument(tx, row)
	if err != nil {
		return err
	}
	if r.Receipt != expected.Receipt || r.Fingerprint != expected.Fingerprint || r.Receipt.ExecutionState != "queued" || r.Receipt.ReceiptState != "accepted" || r.LiveOwner != expected.LiveOwner || !bytes.Equal(r.MessageJSON, expected.MessageJSON) || !bytes.Equal(r.RuntimeJSON, expected.RuntimeJSON) || !bytes.Equal(r.SelectionJSON, expected.SelectionJSON) || rec.Role != "user" || rec.Origin != "user" || rec.Hidden || rec.ReadOnly {
		return ErrClientInputSettled
	}
	if err := validateClientInputControlDocument(tx, r); err != nil {
		return err
	}
	return validateClientInputBindingDocument(tx, r)
}

func settleClientInputDocument(tx *persistence.Tx, rec HistoryRecord, seq int) error {
	if rec.AcceptedClientInput == nil {
		return nil
	}
	r := *rec.AcceptedClientInput
	r.Receipt.MessageSeq, r.Receipt.HostTurnID = seq, rec.AcceptedClientTurnID
	r.Receipt.ReceiptState, r.Receipt.ExecutionState = "in_history", "running"
	r.Receipt.Revision++
	// Retain only the exact original text, not the execution snapshot or another
	// copy of binary attachments. The validated admission envelope, never caller
	// OriginalText, supplies this evidence in the same transaction as history.
	r.OriginalText = nil
	var original providers.ChatMessage
	if json.Unmarshal(r.MessageJSON, &original) == nil && original.Role == "user" && original.Origin == "user" && original.OriginID == "" && !original.Hidden && !original.ReadOnly && !original.Truncated && original.ClientID == r.Receipt.ClientID && original.Content == original.DisplayContent {
		text := original.DisplayContent
		r.OriginalText = &text
	}
	r.MessageJSON, r.RuntimeJSON = nil, nil
	return saveClientInputDocument(tx, r)
}

func (s *documentStore) SettleClientInputExecution(scope ClientInputScope, hostRequestID, turnID, state, reason string) error {
	switch state {
	case "completed", "failed", "interrupted":
	default:
		return nil
	}
	if err := scope.validate(true); err != nil {
		return err
	}
	return s.transaction(func(tx *persistence.Tx) error {
		row, found, err := readClientInputByRequestDocument(tx, scope, hostRequestID)
		if err != nil || !found {
			return err
		}
		r := &row.Receipt
		if r.ReceiptState != "in_history" || r.HostTurnID != turnID || r.ExecutionState != "running" {
			return nil
		}
		r.ExecutionState, r.Revision, row.LiveOwner = state, r.Revision+1, ""
		if reason != "" {
			r.ErrorStage, r.Error = "execution", reason
		}
		return tx.Put(documentNamespace("client_input_receipts"), clientInputDocumentKey(r.SessionID, r.ClientID), row)
	})
}

func (s *documentStore) ListClientInputRecoveryCandidates(scope ClientInputScope) ([]AcceptedClientInput, error) {
	if err := scope.validate(false); err != nil {
		return nil, err
	}
	var result []AcceptedClientInput
	err := s.transaction(func(tx *persistence.Tx) error {
		result = nil
		prefix := ""
		if scope.SessionID != "" {
			prefix = documentPrefix(scope.SessionID)
		}
		rows, err := readDocumentRows[clientInputDocument](tx, "client_input_receipts", prefix)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.LiveOwner == "" || row.Receipt.ExecutionState != "queued" && row.Receipt.ExecutionState != "running" {
				continue
			}
			matches, err := clientInputScopeMatchesDocument(tx, scope, row)
			if err != nil {
				return err
			}
			if matches {
				result = append(result, AcceptedClientInput{Receipt: row.Receipt, Fingerprint: row.Fingerprint, LiveOwner: row.LiveOwner})
			}
		}
		return nil
	})
	return result, err
}

// The caller has checked host-owned liveness outside the selected provider.
// Compare the complete admission summary so a stale probe cannot overwrite a
// concurrent resume, cancellation, owner change or terminal settlement.
func (s *documentStore) RecoverClientInput(scope ClientInputScope, expected AcceptedClientInput) error {
	if err := scope.validate(false); err != nil {
		return err
	}
	return s.transaction(func(tx *persistence.Tx) error {
		key := clientInputDocumentKey(expected.Receipt.SessionID, expected.Receipt.ClientID)
		var row clientInputDocument
		found, err := tx.Get(documentNamespace("client_input_receipts"), key, &row)
		if err != nil || !found {
			return err
		}
		matches, err := clientInputScopeMatchesDocument(tx, scope, row)
		if err != nil {
			return err
		}
		if !matches || row.Receipt != expected.Receipt || row.LiveOwner == "" || row.LiveOwner != expected.LiveOwner || row.Fingerprint != expected.Fingerprint || row.Receipt.ExecutionState != "queued" && row.Receipt.ExecutionState != "running" {
			return nil
		}
		switch row.Receipt.ReceiptState {
		case "accepted":
			row.Receipt.ExecutionState = "held"
		case "in_history":
			row.Receipt.ExecutionState = "unconfirmed"
		default:
			return nil
		}
		row.Receipt.Revision++
		row.LiveOwner = ""
		return tx.Put(documentNamespace("client_input_receipts"), key, row)
	})
}

func (s *documentStore) ClientInputHistoryReceipts(scope ClientInputScope) (map[string]providers.ClientInputReceipt, error) {
	if err := scope.validate(true); err != nil {
		return nil, err
	}
	var result map[string]providers.ClientInputReceipt
	err := s.transaction(func(tx *persistence.Tx) error {
		result = make(map[string]providers.ClientInputReceipt)
		rows, err := readDocumentRows[clientInputDocument](tx, "client_input_receipts", documentPrefix(scope.SessionID))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Receipt.MessageSeq <= 0 {
				continue
			}
			matches, err := clientInputScopeMatchesDocument(tx, scope, row)
			if err != nil {
				return err
			}
			if matches {
				result[row.Receipt.ClientID] = row.Receipt
			}
		}
		return nil
	})
	return result, err
}
