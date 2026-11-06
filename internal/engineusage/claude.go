package engineusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// scanClaude reads ~/.claude/projects transcripts. Each assistant line carries
// the message's per-response usage; files are named <session-id>.jsonl, which
// is exactly the reference Wuu persists for claude threads.
func (s *Scanner) scanClaude(ctx context.Context, exclude func(string, string) bool) ([]Row, error) {
	root := s.claudeDir()
	if _, err := os.Stat(root); err != nil {
		return nil, nil
	}
	var rows []Row
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || ctx.Err() != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		if exclude("claude", strings.TrimSuffix(info.Name(), ".jsonl")) {
			return nil
		}
		if cached, ok := s.cachedRows(path, info); ok {
			rows = append(rows, cached...)
			return nil
		}
		fileRows, err := parseClaudeFile(path)
		if err != nil {
			return nil
		}
		s.storeRows(path, info, fileRows)
		rows = append(rows, fileRows...)
		return nil
	})
	return rows, err
}

func parseClaudeFile(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	var rows []Row
	reader := bufio.NewReaderSize(f, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if row, ok := parseClaudeLine(line, sessionID); ok {
				rows = append(rows, row)
			}
		}
		if err != nil {
			if err == io.EOF {
				return rows, nil
			}
			return rows, err
		}
	}
}

var claudeAssistantMarker = []byte(`"type":"assistant"`)

// parseClaudeLine extracts the usage of one assistant transcript line. Usage
// sits on message.usage with input/output and both cache directions; every
// recorded response is real spend, so lines left behind by history rewinds
// still count.
func parseClaudeLine(line []byte, sessionID string) (Row, bool) {
	if !bytes.Contains(line, claudeAssistantMarker) || !bytes.Contains(line, []byte(`"usage"`)) {
		return Row{}, false
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		Message   struct {
			Model string `json:"model"`
			Usage *struct {
				InputTokens         int `json:"input_tokens"`
				OutputTokens        int `json:"output_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Message.Usage == nil {
		return Row{}, false
	}
	row := Row{
		Engine:              "claude",
		SessionID:           sessionID,
		Model:               strings.TrimSpace(record.Message.Model),
		InputTokens:         record.Message.Usage.InputTokens,
		OutputTokens:        record.Message.Usage.OutputTokens,
		CacheCreationTokens: record.Message.Usage.CacheCreationTokens,
		CacheReadTokens:     record.Message.Usage.CacheReadTokens,
	}
	if sid := strings.TrimSpace(record.SessionID); sid != "" {
		row.SessionID = sid
	}
	if at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.Timestamp)); err == nil {
		row.At = at.UTC()
	}
	return row, true
}
