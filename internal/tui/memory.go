package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/jsonl"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type toolCallEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type imageEntry struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type memoryEntry struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Images           []imageEntry    `json:"images,omitempty"`
	At               time.Time       `json:"at"`
	ToolCalls        []toolCallEntry `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	InputTokens      int             `json:"input_tokens,omitempty"`
	OutputTokens     int             `json:"output_tokens,omitempty"`
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

	entries := make([]transcriptEntry, 0, 64)
	line := 0
	err = jsonl.ForEachLine(file, func(raw []byte) error {
		line++
		payload := bytes.TrimSpace(raw)
		if len(payload) == 0 {
			return nil
		}
		var rec memoryEntry
		if err := json.Unmarshal(payload, &rec); err != nil {
			return fmt.Errorf("parse memory line %d: %w", line, err)
		}
		role := strings.ToUpper(strings.TrimSpace(rec.Role))
		if role == "" {
			role = "SYSTEM"
		}
		if role == "META" {
			return nil
		}
		content := strings.TrimSpace(rec.Content)

		// Tool results: merge into the previous assistant entry's ToolCalls.
		if role == "TOOL" {
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i].Role == "ASSISTANT" {
					// Find matching tool call by ID and fill in the result.
					matched := false
					for j := range entries[i].ToolCalls {
						if entries[i].ToolCalls[j].ID == rec.ToolCallID {
							entries[i].ToolCalls[j].Result = content
							entries[i].ToolCalls[j].Status = ToolCallDone
							entries[i].ToolCalls[j].Collapsed = true
							matched = true
							break
						}
					}
					if !matched && rec.Name != "" {
						// Tool call wasn't tracked yet — add it.
						entries[i].ToolCalls = append(entries[i].ToolCalls, ToolCallEntry{
							ID:        rec.ToolCallID,
							Name:      rec.Name,
							Result:    content,
							Status:    ToolCallDone,
							Collapsed: true,
						})
					}
					break
				}
			}
			return nil // Don't create a separate entry for tool results.
		}

		if role == "USER" {
			content = formatUserEntryContent(content, len(rec.Images))
		} else if content == "" {
			content = "(empty)"
		}

		entry := transcriptEntry{
			Role:    role,
			Content: content,
		}

		// Restore tool calls from assistant messages.
		if role == "ASSISTANT" && len(rec.ToolCalls) > 0 {
			for _, tc := range rec.ToolCalls {
				entry.ToolCalls = append(entry.ToolCalls, ToolCallEntry{
					ID:        tc.ID,
					Name:      tc.Name,
					Args:      tc.Arguments,
					Status:    ToolCallDone,
					Collapsed: true,
				})
			}
		}
		if role == "ASSISTANT" && strings.TrimSpace(rec.ReasoningContent) != "" {
			entry.ThinkingContent = rec.ReasoningContent
			entry.ThinkingDone = true
		}

		entries = append(entries, entry)
		return nil
	})
	if err != nil {
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
		Role:             strings.ToLower(entry.Role),
		Content:          entry.Content,
		ReasoningContent: entry.ThinkingContent,
		At:               time.Now().UTC(),
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

	rec := memoryEntryFromChatMessage(msg)
	if err := json.NewEncoder(file).Encode(rec); err != nil {
		return fmt.Errorf("write chat message: %w", err)
	}
	return nil
}

func memoryEntryFromChatMessage(msg providers.ChatMessage) memoryEntry {
	var tcs []toolCallEntry
	for _, tc := range msg.ToolCalls {
		tcs = append(tcs, toolCallEntry{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}
	var imgs []imageEntry
	for _, image := range msg.Images {
		data := strings.TrimSpace(image.Data)
		if data == "" {
			continue
		}
		imgs = append(imgs, imageEntry{
			MediaType: image.MediaType,
			Data:      data,
		})
	}
	return memoryEntry{
		Role:             strings.ToLower(msg.Role),
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
		Images:           imgs,
		At:               time.Now().UTC(),
		ToolCalls:        tcs,
		ToolCallID:       msg.ToolCallID,
		Name:             msg.Name,
	}
}

func rewriteChatHistory(path string, msgs []providers.ChatMessage) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	metas, err := loadMetaEntries(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load existing meta entries: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("rewrite chat history: %w", err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	for _, msg := range msgs {
		if err := enc.Encode(memoryEntryFromChatMessage(msg)); err != nil {
			return fmt.Errorf("write chat history: %w", err)
		}
	}
	for _, rec := range metas {
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("write meta history: %w", err)
		}
	}
	return nil
}

// appendTokenUsage writes a meta record with token usage for the turn.
func appendTokenUsage(path string, inputTokens, outputTokens int) error {
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
		Role:         "meta",
		Content:      "token_usage",
		At:           time.Now().UTC(),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}
	return json.NewEncoder(file).Encode(rec)
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

	var msgs []providers.ChatMessage
	line := 0
	err = jsonl.ForEachLine(file, func(raw []byte) error {
		line++
		payload := bytes.TrimSpace(raw)
		if len(payload) == 0 {
			return nil
		}
		var rec memoryEntry
		if err := json.Unmarshal(payload, &rec); err != nil {
			return fmt.Errorf("parse memory line %d: %w", line, err)
		}
		role := strings.ToLower(strings.TrimSpace(rec.Role))
		switch role {
		case "user", "assistant", "tool":
		case "system":
			if !isConversationSummaryContent(rec.Content) {
				return nil
			}
		default:
			return nil
		}

		var tcs []providers.ToolCall
		for _, tc := range rec.ToolCalls {
			tcs = append(tcs, providers.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
		var imgs []providers.InputImage
		for _, image := range rec.Images {
			data := strings.TrimSpace(image.Data)
			if data == "" {
				continue
			}
			imgs = append(imgs, providers.InputImage{
				MediaType: image.MediaType,
				Data:      data,
			})
		}

		msgs = append(msgs, providers.ChatMessage{
			Role:             role,
			Name:             rec.Name,
			Content:          rec.Content,
			ReasoningContent: rec.ReasoningContent,
			Images:           imgs,
			ToolCallID:       rec.ToolCallID,
			ToolCalls:        tcs,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan memory file: %w", err)
	}
	return normalizeChatHistory(msgs), nil
}

func loadMetaEntries(path string) ([]memoryEntry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var metas []memoryEntry
	err = jsonl.ForEachLine(file, func(raw []byte) error {
		payload := bytes.TrimSpace(raw)
		if len(payload) == 0 {
			return nil
		}
		var rec memoryEntry
		if err := json.Unmarshal(payload, &rec); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
			metas = append(metas, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return metas, nil
}

func loadTokenUsageTotals(path string) (inputTokens, outputTokens int, err error) {
	metas, err := loadMetaEntries(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	for _, rec := range metas {
		if rec.Content != "token_usage" {
			continue
		}
		inputTokens += rec.InputTokens
		outputTokens += rec.OutputTokens
	}
	return inputTokens, outputTokens, nil
}

func isConversationSummaryContent(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "[Conversation summary]")
}
