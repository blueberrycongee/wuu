package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/storagecodec"
)

const (
	modelInputReceiptRetention     = 7 * 24 * time.Hour
	modelInputReceiptsPerSession   = 64
	modelInputReceiptCompressBatch = 32
)

type ModelInputReceiptMaintenanceResult struct {
	Deleted         int64
	Compressed      int64
	BytesBefore     int64
	BytesAfter      int64
	CompressionDone bool
}

type ModelInputReceiptRecord struct {
	OperationID     string
	ContractVersion int
	DriverID        string
	DriverVersion   string
	Payload         json.RawMessage
	CreatedAt       time.Time
}

func SaveModelInputReceipt(sessDir, sessionID string, record ModelInputReceiptRecord) error {
	sessionID = strings.TrimSpace(sessionID)
	record.OperationID = strings.TrimSpace(record.OperationID)
	record.DriverID = strings.TrimSpace(record.DriverID)
	record.DriverVersion = strings.TrimSpace(record.DriverVersion)
	if sessionID == "" || record.OperationID == "" || record.DriverID == "" || record.DriverVersion == "" || record.ContractVersion < 1 {
		return errors.New("complete model input receipt identity is required")
	}
	if len(record.Payload) == 0 || !json.Valid(record.Payload) {
		return errors.New("model input receipt payload must be valid JSON")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	storedPayload, err := storagecodec.Encode(record.Payload)
	if err != nil {
		return fmt.Errorf("compress model input receipt: %w", err)
	}

	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin model input receipt write: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, sessionID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	result, err := tx.Exec(`
INSERT OR IGNORE INTO session_model_input_receipts (
    session_id, operation_id, contract_version, driver_id, driver_version, receipt_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		record.OperationID,
		record.ContractVersion,
		record.DriverID,
		record.DriverVersion,
		storedPayload,
		timeText(record.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("write model input receipt: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect model input receipt write: %w", err)
	}
	if inserted == 0 {
		var contractVersion int
		var driverID, driverVersion string
		var storedExisting []byte
		if err := tx.QueryRow(`
SELECT contract_version, driver_id, driver_version, receipt_json
FROM session_model_input_receipts
WHERE session_id = ? AND operation_id = ?`, sessionID, record.OperationID).Scan(
			&contractVersion,
			&driverID,
			&driverVersion,
			&storedExisting,
		); err != nil {
			return fmt.Errorf("load existing model input receipt: %w", err)
		}
		existingPayload, err := storagecodec.Decode(storedExisting)
		if err != nil {
			return fmt.Errorf("decode existing model input receipt: %w", err)
		}
		if contractVersion != record.ContractVersion || driverID != record.DriverID || driverVersion != record.DriverVersion || !bytes.Equal(existingPayload, record.Payload) {
			return fmt.Errorf("model input receipt %q changed after persistence", record.OperationID)
		}
	}
	if _, err := tx.Exec(`
DELETE FROM session_model_input_receipts
WHERE rowid IN (
    SELECT rowid
    FROM session_model_input_receipts
    WHERE session_id = ?
    ORDER BY created_at DESC, operation_id DESC
    LIMIT -1 OFFSET ?
)`, sessionID, modelInputReceiptsPerSession); err != nil {
		return fmt.Errorf("enforce model input receipt session budget: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit model input receipt: %w", err)
	}
	return nil
}

func LoadModelInputReceipts(sessDir, sessionID string) ([]ModelInputReceiptRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("model input receipt session id is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if ok, err := sessionExistsDB(db, sessionID); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	rows, err := db.Query(`
SELECT operation_id, contract_version, driver_id, driver_version, receipt_json, created_at
FROM session_model_input_receipts
WHERE session_id = ?
ORDER BY created_at, operation_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load model input receipts: %w", err)
	}
	defer rows.Close()
	var records []ModelInputReceiptRecord
	for rows.Next() {
		var record ModelInputReceiptRecord
		var storedPayload []byte
		var createdAt string
		if err := rows.Scan(
			&record.OperationID,
			&record.ContractVersion,
			&record.DriverID,
			&record.DriverVersion,
			&storedPayload,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan model input receipt: %w", err)
		}
		payload, err := storagecodec.Decode(storedPayload)
		if err != nil {
			return nil, fmt.Errorf("decode model input receipt %q: %w", record.OperationID, err)
		}
		if !json.Valid(payload) {
			return nil, fmt.Errorf("decode model input receipt %q: payload is not valid JSON", record.OperationID)
		}
		record.Payload = json.RawMessage(payload)
		record.CreatedAt = parseTime(createdAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load model input receipts: %w", err)
	}
	return records, nil
}

// MaintainModelInputReceipts bounds diagnostic reconstruction data separately
// from durable conversation history. It never deletes session messages. Recent
// receipts remain exact and losslessly readable; older receipts and excess
// per-session snapshots are discarded because they are not part of resume or
// UI history.
func MaintainModelInputReceipts(ctx context.Context, sessDir string, now time.Time) (ModelInputReceiptMaintenanceResult, error) {
	var result ModelInputReceiptMaintenanceResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	db, err := openStore(sessDir)
	if err != nil {
		return result, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	deletedByAge, err := db.ExecContext(ctx, `
DELETE FROM session_model_input_receipts
WHERE created_at < ?`, timeText(now.UTC().Add(-modelInputReceiptRetention)))
	if err == nil {
		var deletedByCount sql.Result
		deletedByCount, err = db.ExecContext(ctx, `
WITH ranked AS (
    SELECT rowid,
           ROW_NUMBER() OVER (
               PARTITION BY session_id
               ORDER BY created_at DESC, operation_id DESC
           ) AS receipt_rank
    FROM session_model_input_receipts
)
DELETE FROM session_model_input_receipts
WHERE rowid IN (
    SELECT rowid FROM ranked WHERE receipt_rank > ?
)`, modelInputReceiptsPerSession)
		if err == nil {
			ageCount, ageErr := deletedByAge.RowsAffected()
			capCount, capErr := deletedByCount.RowsAffected()
			if ageErr != nil {
				err = ageErr
			} else if capErr != nil {
				err = capErr
			} else {
				result.Deleted = ageCount + capCount
			}
		}
	}
	storeWriteMu.Unlock()
	if err != nil {
		return result, fmt.Errorf("prune model input receipts: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		batch, err := compressModelInputReceiptBatch(ctx, db, modelInputReceiptCompressBatch)
		if err != nil {
			return result, err
		}
		result.Compressed += batch.Compressed
		result.BytesBefore += batch.BytesBefore
		result.BytesAfter += batch.BytesAfter
		if batch.CompressionDone {
			result.CompressionDone = true
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return result, reclaimSessionStorage(ctx, db)
}

func compressModelInputReceiptBatch(ctx context.Context, db *sql.DB, limit int) (ModelInputReceiptMaintenanceResult, error) {
	var result ModelInputReceiptMaintenanceResult
	if limit <= 0 {
		result.CompressionDone = true
		return result, nil
	}
	type pendingReceipt struct {
		rowID   int64
		payload []byte
	}

	rows, err := db.QueryContext(ctx, `
SELECT rowid, receipt_json
FROM session_model_input_receipts
WHERE typeof(receipt_json) = 'text'
ORDER BY created_at, operation_id
LIMIT ?`, limit)
	if err != nil {
		return result, fmt.Errorf("list model input receipts for compression: %w", err)
	}
	pending := make([]pendingReceipt, 0, limit)
	for rows.Next() {
		var receipt pendingReceipt
		if err := rows.Scan(&receipt.rowID, &receipt.payload); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan model input receipt for compression: %w", err)
		}
		pending = append(pending, receipt)
	}
	if err := rows.Close(); err != nil {
		return result, fmt.Errorf("close model input receipt compression scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("list model input receipts for compression: %w", err)
	}
	result.CompressionDone = len(pending) < limit
	type compressedReceipt struct {
		rowID  int64
		before int64
		stored []byte
	}
	compressed := make([]compressedReceipt, 0, len(pending))
	for _, receipt := range pending {
		stored, err := storagecodec.Encode(receipt.payload)
		if err != nil {
			return result, fmt.Errorf("compress stored model input receipt: %w", err)
		}
		compressed = append(compressed, compressedReceipt{
			rowID:  receipt.rowID,
			before: int64(len(receipt.payload)),
			stored: stored,
		})
	}

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin model input receipt compression: %w", err)
	}
	defer tx.Rollback()
	for _, receipt := range compressed {
		updated, err := tx.ExecContext(ctx, `
UPDATE session_model_input_receipts
SET receipt_json = ?
WHERE rowid = ? AND typeof(receipt_json) = 'text'`, receipt.stored, receipt.rowID)
		if err != nil {
			return result, fmt.Errorf("update compressed model input receipt: %w", err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("inspect compressed model input receipt update: %w", err)
		}
		if count == 0 {
			continue
		}
		result.Compressed += count
		result.BytesBefore += receipt.before
		result.BytesAfter += int64(len(receipt.stored))
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit model input receipt compression: %w", err)
	}
	return result, nil
}
