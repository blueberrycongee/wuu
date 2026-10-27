package session

import (
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func inferenceIndexPrefix(scope, status string) string {
	state := "terminal"
	if status == "active" {
		state = "active"
	}
	return documentPrefix(scope) + state + "/"
}

func inferenceIndexKey(scope, status string, stamp int64, id string) string {
	return inferenceIndexPrefix(scope, status) + fmt.Sprintf("%020d", uint64(stamp)^(1<<63)) + "/" + id
}

func putInferenceOperation(tx *persistence.Tx, record inferenceOperationDocument) error {
	return putInferenceIndexedDocument(tx, "inference_operations", record.ID, record.WorkspaceScope, record.Status, record.UpdatedAt, record)
}

func putInferenceWorkflow(tx *persistence.Tx, record inferenceWorkflowDocument) error {
	return putInferenceIndexedDocument(tx, "inference_workflows", record.ID, record.WorkspaceScope, record.Status, record.UpdatedAt, record)
}

func putInferenceIndexedDocument(tx *persistence.Tx, table, id, scope, status string, stamp int64, record any) error {
	var previous struct {
		WorkspaceScope, Status string
		UpdatedAt              int64
	}
	found, err := tx.Get(documentNamespace(table), id, &previous)
	if err != nil {
		return err
	}
	if found {
		tx.Delete(documentNamespace(table+"_index"), inferenceIndexKey(previous.WorkspaceScope, previous.Status, previous.UpdatedAt, id))
	}
	if err := tx.Put(documentNamespace(table+"_index"), inferenceIndexKey(scope, status, stamp, id), id); err != nil {
		return err
	}
	return tx.Put(documentNamespace(table), id, record)
}

// Read index pages at one revision before callers mutate their membership.
// A terminal cutoff is exclusive and is applied by the storage provider.
func readInferenceIndexedDocuments[T any](tx *persistence.Tx, table, scope, status string, cutoff *int64) ([]T, error) {
	prefix := inferenceIndexPrefix(scope, status)
	query := persistence.Query{Namespace: documentNamespace(table + "_index"), Prefix: prefix, Limit: 256}
	if cutoff != nil {
		query.End = prefix + fmt.Sprintf("%020d", uint64(*cutoff)^(1<<63)) + "/"
	}
	var result []T
	for {
		page, err := tx.ReadView(query)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Documents {
			var id string
			if err := json.Unmarshal(row.Value, &id); err != nil {
				return nil, err
			}
			record, err := requireInferenceDocument[T](tx, table, id)
			if err != nil {
				return nil, err
			}
			result = append(result, record)
		}
		if page.NextCursor == "" {
			return result, nil
		}
		query.Cursor = page.NextCursor
		query.AtRevision = &page.Revision
	}
}
