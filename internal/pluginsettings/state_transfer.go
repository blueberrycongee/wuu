package pluginsettings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/storelock"
)

// Transferred records deliberately reject older hosts whose schema-1 writer
// would drop unknown fences and expose retained source backups again.
const transferredStateSchemaVersion = 2

// StateTransfer identifies a one-time transfer of a user-scoped storage prefix.
// The host must resolve and authorize both exact subject identities before use.
// IDs and prefixes must be nonempty valid storage keys. Ownership is permanent:
// the source retains hidden backup bytes but can no longer read or write them.
type StateTransfer struct {
	ID              string `json:"id"`
	SourceSubjectID string `json:"source_subject_id"`
	TargetSubjectID string `json:"target_subject_id"`
	KeyPrefix       string `json:"key_prefix"`
	Scope           Scope  `json:"scope"`
}

// MoveStatePrefix transfers raw string values without interpreting their payloads.
// Any existing target key under the prefix is a conflict, even an equal value.
// Validation completes before fencing the source. An interrupted commit remains
// fenced and can be retried with the identical transfer. A committed receipt
// prevents retries from replaying source backups over target updates or deletions.
func MoveStatePrefix(wuuHome, workspaceRoot string, transfer StateTransfer) error {
	return moveStatePrefix(wuuHome, workspaceRoot, transfer, nil)
}

type stateTransferStage string

const (
	beforeStateTransferFence       stateTransferStage = "before_fence"
	afterStateTransferFence        stateTransferStage = "after_fence"
	afterStateTransferTargetCommit stateTransferStage = "after_target_commit"
)

func moveStatePrefix(wuuHome, workspaceRoot string, transfer StateTransfer, checkpoint func(stateTransferStage) error) error {
	return moveStatePrefixContext(context.Background(), wuuHome, workspaceRoot, transfer, checkpoint)
}

func moveStatePrefixContext(ctx context.Context, wuuHome, workspaceRoot string, transfer StateTransfer, checkpoint func(stateTransferStage) error) error {
	if err := validateStateTransfer(transfer); err != nil {
		return err
	}
	sourcePath, storeDir, err := stateDocumentPath(wuuHome, workspaceRoot, transfer.SourceSubjectID, transfer.Scope)
	if err != nil {
		return err
	}
	targetPath, _, err := stateDocumentPath(wuuHome, workspaceRoot, transfer.TargetSubjectID, transfer.Scope)
	if err != nil {
		return err
	}
	if sourcePath == targetPath {
		return errors.New("plugin storage transfer subjects resolve to the same document")
	}
	// Ordinary writes use this same store-directory lock, including CAS.
	lock, err := storelock.AcquireContext(ctx, storeDir)
	if err != nil {
		return fmt.Errorf("lock plugin storage transfer: %w", err)
	}
	defer lock.Release() //nolint:errcheck -- the commit result is authoritative
	if err := checkStateInspection(storeDir, transfer.SourceSubjectID, transfer.TargetSubjectID); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	source, err := readStateRecord(sourcePath, transfer.SourceSubjectID)
	if err != nil {
		return err
	}
	target, err := readStateRecord(targetPath, transfer.TargetSubjectID)
	if err != nil {
		return err
	}
	sourceFenced, err := matchStateTransfer(source, transfer)
	if err != nil {
		return err
	}
	targetReceived, err := matchStateTransfer(target, transfer)
	if err != nil {
		return err
	}
	if targetReceived {
		if !sourceFenced {
			return errors.New("plugin storage transfer receipt has no matching source fence")
		}
		// Reassert the current target, never the backup, in case the previous
		// caller observed an error after rename but before durability confirmation.
		data, err := encodeStateRecord(target, transfer.TargetSubjectID)
		if err != nil {
			return err
		}
		return writeStateFile(targetPath, data)
	}
	for key := range target.Values {
		if strings.HasPrefix(key, transfer.KeyPrefix) {
			return fmt.Errorf("plugin storage transfer target prefix already contains key %q", key)
		}
	}
	if target.Values == nil {
		target.Values = make(map[string]string)
	}
	for key, value := range source.Values {
		if strings.HasPrefix(key, transfer.KeyPrefix) {
			target.Values[key] = value
		}
	}
	target.TransferReceipts = append(target.TransferReceipts, transfer)
	target.SchemaVersion = transferredStateSchemaVersion
	if !sourceFenced {
		source.TransferFences = append(source.TransferFences, transfer)
		source.SchemaVersion = transferredStateSchemaVersion
	}
	// Both complete documents, including bounded metadata, must fit before
	// the first ownership-changing write. A full target must not hide source data.
	sourceBytes, err := encodeStateRecord(source, transfer.SourceSubjectID)
	if err != nil {
		return err
	}
	targetBytes, err := encodeStateRecord(target, transfer.TargetSubjectID)
	if err != nil {
		return err
	}
	if checkpoint != nil {
		if err := checkpoint(beforeStateTransferFence); err != nil {
			return err
		}
	}
	// A fence is already effective while pending. Never roll it back on error:
	// target commit may have succeeded even when its caller saw an I/O failure.
	// Reassert a pending fence too: its previous write may have returned an
	// uncertain durability error after the rename became visible.
	if err := writeStateFile(sourcePath, sourceBytes); err != nil {
		return err
	}
	if checkpoint != nil {
		if err := checkpoint(afterStateTransferFence); err != nil {
			return err
		}
	}
	if err := writeStateFile(targetPath, targetBytes); err != nil {
		return err
	}
	if checkpoint != nil {
		return checkpoint(afterStateTransferTargetCommit)
	}
	return nil
}

