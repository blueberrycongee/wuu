package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessiontrace"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func indexContextProjection(events []pluginhost.DataEvent) ([]pluginhost.DataEvent, error) {
	raw := make([]sessiontrace.RawEvent, len(events))
	for i, event := range events {
		raw[i] = sessiontrace.RawEvent(event)
	}
	projected, err := sessiontrace.LatestContextEvents(raw)
	if err != nil {
		return nil, serviceError("data_unavailable", "index context metadata is invalid")
	}
	result := make([]pluginhost.DataEvent, len(projected))
	for i, event := range projected {
		result[i] = pluginhost.DataEvent(event)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, serviceError("data_unavailable", "index context metadata is invalid")
	}
	if int64(len(encoded))+1024 > pluginhost.DataLatestContextMaxBytes {
		return nil, serviceError("limit_exceeded", "latest context exceeds the read budget")
	}
	return result, nil
}

func queryLatestNativeContext(ctx context.Context, stateDir, threadID string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := statepath.SessionArtifactDir(stateDir, threadID)
	result := pluginhost.DataQueryResult{Version: pluginhost.DataQueryServiceVersion, ThreadID: threadID, Events: []pluginhost.DataEvent{}}
	finish := func() (json.RawMessage, error) {
		if result.Events == nil {
			return nil, serviceError("data_unavailable", "selected context projection is missing")
		}
		if info, err := os.Stat(sessiontrace.Path(dir)); err == nil && info.Mode().IsRegular() {
			result.DiagnosticPath = sessiontrace.Path(dir)
		}
		encoded, err := marshalServiceResult(result)
		if int64(len(encoded)) > pluginhost.DataLatestContextMaxBytes {
			return nil, serviceError("limit_exceeded", "latest context exceeds the read budget")
		}
		return encoded, err
	}
	file, err := os.Open(filepath.Join(dir, nativeIndexFile))
	legacy := errors.Is(err, os.ErrNotExist)
	if legacy {
		file, err = os.Open(sessiontrace.Path(dir))
	}
	if errors.Is(err, os.ErrNotExist) {
		return finish()
	}
	if err != nil {
		return nil, serviceError("data_unavailable", "selected context index is unavailable")
	}
	defer file.Close()
	var version int
	if !legacy {
		bounded := &io.LimitedReader{R: file, N: pluginhost.DataLatestContextMaxBytes + 1}
		decoder := json.NewDecoder(bounded)
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, serviceError("data_unavailable", "selected context index is invalid")
		}
		token, err = decoder.Token()
		if err != nil || token != "version" || decoder.Decode(&version) != nil {
			return nil, serviceError("data_unavailable", "selected context index is invalid")
		}
		if version == 2 {
			token, err = decoder.Token()
			if err != nil || token != "latest_context" {
				return nil, serviceError("data_unavailable", "selected context projection is missing")
			}
			if err := decoder.Decode(&result.Events); err != nil {
				if bounded.N == 0 {
					return nil, serviceError("limit_exceeded", "latest context exceeds the read budget")
				}
				return nil, serviceError("data_unavailable", "selected context projection is invalid")
			}
			return finish()
		}
		if version != 1 {
			return nil, serviceError("data_unavailable", "selected context index version is invalid")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, serviceError("data_unavailable", "legacy context index is unavailable")
		}
	}
	// Older sources have no persisted projection. Compatibility is explicitly
	// bounded; the next normal append upgrades them without truncating history.
	raw, err := io.ReadAll(io.LimitReader(file, pluginhost.DataLatestContextMaxBytes+1))
	if err != nil {
		return nil, serviceError("data_unavailable", "legacy context index cannot be read")
	}
	if int64(len(raw)) > pluginhost.DataLatestContextMaxBytes {
		return nil, serviceError("limit_exceeded", "legacy context exceeds the read budget; a new indexed turn upgrades the source")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var events []pluginhost.DataEvent
	if !legacy {
		var document nativeIndexDocument
		if json.Unmarshal(raw, &document) != nil || document.Receipts == nil {
			return nil, serviceError("data_unavailable", "legacy context index is invalid")
		}
		events = document.Events
	} else {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var event pluginhost.DataEvent
			err := decoder.Decode(&event)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, serviceError("data_unavailable", "legacy context trace is invalid")
			}
			events = append(events, event)
		}
	}
	for i := range events {
		if events[i].ThreadID != "" && events[i].ThreadID != threadID {
			return nil, serviceError("data_unavailable", "legacy context scope is invalid")
		}
		events[i].ThreadID = threadID
	}
	result.Events, err = indexContextProjection(events)
	if err != nil {
		return nil, err
	}
	return finish()
}
