package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

var ErrClientInputConflict = errors.New("request_id_conflict")
var ErrClientInputSettled = errors.New("accepted client input changed or settled")

// AcceptedClientInput is host-owned. SelectionJSON retains the complete original
// attachment bytes, addressed by receipt ID and ordinal, independently of expiring
// tool working copies. Only validated live client submissions may create a row.
type AcceptedClientInput struct {
	Receipt       providers.ClientInputReceipt
	Fingerprint   string
	MessageJSON   []byte
	RuntimeJSON   []byte
	SelectionJSON []byte
	LiveOwner     string
}

type receiptScanner interface{ Scan(...any) error }

func scanClientInput(row receiptScanner) (AcceptedClientInput, error) {
	var r AcceptedClientInput
	var summary, message, runtime, selection string
	err := row.Scan(&summary, &r.Fingerprint, &message, &runtime, &selection, &r.LiveOwner)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(summary), &r.Receipt)
	r.MessageJSON = []byte(message)
	r.RuntimeJSON = []byte(runtime)
	r.SelectionJSON = []byte(selection)
	return r, err
}

const clientInputColumns = "summary_json,fingerprint,message_json,runtime_json,selection_json,live_owner"

// ClientInputScope comes from the immutable host binding, never plugin parameters.
// Payload queries require a target session. Recovery may span that binding at
// startup, or only this host's owner at shutdown.
type ClientInputScope struct {
	Binding   userspace.Binding
	SessionID string
	PluginID  string
	LiveOwner string
}

func (scope ClientInputScope) predicate(requireSession bool) (string, []any, error) {
	if scope.Binding.Validate() != nil || !scope.Binding.IsUser() || scope.Binding.ProductDomain != "personal" || (requireSession && scope.SessionID == "") {
		return "", nil, errors.New("client receipts require a Personal host binding and target session")
	}
	where := `session_id IN (SELECT id FROM sessions WHERE user_space_id=? AND product_domain='personal') AND json_extract(summary_json,'$.user_space_id')=? AND json_extract(summary_json,'$.product_domain')='personal'`
	args := []any{scope.Binding.UserSpaceID, scope.Binding.UserSpaceID}
	if scope.SessionID != "" {
		where += ` AND session_id=?`
		args = append(args, scope.SessionID)
	}
	if scope.PluginID != "" {
		where += ` AND plugin_id=?`
		args = append(args, scope.PluginID)
	}
	if scope.LiveOwner != "" {
		where += ` AND live_owner=?`
		args = append(args, scope.LiveOwner)
	}
	return where, args, nil
}

