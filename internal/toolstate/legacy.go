package toolstate

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// NewLegacyConverter collects a single legacy snapshot. The caller supplies
// each table once, then calls it with an empty table name to validate parent
// references and emit all writes together in the migration transaction.
func NewLegacyConverter() func(string, []map[string]any) ([]persistence.Write, bool, error) {
	tables := make(map[string][]map[string]any)
	return func(table string, rows []map[string]any) ([]persistence.Write, bool, error) {
		if table != "" {
			if table != "tool_batches" && table != "tool_invocations" {
				return nil, false, nil
			}
			tables[table] = rows
			return nil, true, nil
		}
		batches := make(map[string]Batch)
		for _, row := range tables["tool_batches"] {
			var batch Batch
			if err := decodeLegacyRow(row, &batch); err != nil {
				return nil, true, err
			}
			if batch.ID == "" || batch.OwnerID == "" {
				return nil, true, fmt.Errorf("%w: legacy tool batch identity is missing", persistence.ErrCorrupt)
			}
			batch.InvocationIDs = []string{}
			if _, ok := batches[batch.ID]; ok {
				return nil, true, fmt.Errorf("%w: duplicate legacy tool batch %q", persistence.ErrCorrupt, batch.ID)
			}
			batches[batch.ID] = batch
		}
		invocations := make(map[string]Invocation)
		providerCalls := make(map[string]map[string]bool)
		for _, row := range tables["tool_invocations"] {
			var invocation Invocation
			if err := decodeLegacyRow(row, &invocation); err != nil {
				return nil, true, err
			}
			batch, ok := batches[invocation.BatchID]
			if !ok || invocation.ID == "" || invocation.ProviderCallID == "" {
				return nil, true, fmt.Errorf("%w: legacy tool invocation has no identity or batch", persistence.ErrCorrupt)
			}
			if _, ok := invocations[invocation.ID]; ok {
				return nil, true, fmt.Errorf("%w: duplicate legacy tool invocation %q", persistence.ErrCorrupt, invocation.ID)
			}
			if providerCalls[batch.ID] == nil {
				providerCalls[batch.ID] = make(map[string]bool)
			}
			if providerCalls[batch.ID][invocation.ProviderCallID] {
				return nil, true, fmt.Errorf("%w: duplicate legacy provider tool call", persistence.ErrCorrupt)
			}
			providerCalls[batch.ID][invocation.ProviderCallID] = true
			raw, ok := row["result_json"].(string)
			if !ok {
				return nil, true, fmt.Errorf("%w: legacy tool result must be text", persistence.ErrCorrupt)
			}
			if raw != "" {
				var result toolresult.Result
				if err := json.Unmarshal([]byte(raw), &result); err != nil {
					return nil, true, fmt.Errorf("decode legacy tool result %q: %w", invocation.ID, err)
				}
				invocation.Result = &result
			}
			invocations[invocation.ID] = invocation
			batch.InvocationIDs = append(batch.InvocationIDs, invocation.ID)
			batches[batch.ID] = batch
		}
		var writes []persistence.Write
		appendWrite := func(namespace, key string, value any) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			writes = append(writes, persistence.Write{Namespace: namespace, Key: key, Value: raw})
			return nil
		}
		for _, invocation := range invocations {
			if invocation.ParentInvocationID != "" {
				parent, ok := invocations[invocation.ParentInvocationID]
				if !ok || parent.ID == invocation.ID || batches[parent.BatchID].OwnerID != batches[invocation.BatchID].OwnerID {
					return nil, true, fmt.Errorf("%w: legacy nested invocation %q has no same-owner parent", persistence.ErrCorrupt, invocation.ID)
				}
			}
			if err := appendWrite(Invocations, invocation.ID, invocation); err != nil {
				return nil, true, err
			}
		}
		for _, batch := range batches {
			slices.SortFunc(batch.InvocationIDs, func(a, b string) int {
				if invocations[a].PreparedAt < invocations[b].PreparedAt {
					return -1
				}
				if invocations[a].PreparedAt > invocations[b].PreparedAt {
					return 1
				}
				return strings.Compare(a, b)
			})
			if err := appendWrite(batchesByOwner, batchOwnerKey(batch), batch.ID); err != nil {
				return nil, true, err
			}
			if err := appendWrite(Batches, batch.ID, batch); err != nil {
				return nil, true, err
			}
		}
		slices.SortFunc(writes, func(a, b persistence.Write) int {
			if n := strings.Compare(a.Namespace, b.Namespace); n != 0 {
				return n
			}
			return strings.Compare(a.Key, b.Key)
		})
		return writes, true, nil
	}
}

func decodeLegacyRow(row map[string]any, target any) error {
	fields := make(map[string]any, len(row))
	for key, value := range row {
		if key == "result_json" {
			continue
		}
		if key == "arguments_json" {
			key = "arguments"
		}
		fields[strings.ReplaceAll(key, "_", "")] = value
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