func validateStateTransfer(transfer StateTransfer) error {
	if transfer.Scope != ScopeUser {
		return fmt.Errorf("plugin storage transfers require user scope, got %q", transfer.Scope)
	}
	if err := ValidateStateKey(transfer.ID); err != nil {
		return fmt.Errorf("invalid plugin storage transfer id: %w", err)
	}
	if err := ValidateStateKey(transfer.KeyPrefix); err != nil {
		return fmt.Errorf("invalid plugin storage transfer prefix: %w", err)
	}
	for _, subject := range []string{transfer.SourceSubjectID, transfer.TargetSubjectID} {
		if subject == "" || len(subject) > 256 || strings.TrimSpace(subject) != subject {
			return fmt.Errorf("invalid plugin storage transfer subject %q", subject)
		}
	}
	if transfer.SourceSubjectID == transfer.TargetSubjectID {
		return errors.New("plugin storage transfer source and target must differ")
	}
	return nil
}

func validateStateRecord(record stateRecord, pluginID string) error {
	if err := validateStateDocument(record.StateDocument, pluginID); err != nil {
		return err
	}
	metadataCount := len(record.TransferFences) + len(record.TransferReceipts)
	expectedVersion := SchemaVersion
	if metadataCount > 0 {
		expectedVersion = transferredStateSchemaVersion
	}
	if record.SchemaVersion != expectedVersion {
		return errors.New("plugin storage schema does not match ownership metadata")
	}
	if metadataCount > maxStateEntries {
		return fmt.Errorf("plugin storage contains more than %d transfer records", maxStateEntries)
	}
	seen := make([]StateTransfer, 0, len(record.TransferFences)+len(record.TransferReceipts))
	for _, group := range []struct {
		transfers []StateTransfer
		fence     bool
	}{{record.TransferFences, true}, {record.TransferReceipts, false}} {
		for _, transfer := range group.transfers {
			if err := validateStateTransfer(transfer); err != nil {
				return err
			}
			owner := transfer.TargetSubjectID
			if group.fence {
				owner = transfer.SourceSubjectID
			}
			if owner != pluginID {
				return errors.New("plugin storage transfer metadata ownership mismatch")
			}
			for _, previous := range seen {
				if stateTransfersConflict(previous, transfer) {
					return errors.New("plugin storage contains duplicate or overlapping transfer metadata")
				}
			}
			seen = append(seen, transfer)
		}
	}
	return nil
}

func matchStateTransfer(record stateRecord, transfer StateTransfer) (bool, error) {
	matched := false
	for _, group := range [][]StateTransfer{record.TransferFences, record.TransferReceipts} {
		for _, previous := range group {
			if previous == transfer {
				matched = true
			} else if stateTransfersConflict(previous, transfer) {
				return false, errors.New("plugin storage transfer conflicts with existing ownership metadata")
			}
		}
	}
	return matched, nil
}

func stateTransfersConflict(left, right StateTransfer) bool {
	return left.ID == right.ID || strings.HasPrefix(left.KeyPrefix, right.KeyPrefix) || strings.HasPrefix(right.KeyPrefix, left.KeyPrefix)
}
