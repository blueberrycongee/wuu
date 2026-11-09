package engineusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var codexSessionIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// scanCodex reads ~/.codex rollout transcripts (sessions and archived_sessions).
// Each file is one thread; token_count event messages carry the per-request
// delta in last_token_usage. Wuu-bound threads are identified by the codex
// thread id recorded in session_meta, matching Session.EngineRef.
func (s *Scanner) scanCodex(ctx context.Context, exclude func(string, string) bool) ([]Row, error) {
	var rows []Row
	for _, root := range s.codexDirs() {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || ctx.Err() != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
				return nil
			}
			if exclude("codex", codexSessionRef(path)) {
				return nil
			}
			if cached, ok := s.cachedRows(path, info.Size(), info.ModTime()); ok {
				rows = append(rows, cached...)
				return nil
			}
			fileRows, sessionID, err := parseCodexFile(path)
			if err != nil {
				return nil
			}
			if exclude("codex", sessionID) {
				return nil
			}
			s.storeRows(path, info.Size(), info.ModTime(), fileRows)
			rows = append(rows, fileRows...)
			return nil
		})
		if err != nil {
			return rows, err
		}
	}
	return rows, nil
}

// codexSessionRef extracts the thread id from a rollout filename:
// rollout-<ts>-<uuid>.jsonl or a bare <uuid>.jsonl.
func codexSessionRef(path string) string {
	return codexSessionIDPattern.FindString(filepath.Base(path))
}

var codexTokenCountMarker = []byte(`"token_count"`)

func parseCodexFile(path string) ([]Row, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	sessionID := codexSessionRef(path)
	var rows []Row
	reader := bufio.NewReaderSize(f, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var row Row
			var ok bool
			sessionID, row, ok = parseCodexLine(line, sessionID)
			if ok {
				rows = append(rows, row)
			}
		}
		if err != nil {
			if err == io.EOF {
				return rows, sessionID, nil
			}
			return rows, sessionID, err
		}
	}
}

// parseCodexLine reads session_meta for the thread id and token_count events
// for usage. Codex reports input_tokens including cached and cache-write
// tokens, so the fresh-input split mirrors codexengine's own mapping.
func parseCodexLine(line []byte, sessionID string) (string, Row, bool) {
	if bytes.Contains(line, []byte(`"session_meta"`)) {
		var meta struct {
			Payload struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &meta) == nil {
			if id := strings.TrimSpace(meta.Payload.ID); id != "" {
				sessionID = id
			} else if id = strings.TrimSpace(meta.Payload.SessionID); id != "" {
				sessionID = id
			}
		}
		return sessionID, Row{}, false
	}
	if !bytes.Contains(line, codexTokenCountMarker) {
		return sessionID, Row{}, false
	}
	var event struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
			Info *struct {
				Last struct {
					InputTokens           int `json:"input_tokens"`
					CachedInputTokens     int `json:"cached_input_tokens"`
					CacheWriteTokens      int `json:"cache_write_input_tokens"`
					OutputTokens          int `json:"output_tokens"`
					ReasoningOutputTokens int `json:"reasoning_output_tokens"`
				} `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &event) != nil || event.Payload.Type != "token_count" || event.Payload.Info == nil {
		return sessionID, Row{}, false
	}
	last := event.Payload.Info.Last
	row := Row{
		Engine:              "codex",
		SessionID:           sessionID,
		InputTokens:         max(0, last.InputTokens-last.CachedInputTokens-last.CacheWriteTokens),
		OutputTokens:        last.OutputTokens,
		CacheCreationTokens: last.CacheWriteTokens,
		CacheReadTokens:     last.CachedInputTokens,
	}
	if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.Timestamp)); err == nil {
		row.At = at.UTC()
	}
	return sessionID, row, true
}
