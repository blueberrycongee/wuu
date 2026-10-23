package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

// The authoritative document and receipts commit together. JSONL is a legacy
// import source until this exists, and a diagnostic export afterward.
type nativeIndexDocument struct {
	Version       int                    `json:"version"`
	LatestContext []pluginhost.DataEvent `json:"latest_context"`
	Events        []pluginhost.DataEvent `json:"events"`
	Receipts      map[string]string      `json:"receipts"`
}

const nativeIndexFile = "turn-index.json"

func readNativeIndex(path string) (nativeIndexDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nativeIndexDocument{}, err
	}
	var document nativeIndexDocument
	if json.Unmarshal(raw, &document) != nil || (document.Version != 1 && document.Version != 2) || document.Receipts == nil {
		return nativeIndexDocument{}, serviceError("data_unavailable", "selected index document is invalid")
	}
	return document, nil
}

func queryNativeIndex(ctx context.Context, stateDir string, query pluginhost.DataQueryParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := statepath.SessionArtifactDir(stateDir, query.ThreadID)
	file, err := os.Open(filepath.Join(dir, nativeIndexFile))
	if errors.Is(err, os.ErrNotExist) {
		return querySessionTrace(ctx, stateDir, query)
	}
	if err != nil {
		return nil, serviceError("data_unavailable", "selected index cannot be read")
	}
	defer file.Close()
	result := pluginhost.DataQueryResult{Version: pluginhost.DataQueryServiceVersion, ThreadID: query.ThreadID, Events: []pluginhost.DataEvent{}}
	finish := func() (json.RawMessage, error) {
		if info, err := os.Stat(sessiontrace.Path(dir)); err == nil && info.Mode().IsRegular() {
			result.DiagnosticPath = sessiontrace.Path(dir)
		}
		return marshalServiceResult(result)
	}
	invalid := serviceError("data_unavailable", "selected index document is invalid")
	decoder := json.NewDecoder(file)
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	var version int
	var eventsSeen, receiptsSeen bool
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		switch token {
		case "version":
			if decoder.Decode(&version) != nil || (version != 1 && version != 2) {
				return nil, invalid
			}
		case "latest_context":
			var projection json.RawMessage
			if version != 2 || decoder.Decode(&projection) != nil {
				return nil, invalid
			}
		case "events":
			if (version != 1 && version != 2) || eventsSeen {
				return nil, invalid
			}
			eventsSeen = true
			if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
				return nil, invalid
			}
			for decoder.More() {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				var event pluginhost.DataEvent
				if decoder.Decode(&event) != nil || event.ThreadID != query.ThreadID {
					return nil, invalid
				}
				if len(pluginhost.FilterDataEvents([]pluginhost.DataEvent{event}, query)) == 0 {
					continue
				}
				result.Events = append(result.Events, event)
				// Stop decoding at the storage boundary. A limited query never loads or
				// validates the remaining event stream or its receipt index.
				if query.Limit > 0 && len(result.Events) >= query.Limit {
					return finish()
				}
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
				return nil, invalid
			}
		case "receipts":
			var receipts map[string]string
			if decoder.Decode(&receipts) != nil || receipts == nil {
				return nil, invalid
			}
			receiptsSeen = true
		default:
			return nil, invalid
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || (version != 1 && version != 2) || !eventsSeen || !receiptsSeen {
		return nil, invalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid
	}
	return finish()
}

func appendNativeIndexTurn(ctx context.Context, stateDir string, batch pluginhost.DataAppendTurnParams) (pluginhost.DataAppendTurnResult, error) {
	receipt := pluginhost.DataAppendTurnResult{Version: pluginhost.DataQueryServiceVersion, ThreadID: batch.ThreadID, TurnID: batch.TurnID}
	dir := statepath.SessionArtifactDir(stateDir, batch.ThreadID)
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return receipt, serviceError("index_unavailable", "selected index lock is unavailable")
	}
	defer lock.Release()
	document, err := readNativeIndex(filepath.Join(dir, nativeIndexFile))
	imported := errors.Is(err, os.ErrNotExist)
	if imported {
		document = nativeIndexDocument{Version: 1, Events: []pluginhost.DataEvent{}, Receipts: map[string]string{}}
		old, readErr := sessiontrace.ReadEvents(sessiontrace.Path(dir))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return receipt, serviceError("data_unavailable", "legacy index source is invalid or unavailable")
		}
		groups := map[string][]pluginhost.DataEvent{}
		for _, raw := range old {
			event := pluginhost.DataEvent(raw)
			if event.ThreadID != "" && event.ThreadID != batch.ThreadID {
				return receipt, serviceError("data_unavailable", "legacy index source has an invalid scope")
			}
			event.ThreadID = batch.ThreadID
			document.Events = append(document.Events, event)
			if event.TurnID != "" {
				groups[event.TurnID] = append(groups[event.TurnID], event)
			}
		}
		for turn, events := range groups {
			document.Receipts[turn] = indexBatchDigest(events)
		}
	} else if err != nil {
		return receipt, serviceError("data_unavailable", "selected index cannot be read")
	}
	upgraded := document.Version == 1
	if upgraded {
		projection, err := indexContextProjection(document.Events)
		if err != nil {
			return receipt, err
		}
		document.LatestContext = projection
		document.Version = 2
	}
	digest := indexBatchDigest(batch.Events)
	if previous, exists := document.Receipts[batch.TurnID]; exists {
		if previous != digest {
			return receipt, serviceError("index_conflict", "turn index already contains a different batch")
		}
		receipt.Duplicate = true
	} else {
		projection, err := indexContextProjection(batch.Events)
		if err != nil {
			return receipt, err
		}
		if len(projection) > 0 {
			document.LatestContext = projection
		}
		document.Events = append(document.Events, batch.Events...)
		document.Receipts[batch.TurnID] = digest
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return receipt, serviceError("invalid_params", "turn index batch is invalid")
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if imported || upgraded || !receipt.Duplicate {
		if err := securefs.WriteFileAtomic(filepath.Join(dir, nativeIndexFile), encoded); err != nil {
			return receipt, serviceError("index_unavailable", "index commit failed; the batch may have been applied")
		}
	}
	receipt.Committed = true
	// Export is best effort after the authoritative atomic commit. Its failure
	// never authorizes replaying an already committed append.
	var export bytes.Buffer
	encoder := json.NewEncoder(&export)
	for _, event := range document.Events {
		if err := encoder.Encode(event); err != nil {
			return receipt, nil
		}
	}
	if err := securefs.WriteFileAtomic(sessiontrace.Path(dir), export.Bytes()); err == nil {
		receipt.DiagnosticPath = sessiontrace.Path(dir)
	}
	return receipt, nil
}
func indexBatchDigest(events []pluginhost.DataEvent) string {
	raw, _ := json.Marshal(events)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
