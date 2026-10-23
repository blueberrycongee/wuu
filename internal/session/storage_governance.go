package session

import (
	"encoding/json"

	"strings"

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

type storedCheckpoint struct {
	sessionID   string
	version     int
	bytesBefore int64
	stored      []byte
}
