package session

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// MaintainModelInputReceipts applies diagnostic retention to the captured
// provider. Physical encoding/reclamation belongs to that provider; this policy
// never prunes conversation history, checkpoints, or the legacy source archive.
func (s *documentStore) MaintainModelInputReceipts(now time.Time) (ModelInputReceiptMaintenanceResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.Add(-modelInputReceiptRetention)
	var result ModelInputReceiptMaintenanceResult
	err := s.transaction(func(tx *persistence.Tx) error {
		result = ModelInputReceiptMaintenanceResult{CompressionDone: true}
		groups := make(map[string][]struct {
			key         string
			createdAt   time.Time
			operationID string
		})
		query := persistence.Query{Namespace: documentNamespace("session_model_input_receipts"), Limit: 256}
		// Gather keys first: changing the pending write set while paging would
		// invalidate the transaction's read-your-writes cursor.
		for {
			view, err := tx.ReadView(query)
			if err != nil {
				return err
			}
			for _, document := range view.Documents {
				var receipt modelReceiptDocument
				if err := json.Unmarshal(document.Value, &receipt); err != nil {
					return err
				}
				prefix, _, _ := strings.Cut(document.Key, "/")
				groups[prefix] = append(groups[prefix], struct {
					key         string
					createdAt   time.Time
					operationID string
				}{document.Key, receipt.CreatedAt, receipt.OperationID})
			}
			if view.NextCursor == "" {
				break
			}
			query.Cursor = view.NextCursor
			query.AtRevision = &view.Revision
		}
		for _, rows := range groups {
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].createdAt.Equal(rows[j].createdAt) {
					return rows[i].operationID > rows[j].operationID
				}
				return rows[i].createdAt.After(rows[j].createdAt)
			})
			for i, row := range rows {
				if i < modelInputReceiptsPerSession && !row.createdAt.Before(cutoff) {
					continue
				}
				if err := tx.Delete(query.Namespace, row.key); err != nil {
					return err
				}
				result.Deleted++
			}
		}
		return nil
	})
	return result, err
}

// MaintainRedundantStorage removes only reproducible legacy text projections.
// Current checkpoint replacement already keeps one authoritative document;
// tool-ledger projection/retention is owned by the tool domain.
func (s *documentStore) MaintainRedundantStorage() (RedundantStorageMaintenanceResult, error) {
	var result RedundantStorageMaintenanceResult
	for {
		var done bool
		var compacted int64
		err := s.transaction(func(tx *persistence.Tx) error {
			compacted = 0
			var progress struct {
				After string `json:"after"`
				Done  bool   `json:"done"`
			}
			if _, err := tx.Get(documentNamespace("session_maintenance"), "legacy-projections-v1", &progress); err != nil {
				return err
			}
			done = progress.Done
			if done {
				return nil
			}
			query := persistence.Query{Namespace: documentNamespace("session_messages"), Limit: 128}
			if progress.After != "" {
				query.Start = progress.After + "\x00"
			}
			view, err := tx.ReadView(query)
			if err != nil {
				return err
			}
			for _, document := range view.Documents {
				var record HistoryRecord
				if err := json.Unmarshal(document.Value, &record); err != nil {
					// Legacy cleanup is best-effort; preserve unreadable originals.
					continue
				}
				updated := compactHistoryRecordForStorage(record)
				if updated.Content != record.Content {
					if err := tx.Put(query.Namespace, document.Key, updated); err != nil {
						return err
					}
					compacted++
				}
			}
			if len(view.Documents) > 0 {
				progress.After = view.Documents[len(view.Documents)-1].Key
			}
			progress.Done = view.NextCursor == ""
			done = progress.Done
			// Publish progress with this batch. A heartbeat retries only the batch,
			// and a restart resumes after it instead of scanning all history again.
			return tx.Put(documentNamespace("session_maintenance"), "legacy-projections-v1", progress)
		})
		if err != nil {
			return result, err
		}
		result.ToolMessageContentsCompacted += compacted
		if done {
			return result, nil
		}
	}
}
