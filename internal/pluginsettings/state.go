package pluginsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const (
	stateDirectory        = "plugin-storage"
	maxStateEntries       = 256
	maxStateDocumentBytes = maxDocumentBytes + MaxStateValueBytes
	MaxStateKeyBytes      = 128
	MaxStateValueBytes    = 1 << 20
)

type StateDocument struct {
	SchemaVersion int               `json:"schema_version"`
	PluginID      string            `json:"plugin_id"`
	Values        map[string]string `json:"values"`
}

func ValidateStateKey(key string) error {
	if len(key) > MaxStateKeyBytes || !safeNamePattern.MatchString(key) {
		return fmt.Errorf("invalid plugin storage key %q", key)
	}
	return nil
}

// ReadState returns only keys currently owned by the subject. Transferred
// prefixes are hidden as soon as their source fence is persisted.
func ReadState(wuuHome, workspaceRoot, pluginID string, scope Scope) (StateDocument, error) {
	path, _, err := stateDocumentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return StateDocument{}, err
	}
	record, err := readStateRecord(path, pluginID)
	if err != nil {
		return StateDocument{}, err
	}
	return visibleStateDocument(record), nil
}

// UpdateState serializes a read-modify-write transaction with transfers and
// other writers. The callback receives only visible keys; assignments under a
// transferred prefix are rejected, while hidden backups and receipts survive.
func UpdateState(wuuHome, workspaceRoot, pluginID string, scope Scope, update func(map[string]string) error) (StateDocument, error) {
	if update == nil {
		return StateDocument{}, errors.New("plugin storage update callback is required")
	}
	path, storeDir, err := stateDocumentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return StateDocument{}, err
	}
	lock, err := storelock.Acquire(storeDir)
	if err != nil {
		return StateDocument{}, fmt.Errorf("lock plugin storage: %w", err)
	}
	defer lock.Release() //nolint:errcheck -- the write result is authoritative
	record, err := readStateRecord(path, pluginID)
	if err != nil {
		return StateDocument{}, err
	}
	values := visibleStateDocument(record).Values
	if err := update(values); err != nil {
		return StateDocument{}, err
	}
	for key := range values {
		if stateKeyFenced(record, key) {
			return StateDocument{}, fmt.Errorf("plugin storage prefix for key %q has transferred ownership", key)
		}
	}
	// Callbacks never see backups. Preserve them after applying unrelated
	// writes, together with the fences and receipts that make retry safe.
	merged := make(map[string]string, len(values)+len(record.Values))
	for key, value := range values {
		merged[key] = value
	}
	for key, value := range record.Values {
		if stateKeyFenced(record, key) {
			merged[key] = value
		}
	}
	record.Values = merged
	data, err := encodeStateRecord(record, pluginID)
	if err != nil {
		return StateDocument{}, err
	}
	if err := writeStateFile(path, data); err != nil {
		return StateDocument{}, err
	}
	return visibleStateDocument(record), nil
}

func stateDocumentPath(wuuHome, workspaceRoot, pluginID string, scope Scope) (string, string, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" || len(pluginID) > 256 {
		return "", "", fmt.Errorf("invalid plugin id %q", pluginID)
	}
	if strings.TrimSpace(wuuHome) == "" {
		return "", "", errors.New("Wuu home is required")
	}
	var storeDir string
	switch scope {
	case ScopeUser:
		storeDir = filepath.Join(wuuHome, stateDirectory, string(scope))
	case ScopeWorkspace:
		if strings.TrimSpace(workspaceRoot) == "" {
			return "", "", errors.New("workspace root is required for workspace plugin storage")
		}
		workspaceDir, err := statepath.WorkspaceDir(wuuHome, workspaceRoot)
		if err != nil {
			return "", "", fmt.Errorf("resolve plugin storage workspace: %w", err)
		}
		storeDir = filepath.Join(workspaceDir, stateDirectory)
	default:
		return "", "", fmt.Errorf("unsupported plugin storage scope %q", scope)
	}
	return filepath.Join(storeDir, pluginDocumentName(pluginID)), storeDir, nil
}

// State metadata is private to the store; callers receive only visible values.
type stateRecord struct {
	StateDocument
	TransferFences   []StateTransfer `json:"transfer_fences,omitempty"`
	TransferReceipts []StateTransfer `json:"transfer_receipts,omitempty"`
}

func readStateRecord(path, pluginID string) (stateRecord, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stateRecord{StateDocument: StateDocument{SchemaVersion: SchemaVersion, PluginID: pluginID, Values: map[string]string{}}}, nil
	}
	if err != nil {
		return stateRecord{}, fmt.Errorf("read plugin storage: %w", err)
	}
	if len(data) > maxStateDocumentBytes {
		return stateRecord{}, errors.New("plugin storage document exceeds the supported limit")
	}
	var record stateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return stateRecord{}, fmt.Errorf("decode plugin storage: %w", err)
	}
	if err := validateStateRecord(record, pluginID); err != nil {
		return stateRecord{}, err
	}
	return record, nil
}

func encodeStateRecord(record stateRecord, pluginID string) ([]byte, error) {
	if err := validateStateRecord(record, pluginID); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode plugin storage: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxStateDocumentBytes {
		return nil, errors.New("plugin storage document exceeds the supported limit")
	}
	return data, nil
}

func visibleStateDocument(record stateRecord) StateDocument {
	document := record.StateDocument
	document.Values = make(map[string]string, len(record.Values))
	for key, value := range record.Values {
		if !stateKeyFenced(record, key) {
			document.Values[key] = value
		}
	}
	return document
}

func stateKeyFenced(record stateRecord, key string) bool {
	for _, transfer := range record.TransferFences {
		if strings.HasPrefix(key, transfer.KeyPrefix) {
			return true
		}
	}
	return false
}

func validateStateDocument(document StateDocument, pluginID string) error {
	if (document.SchemaVersion != SchemaVersion && document.SchemaVersion != transferredStateSchemaVersion) || document.PluginID != pluginID {
		return errors.New("plugin storage ownership or schema mismatch")
	}
	if len(document.Values) > maxStateEntries {
		return fmt.Errorf("plugin storage contains more than %d values", maxStateEntries)
	}
	for key, value := range document.Values {
		if err := ValidateStateKey(key); err != nil {
			return err
		}
		if len([]byte(value)) > MaxStateValueBytes {
			return fmt.Errorf("plugin storage value %q exceeds %d bytes", key, MaxStateValueBytes)
		}
	}
	return nil
}

// InspectState holds the same store lock as all mutations and transfers while
// a host checks authority and dispatches its bounded operation. The snapshot is
// read-only; callbacks must not call storage mutators or plugin RPCs.
func InspectState(wuuHome, workspaceRoot, pluginID string, scope Scope, inspect func(StateDocument) error) error {
	path, storeDir, err := stateDocumentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return err
	}
	lock, err := storelock.Acquire(storeDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	record, err := readStateRecord(path, pluginID)
	if err != nil {
		return err
	}
	return inspect(visibleStateDocument(record))
}