func ReadClientInput(dir string, scope ClientInputScope, hostRequestID string) (AcceptedClientInput, bool, error) {
	where, args, err := scope.predicate(true)
	if err != nil {
		return AcceptedClientInput{}, false, err
	}
	args = append(args, hostRequestID)
	db, err := openStore(dir)
	if err != nil {
		return AcceptedClientInput{}, false, err
	}
	defer db.Close()
	r, err := scanClientInput(db.QueryRow(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND host_request_id=?`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func ListUnsettledClientInputs(dir string, scope ClientInputScope) ([]AcceptedClientInput, error) {
	where, args, err := scope.predicate(true)
	if err != nil {
		return nil, err
	}
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND json_extract(summary_json,'$.execution_state') IN ('queued','held','running') ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AcceptedClientInput
	for rows.Next() {
		r, err := scanClientInput(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func validateClientInputBindingTx(tx *sql.Tx, r AcceptedClientInput) error {
	binding := userspace.Binding{UserSpaceID: r.Receipt.UserSpaceID, ProductDomain: r.Receipt.ProductDomain}
	if binding.Validate() != nil || !binding.IsUser() || binding.ProductDomain != "personal" {
		return errors.New("accepted input requires Personal binding")
	}
	var owner, space, domain, agent string
	var archived sql.NullString
	err := tx.QueryRow(`SELECT owner,user_space_id,product_domain,agent_id,archived_at FROM sessions WHERE id=?`, r.Receipt.SessionID).Scan(&owner, &space, &domain, &agent, &archived)
	if err != nil {
		return err
	}
	if owner != r.Receipt.Owner || space != r.Receipt.UserSpaceID || domain != r.Receipt.ProductDomain || agent != r.Receipt.AgentID || archived.Valid {
		return errors.New("accepted input session ownership or binding changed")
	}
	return nil
}

// A client receipt never inherits the continuation API's first-manager adoption
// exception. Its saved admission must still be exact at commit time.
func validateClientInputControlTx(tx *sql.Tx, r AcceptedClientInput) error {
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
	err := tx.QueryRow(`SELECT manager_id,revision,state FROM session_controls WHERE session_id=?`, r.Receipt.SessionID).Scan(&current.ManagerID, &current.Revision, &current.State)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current != *baseline {
		return ErrControlChanged
	}
	return validateHistoryControlTx(tx, r.Receipt.SessionID, snapshot.Control, baseline)
}

func saveClientInputTx(tx *sql.Tx, r AcceptedClientInput) error {
	summary, err := json.Marshal(r.Receipt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE client_input_receipts SET summary_json=?,message_json=?,runtime_json=?,selection_json=?,live_owner=? WHERE session_id=? AND client_id=?`, string(summary), string(r.MessageJSON), string(r.RuntimeJSON), string(r.SelectionJSON), r.LiveOwner, r.Receipt.SessionID, r.Receipt.ClientID)
	return err
}

func AcceptClientInput(dir string, r AcceptedClientInput, control, baseline *Control) (AcceptedClientInput, bool, error) {
	db, err := openStore(dir)
	if err != nil {
		return r, false, err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA synchronous = FULL`); err != nil {
		return r, false, err
	}

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return r, false, err
	}
	defer tx.Rollback()
	if err := validateClientInputBindingTx(tx, r); err != nil {
		return r, false, err
	}
	if err := validateClientInputControlTx(tx, r); err != nil {
		return r, false, err
	}
	if err := validateHistoryControlTx(tx, r.Receipt.SessionID, control, baseline); err != nil {
		return r, false, err
	}
	var priorSession string
	err = tx.QueryRow(`SELECT session_id FROM client_input_receipts WHERE plugin_id=? AND host_request_id=?`, r.Receipt.PluginID, r.Receipt.HostRequestID).Scan(&priorSession)
	if err == nil && priorSession != r.Receipt.SessionID {
		return r, false, ErrClientInputConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, false, err
	}
	scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: r.Receipt.UserSpaceID, ProductDomain: r.Receipt.ProductDomain}, SessionID: r.Receipt.SessionID, PluginID: r.Receipt.PluginID}
	where, args, err := scope.predicate(true)
	if err != nil {
		return r, false, err
	}
	args = append(args, r.Receipt.HostRequestID)
	prior, err := scanClientInput(tx.QueryRow(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND host_request_id=?`, args...))
	if errors.Is(err, sql.ErrNoRows) && priorSession != "" {
		return r, false, ErrClientInputConflict
	}
	if err == nil {
		if prior.Fingerprint != r.Fingerprint || prior.Receipt.SessionID != r.Receipt.SessionID || prior.Receipt.RequestID != r.Receipt.RequestID {
			return r, false, ErrClientInputConflict
		}
		return prior, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, false, err
	}
	if r.Receipt.RequestID == "" || r.Receipt.ClientID == "" || r.Receipt.ReceiptID == "" || r.Receipt.QueueID == "" || r.Fingerprint == "" || len(r.MessageJSON) == 0 || len(r.RuntimeJSON) == 0 || r.LiveOwner == "" {
		return r, false, errors.New("incomplete accepted client input")
	}
	r.Receipt.AcceptedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.Receipt.Revision = 1
	r.Receipt.ReceiptState = "accepted"
	r.Receipt.ExecutionState = "queued"
	summary, err := json.Marshal(r.Receipt)
	if err != nil {
		return r, false, err
	}
	_, err = tx.Exec(`INSERT INTO client_input_receipts(session_id,client_id,plugin_id,host_request_id,request_id,queue_id,summary_json,fingerprint,message_json,runtime_json,selection_json,live_owner) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, r.Receipt.SessionID, r.Receipt.ClientID, r.Receipt.PluginID, r.Receipt.HostRequestID, r.Receipt.RequestID, r.Receipt.QueueID, string(summary), r.Fingerprint, string(r.MessageJSON), string(r.RuntimeJSON), string(r.SelectionJSON), r.LiveOwner)
	if err != nil {
		return r, false, err
	}
	return r, true, tx.Commit()
}

// ChangeClientInput compares the complete row revision before a hold, explicit
// resume or cancellation. A stale queue owner cannot consume a resumed input.
func ChangeClientInput(dir string, expected AcceptedClientInput, state, owner string, runtimeJSON []byte, control, baseline *Control) (AcceptedClientInput, error) {
	scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: expected.Receipt.UserSpaceID, ProductDomain: expected.Receipt.ProductDomain}, SessionID: expected.Receipt.SessionID, PluginID: expected.Receipt.PluginID}
	where, args, err := scope.predicate(true)
	if err != nil {
		return expected, err
	}
	args = append(args, expected.Receipt.ClientID)
	db, err := openStore(dir)
	if err != nil {
		return expected, err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA synchronous = FULL`); err != nil {
		return expected, err
	}

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return expected, err
	}
	defer tx.Rollback()
	r, err := scanClientInput(tx.QueryRow(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND client_id=?`, args...))
	if err != nil {
		return r, err
	}
	if r.Receipt.Revision != expected.Receipt.Revision || r.Receipt.ReceiptState != "accepted" || r.Receipt.ExecutionState == "cancelled" {
		return r, ErrClientInputSettled
	}
	if state != "held" {
		if err := validateClientInputBindingTx(tx, r); err != nil {
			return r, err
		}
	}
	if state == "queued" {
		if err := validateHistoryControlTx(tx, r.Receipt.SessionID, control, baseline); err != nil {
			return r, err
		}
	}
	if state != "queued" && state != "held" && state != "cancelled" {
		return r, errors.New("invalid accepted input transition")
	}
	r.Receipt.ExecutionState = state
	r.Receipt.Revision++
	r.LiveOwner = owner
	if runtimeJSON != nil {
		r.RuntimeJSON = runtimeJSON
	}
	if state == "queued" {
		if err := validateClientInputControlTx(tx, r); err != nil {
			return r, err
		}
	}
	if state == "cancelled" {
		r.MessageJSON = nil
		r.RuntimeJSON = nil
		r.SelectionJSON = nil
	}
	if err := saveClientInputTx(tx, r); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func validateAcceptedClientInputTx(tx *sql.Tx, id string, rec HistoryRecord) error {
	// Ordinary Coding history retains its original admission path. Personal still
	// rejects attempts to append a receipt client ID without its CAS envelope.
	var space, domain string
	if err := tx.QueryRow(`SELECT user_space_id,product_domain FROM sessions WHERE id=?`, id).Scan(&space, &domain); err != nil {
		return err
	}
	if space == "" || domain != "personal" {
		if rec.AcceptedClientInput != nil {
			return ErrClientInputSettled
		}
		return nil
	}
	expected := rec.AcceptedClientInput
	if expected == nil {
		var found bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_input_receipts WHERE session_id=? AND client_id=?)`, id, rec.ClientID).Scan(&found); err != nil {
			return err
		}
		if found {
			return ErrClientInputSettled
		}
		return nil
	}
	if expected.Receipt.SessionID != id || expected.Receipt.ClientID != rec.ClientID {
		return ErrClientInputSettled
	}
	if err := validateClientInputBindingTx(tx, *expected); err != nil {
		return err
	}
	scope := ClientInputScope{Binding: userspace.Binding{UserSpaceID: space, ProductDomain: domain}, SessionID: id, PluginID: expected.Receipt.PluginID}
	where, args, err := scope.predicate(true)
	if err != nil {
		return err
	}
	args = append(args, rec.ClientID)
	r, err := scanClientInput(tx.QueryRow(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND client_id=?`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrClientInputSettled
	}
	if err != nil {
		return err
	}
	if r.Receipt.Revision != expected.Receipt.Revision || r.Receipt.ExecutionState != "queued" || r.Receipt.ReceiptState != "accepted" || r.LiveOwner != expected.LiveOwner || string(r.MessageJSON) != string(expected.MessageJSON) || string(r.RuntimeJSON) != string(expected.RuntimeJSON) || rec.Role != "user" || rec.Origin != "user" || rec.Hidden || rec.ReadOnly {
		return ErrClientInputSettled
	}
	if err := validateClientInputControlTx(tx, r); err != nil {
		return err
	}
	return validateClientInputBindingTx(tx, r)
}

func settleClientInputTx(tx *sql.Tx, rec HistoryRecord, seq int) error {
	if rec.AcceptedClientInput == nil {
		return nil
	}
	r := *rec.AcceptedClientInput
	r.Receipt.MessageSeq = seq
	r.Receipt.HostTurnID = rec.AcceptedClientTurnID
	r.Receipt.ReceiptState = "in_history"
	r.Receipt.ExecutionState = "running"
	r.Receipt.Revision++
	r.MessageJSON = nil
	r.RuntimeJSON = nil
	// Keep original attachments addressable by receipt after working-copy expiry.
	return saveClientInputTx(tx, r)
}

// SettleClientInputExecution never changes the acceptance fact or replays work.
func SettleClientInputExecution(dir string, scope ClientInputScope, hostRequestID, turnID, state, reason string) error {
	switch state {
	case "completed", "failed", "interrupted":
	default:
		return nil
	}
	where, args, err := scope.predicate(true)
	if err != nil {
		return err
	}
	args = append(args, hostRequestID)
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA synchronous = FULL`); err != nil {
		return err
	}

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := scanClientInput(tx.QueryRow(`SELECT `+clientInputColumns+` FROM client_input_receipts WHERE `+where+` AND host_request_id=?`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Receipt.ReceiptState != "in_history" || r.Receipt.HostTurnID != turnID || r.Receipt.ExecutionState != "running" {
		return nil
	}
	r.Receipt.ExecutionState = state
	r.Receipt.Revision++
	r.LiveOwner = ""
	if reason != "" {
		r.Receipt.ErrorStage = "execution"
		r.Receipt.Error = reason
	}
	if err := saveClientInputTx(tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

// RecoverClientInputs only holds orphaned accepted work. Admitted work with an
// unknown outcome remains unconfirmed and cannot be resumed or replayed.
func RecoverClientInputs(dir string, scope ClientInputScope) error {
	where, args, err := scope.predicate(false)
	if err != nil {
		return err
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT summary_json,live_owner FROM client_input_receipts WHERE `+where+` AND live_owner<>'' AND json_extract(summary_json,'$.execution_state') IN ('queued','running')`, args...)
	if err != nil {
		return err
	}
	type orphan struct {
		summary, owner string
		receipt        providers.ClientInputReceipt
	}
	var candidates []orphan
	for rows.Next() {
		var r orphan
		if err := rows.Scan(&r.summary, &r.owner); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(r.summary), &r.receipt); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range candidates {
		active, err := ThreadExecutionActive(dir, r.owner)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		switch r.receipt.ReceiptState {
		case "accepted":
			r.receipt.ExecutionState = "held"
		case "in_history":
			r.receipt.ExecutionState = "unconfirmed"
		default:
			continue
		}
		r.receipt.Revision++
		summary, err := json.Marshal(r.receipt)
		if err != nil {
			return err
		}
		if _, err = db.Exec(`PRAGMA synchronous = FULL`); err != nil {
			return err
		}
		updateArgs := []any{string(summary), r.receipt.SessionID, r.receipt.ClientID, r.summary, r.owner}
		updateArgs = append(updateArgs, args...)
		// The exact summary and owner CAS cannot override resume, cancellation or
		// settlement. Recheck the binding in this write without loading attachments.
		_, err = db.Exec(`UPDATE client_input_receipts SET summary_json=?,live_owner='' WHERE session_id=? AND client_id=? AND summary_json=? AND live_owner=? AND `+where, updateArgs...)
		if err != nil {
			return err
		}
	}
	return nil
}

// Receipt summaries never hydrate attachment bytes when replaying history.
func ClientInputHistoryReceipts(dir string, scope ClientInputScope) (map[string]providers.ClientInputReceipt, error) {
	where, args, err := scope.predicate(true)
	if err != nil {
		return nil, err
	}
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT summary_json FROM client_input_receipts WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]providers.ClientInputReceipt)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r providers.ClientInputReceipt
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		if r.MessageSeq > 0 {
			result[r.ClientID] = r
		}
	}
	return result, rows.Err()
}
