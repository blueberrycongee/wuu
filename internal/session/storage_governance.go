package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/storagecodec"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// RedundantStorageMaintenanceResult reports redundant copies removed and
// checkpoint bytes encoded without changing the conversation or recovery.
type RedundantStorageMaintenanceResult struct {
	ProjectedToolResultsCleared  int64
	SupersededCheckpointsDeleted int64
	ToolMessageContentsCompacted int64
	CheckpointsEncoded           int64
	CheckpointBytesBefore        int64
	CheckpointBytesAfter         int64
}

// compactHistoryRecordForStorage removes the legacy text projection when the
// structured tool result can reproduce it exactly. Readers hydrate Content so
// callers continue to observe the same HistoryRecord.
func compactHistoryRecordForStorage(rec HistoryRecord) HistoryRecord {
	if !strings.EqualFold(strings.TrimSpace(rec.Role), "tool") || rec.Content == "" || len(rec.ToolResult) == 0 {
		return rec
	}
	var result toolresult.Result
	if json.Unmarshal(rec.ToolResult, &result) == nil && result.TextProjection() == rec.Content {
		rec.Content = ""
	}
	return rec
}

func hydrateHistoryRecordFromStorage(rec *HistoryRecord) {
	if rec == nil || rec.Content != "" || !strings.EqualFold(strings.TrimSpace(rec.Role), "tool") || len(rec.ToolResult) == 0 {
		return
	}
	var result toolresult.Result
	if json.Unmarshal(rec.ToolResult, &result) == nil {
		rec.Content = result.TextProjection()
	}
}

// MaintainRedundantStorage removes data whose authoritative copy has already
// crossed its durable handoff boundary. It does not prune conversation
// messages, current provider checkpoints, or unprojected tool invocations.
// Each pass also losslessly encodes a bounded batch of legacy checkpoints.
func MaintainRedundantStorage(ctx context.Context, sessDir string) (RedundantStorageMaintenanceResult, error) {
	var result RedundantStorageMaintenanceResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	db, err := openStore(sessDir)
	if err != nil {
		return result, err
	}
	defer db.Close()

	compactRowIDs, err := redundantToolMessageRowIDs(ctx, db)
	if err != nil {
		return result, err
	}

	checkpoints, err := checkpointStorageBatch(ctx, db)
	if err != nil {
		return result, err
	}

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin redundant session storage maintenance: %w", err)
	}
	defer tx.Rollback()

	cleared, err := tx.ExecContext(ctx, `
UPDATE tool_invocations
SET result_json = ''
WHERE projected_at > 0 AND result_json <> ''`)
	if err != nil {
		return result, fmt.Errorf("clear projected tool results: %w", err)
	}
	result.ProjectedToolResultsCleared, err = cleared.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("count cleared projected tool results: %w", err)
	}

	deleted, err := tx.ExecContext(ctx, `
DELETE FROM session_history_checkpoints AS checkpoint
WHERE EXISTS (
    SELECT 1 FROM session_history_checkpoints AS newer
    WHERE newer.session_id = checkpoint.session_id
      AND newer.version > checkpoint.version
)`)
	if err != nil {
		return result, fmt.Errorf("delete superseded history checkpoints: %w", err)
	}
	result.SupersededCheckpointsDeleted, err = deleted.RowsAffected()
	if err != nil {
		return result, fmt.Errorf("count deleted history checkpoints: %w", err)
	}

	if len(compactRowIDs) > 0 {
		statement, err := tx.PrepareContext(ctx, `
UPDATE session_messages
SET content = ''
WHERE rowid = ? AND role = 'tool' AND content <> '' AND tool_result_json <> ''`)
		if err != nil {
			return result, fmt.Errorf("prepare tool message compaction: %w", err)
		}
		defer statement.Close()
		for _, rowID := range compactRowIDs {
			updated, err := statement.ExecContext(ctx, rowID)
			if err != nil {
				return result, fmt.Errorf("compact tool message: %w", err)
			}
			count, err := updated.RowsAffected()
			if err != nil {
				return result, fmt.Errorf("count compacted tool message: %w", err)
			}
			result.ToolMessageContentsCompacted += count
		}
	}

	for _, checkpoint := range checkpoints {
		updated, err := tx.ExecContext(ctx, `
UPDATE session_history_checkpoints SET replacement_json = ?
WHERE session_id = ? AND version = ? AND typeof(replacement_json) = 'text'`,
			checkpoint.stored, checkpoint.sessionID, checkpoint.version)
		if err != nil {
			return result, fmt.Errorf("encode legacy history checkpoint: %w", err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("count encoded history checkpoints: %w", err)
		}
		result.CheckpointsEncoded += count
		result.CheckpointBytesBefore += count * checkpoint.bytesBefore
		result.CheckpointBytesAfter += count * int64(len(checkpoint.stored))
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit redundant session storage maintenance: %w", err)
	}
	return result, reclaimSessionStorage(ctx, db)
}

type storedCheckpoint struct {
	sessionID   string
	version     int
	bytesBefore int64
	stored      []byte
}

func checkpointStorageBatch(ctx context.Context, db *sql.DB) ([]storedCheckpoint, error) {
	rows, err := db.QueryContext(ctx, `
SELECT session_id, version, replacement_json
FROM session_history_checkpoints AS checkpoint
WHERE typeof(replacement_json) = 'text'
  AND NOT EXISTS (
    SELECT 1 FROM session_history_checkpoints AS newer
    WHERE newer.session_id = checkpoint.session_id AND newer.version > checkpoint.version
  )
ORDER BY checkpoint.rowid
LIMIT 32`)
	if err != nil {
		return nil, fmt.Errorf("list legacy history checkpoints: %w", err)
	}
	defer rows.Close()
	var batch []storedCheckpoint
	var bytesRead int
	for rows.Next() {
		var checkpoint storedCheckpoint
		var payload []byte
		if err := rows.Scan(&checkpoint.sessionID, &checkpoint.version, &payload); err != nil {
			return nil, fmt.Errorf("scan legacy history checkpoint: %w", err)
		}
		checkpoint.bytesBefore = int64(len(payload))
		checkpoint.stored, err = storagecodec.Encode(payload)
		if err != nil {
			return nil, fmt.Errorf("compress legacy history checkpoint: %w", err)
		}
		batch = append(batch, checkpoint)
		bytesRead += len(payload)
		// Finish the current checkpoint, then leave the rest for a later pass.
		if bytesRead >= 8*1024*1024 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy history checkpoints: %w", err)
	}
	return batch, nil
}

func redundantToolMessageRowIDs(ctx context.Context, db *sql.DB) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
SELECT rowid, content, tool_result_json
FROM session_messages
WHERE role = 'tool' AND content <> '' AND tool_result_json <> ''`)
	if err != nil {
		return nil, fmt.Errorf("list redundant tool message projections: %w", err)
	}
	defer rows.Close()
	var rowIDs []int64
	for rows.Next() {
		var rowID int64
		var content string
		var payload string
		if err := rows.Scan(&rowID, &content, &payload); err != nil {
			return nil, fmt.Errorf("scan redundant tool message projection: %w", err)
		}
		var result toolresult.Result
		if json.Unmarshal([]byte(payload), &result) == nil && result.TextProjection() == content {
			rowIDs = append(rowIDs, rowID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list redundant tool message projections: %w", err)
	}
	return rowIDs, nil
}
