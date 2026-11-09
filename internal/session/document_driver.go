package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func (s *documentStore) LoadDriverBinding(sessionID string) (pluginprotocol.DriverBinding, bool, error) {
	var binding pluginprotocol.DriverBinding
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var err error
		found, err = tx.Get(documentNamespace("session_driver_bindings"), sessionID, &binding)
		return err
	})
	return binding, found, err
}

func (s *documentStore) BindDriver(sessionID string, binding pluginprotocol.DriverBinding) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(binding.Profile) == "" || strings.TrimSpace(binding.ProviderID) == "" || strings.TrimSpace(binding.DriverID) == "" || strings.TrimSpace(binding.DriverVersion) == "" || binding.ContractVersion < 1 {
		return errors.New("complete loop driver binding is required")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var prior pluginprotocol.DriverBinding
		found, err := tx.Get(documentNamespace("session_driver_bindings"), sessionID, &prior)
		if err != nil {
			return err
		}
		if found && prior != binding {
			return fmt.Errorf("loop driver binding requires %+v; requested %+v", prior, binding)
		}
		var checkpoint DriverCheckpointRecord
		checkpointFound, err := tx.Get(documentNamespace("session_driver_checkpoints"), sessionID, &checkpoint)
		if err != nil {
			return err
		}
		if checkpointFound && (checkpoint.ContractVersion != binding.ContractVersion || checkpoint.DriverID != binding.DriverID || checkpoint.DriverVersion != binding.DriverVersion) {
			return fmt.Errorf("loop driver checkpoint requires %s@%s contract %d; requested binding %+v", checkpoint.DriverID, checkpoint.DriverVersion, checkpoint.ContractVersion, binding)
		}
		if found {
			return nil
		}
		return tx.Put(documentNamespace("session_driver_bindings"), sessionID, binding)
	})
}

func (s *documentStore) LoadDriverCheckpoint(sessionID string) (DriverCheckpointRecord, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return DriverCheckpointRecord{}, false, errors.New("driver checkpoint session id is required")
	}
	var result DriverCheckpointRecord
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var err error
		found, err = tx.Get(documentNamespace("session_driver_checkpoints"), sessionID, &result)
		return err
	})
	return result, found, err
}

func (s *documentStore) SaveDriverCheckpoint(sessionID string, record DriverCheckpointRecord) error {
	sessionID, record.DriverID, record.DriverVersion = strings.TrimSpace(sessionID), strings.TrimSpace(record.DriverID), strings.TrimSpace(record.DriverVersion)
	if sessionID == "" || record.DriverID == "" || record.DriverVersion == "" || record.ContractVersion < 1 {
		return errors.New("complete driver checkpoint identity is required")
	}
	if len(record.State) == 0 || !json.Valid(record.State) {
		return errors.New("driver checkpoint state must be valid JSON")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var binding pluginprotocol.DriverBinding
		found, err := tx.Get(documentNamespace("session_driver_bindings"), sessionID, &binding)
		if err != nil {
			return err
		}
		if found && (binding.ContractVersion != record.ContractVersion || binding.DriverID != record.DriverID || binding.DriverVersion != record.DriverVersion) {
			return fmt.Errorf("driver checkpoint does not match session binding %+v", binding)
		}
		return tx.Put(documentNamespace("session_driver_checkpoints"), sessionID, record)
	})
}

func (s *documentStore) SetHistoryEngineTurnID(id string, seq int, ref, turnID string, pending bool) error {
	turnID = strings.TrimSpace(turnID)
	if strings.TrimSpace(ref) == "" || seq <= 0 {
		return errors.New("native turn boundary requires a persisted prompt and session")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		sess, found, err := getSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if !found || sess.EngineRef != ref {
			return errors.New("native turn boundary no longer matches the admitted prompt")
		}
		var record HistoryRecord
		found, err = tx.Get(documentNamespace("session_messages"), historyDocumentKey(id, seq), &record)
		if err != nil {
			return err
		}
		if !found || record.Role != "user" || record.EngineTurnID != "" && record.EngineTurnID != turnID {
			return errors.New("native turn boundary no longer matches the admitted prompt")
		}
		record.EngineTurnID, record.EngineTurnPending = turnID, pending
		return tx.Put(documentNamespace("session_messages"), historyDocumentKey(id, seq), record)
	})
}

// Receipts compare the exact admitted bytes. Keep the payload as bytes so the
// document envelope's JSON encoding cannot normalize it during persistence.
type modelReceiptDocument struct {
	OperationID     string
	ContractVersion int
	DriverID        string
	DriverVersion   string
	Payload         []byte
	CreatedAt       time.Time
}

func (s *documentStore) SaveModelInputReceipt(sessionID string, record ModelInputReceiptRecord) error {
	sessionID, record.OperationID = strings.TrimSpace(sessionID), strings.TrimSpace(record.OperationID)
	record.DriverID, record.DriverVersion = strings.TrimSpace(record.DriverID), strings.TrimSpace(record.DriverVersion)
	if sessionID == "" || record.OperationID == "" || record.DriverID == "" || record.DriverVersion == "" || record.ContractVersion < 1 {
		return errors.New("complete model input receipt identity is required")
	}
	if len(record.Payload) == 0 || !json.Valid(record.Payload) {
		return errors.New("model input receipt payload must be valid JSON")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		key := documentPrefix(sessionID) + record.OperationID
		var prior modelReceiptDocument
		found, err := tx.Get(documentNamespace("session_model_input_receipts"), key, &prior)
		if err != nil {
			return err
		}
		if found {
			if prior.ContractVersion != record.ContractVersion || prior.DriverID != record.DriverID || prior.DriverVersion != record.DriverVersion || !bytes.Equal(prior.Payload, record.Payload) {
				return fmt.Errorf("model input receipt %q changed after persistence", record.OperationID)
			}
		} else {
			stored := modelReceiptDocument{record.OperationID, record.ContractVersion, record.DriverID, record.DriverVersion, record.Payload, record.CreatedAt}
			if err := tx.Put(documentNamespace("session_model_input_receipts"), key, stored); err != nil {
				return err
			}
		}
		rows, err := readModelReceiptDocuments(tx, sessionID)
		if err != nil {
			return err
		}
		for i := 0; i < len(rows)-modelInputReceiptsPerSession; i++ {
			if err := tx.Delete(documentNamespace("session_model_input_receipts"), documentPrefix(sessionID)+rows[i].OperationID); err != nil {
				return err
			}
		}
		return nil
	})
}

func readModelReceiptDocuments(tx *persistence.Tx, sessionID string) ([]modelReceiptDocument, error) {
	rows, err := readDocumentRows[modelReceiptDocument](tx, "session_model_input_receipts", documentPrefix(sessionID))
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].OperationID < rows[j].OperationID
	})
	return rows, err
}

func (s *documentStore) LoadModelInputReceipts(sessionID string) ([]ModelInputReceiptRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("model input receipt session id is required")
	}
	var result []ModelInputReceiptRecord
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		rows, err := readModelReceiptDocuments(tx, sessionID)
		if err != nil {
			return err
		}
		result = nil
		for _, row := range rows {
			result = append(result, ModelInputReceiptRecord{row.OperationID, row.ContractVersion, row.DriverID, row.DriverVersion, json.RawMessage(row.Payload), row.CreatedAt})
		}
		return nil
	})
	return result, err
}
