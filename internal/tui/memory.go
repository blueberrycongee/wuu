package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type toolCallEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type memoryEntry struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	At         time.Time       `json:"at"`
	ToolCalls  []toolCallEntry `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

func loadMemoryEntries(path string) ([]transcriptEntry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Allow long markdown payloads in a single JSON line.
	scanner.Buffer(make([]byte, 1024), 2*1024*1024)

	entries := make([]transcriptEntry, 0, 64)
	line := 0
	for scanner.Scan() {
		line++
		payload := strings.TrimSpace(scanner.Text())
		if payload == "" {
			continue
		}
		var rec memoryEntry
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			return nil, fmt.Errorf("parse memory line %d: %w", line, err)
		}
		role := strings.ToUpper(strings.TrimSpace(rec.Role))
		if role == "" {
			role = "SYSTEM"
		}
		content := strings.TrimSpace(rec.Content)
		if content == "" {
			content = "(empty)"
		}
		entries = append(entries, transcriptEntry{
			Role:    role,
			Content: content,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan memory file: %w", err)
	}
	return entries, nil
}

func appendMemoryEntry(path string, entry transcriptEntry) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open memory file for append: %w", err)
	}
	defer file.Close()

	rec := memoryEntry{
		Role:    strings.ToLower(entry.Role),
		Content: entry.Content,
		At:      time.Now().UTC(),
	}
	if err := json.NewEncoder(file).Encode(rec); err != nil {
		return fmt.Errorf("write memory entry: %w", err)
	}
	return nil
}

func appendChatMessage(path string, msg providers.ChatMessage) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open memory file for append: %w", err)
	}
	defer file.Close()

	var tcs []toolCallEntry
	for _, tc := range msg.ToolCalls {
		tcs = append(tcs, toolCallEntry{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}

	rec := memoryEntry{
		Role:       strings.ToLower(msg.Role),
		Content:    msg.Content,
		At:         time.Now().UTC(),
		ToolCalls:  tcs,
		ToolCallID: msg.ToolCallID,
		Name:       msg.Name,
	}
	if err := json.NewEncoder(file).Encode(rec); err != nil {
		return fmt.Errorf("write chat message: %w", err)
	}
	return nil
}

func loadChatHistory(path string) ([]providers.ChatMessage, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 2*1024*1024)

	var msgs []providers.ChatMessage
	line := 0
	for scanner.Scan() {
		line++
		payload := strings.TrimSpace(scanner.Text())
		if payload == "" {
			continue
		}
		var rec memoryEntry
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			return nil, fmt.Errorf("parse memory line %d: %w", line, err)
		}

		var tcs []providers.ToolCall
		for _, tc := range rec.ToolCalls {
			tcs = append(tcs, providers.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}

		msgs = append(msgs, providers.ChatMessage{
			Role:       strings.ToLower(strings.TrimSpace(rec.Role)),
			Name:       rec.Name,
			Content:    rec.Content,
			ToolCallID: rec.ToolCallID,
			ToolCalls:  tcs,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan memory file: %w", err)
	}
	return msgs, nil
}
