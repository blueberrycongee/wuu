package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/storelock"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	legacySideChatNamespace         = "wuu.sidethread.record.v1"
	legacySideChatDeletingNamespace = "wuu.sidethread.deleting.v1"
	legacySideChatReceiptNamespace  = "wuu.sidethread.legacy.v1"
)

type legacySideChat struct {
	ID        string                  `json:"side_thread_id"`
	ParentID  string                  `json:"main_thread_id"`
	EngineID  string                  `json:"engine_id"`
	EngineRef string                  `json:"engine_ref"`
	Status    string                  `json:"status"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
	Messages  []legacySideChatMessage `json:"messages"`
}

type legacySideChatMessage struct {
	ID           string    `json:"id"`
	Role         string    `json:"role"`
	Text         string    `json:"text"`
	Status       string    `json:"status"`
	ErrorText    string    `json:"error_message"`
	EngineTurnID string    `json:"engine_turn_id"`
	CreatedAt    time.Time `json:"created_at"`
	Items        []struct {
		ID           string                     `json:"id"`
		SourceID     string                     `json:"source_id"`
		Type         ThreadItemType             `json:"type"`
		Text         string                     `json:"text"`
		Name         string                     `json:"name"`
		Arguments    string                     `json:"arguments"`
		Result       string                     `json:"result"`
		Error        string                     `json:"error"`
		Display      *providers.ToolCallDisplay `json:"display"`
		ResultDetail *toolresult.Result         `json:"result_detail"`
	} `json:"items"`
	Selection *struct {
		Type     string                         `json:"type"`
		Response *providers.ResponseSelection   `json:"response"`
		File     *providers.FileSelectionSource `json:"file"`
	} `json:"selection"`
}

// Remove this migration after direct upgrades from the legacy side-chat store
// are no longer supported. Keep the old record until its new row is durable.
func migrateLegacySideChats(binding *runtime.State) error {
	ctx := context.Background()
	for _, namespace := range []string{legacySideChatNamespace, legacySideChatDeletingNamespace} {
		query := persistence.Query{Namespace: namespace, Limit: 128}
		for {
			view, err := binding.Documents.Read(ctx, query)
			if err != nil {
				return err
			}
			if len(view.Documents) == 0 {
				break
			}
			for _, document := range view.Documents {
				var old legacySideChat
				if err := json.Unmarshal(document.Value, &old); err != nil {
					return fmt.Errorf("decode legacy side chat %q: %w", document.Key, err)
				}
				if err := migrateLegacySideChat(binding.Sessions, old); err != nil {
					return fmt.Errorf("migrate legacy side chat %q: %w", document.Key, err)
				}
				if err := persistence.Update(ctx, binding.Documents, func(tx *persistence.Tx) error {
					return tx.Delete(namespace, document.Key)
				}); err != nil {
					return err
				}
			}
			query.Cursor = view.Documents[len(view.Documents)-1].Key
		}
	}
	return nil
}

func migrateLegacySideChat(store *session.Store, old legacySideChat) error {
	parent, found, err := store.Find(old.ParentID)
	if err != nil || !found || len(old.Messages) == 0 {
		return err
	}
	if old.ID == "" {
		return fmt.Errorf("legacy side chat has no session ID")
	}
	if existing, exists, err := store.Find(old.ID); err != nil {
		return err
	} else if exists {
		if existing.ForkedFromID == parent.ID {
			return nil
		}
		return fmt.Errorf("legacy side chat ID %q belongs to another session", old.ID)
	}

	var records []session.HistoryRecord
	var summary string
	for _, message := range old.Messages {
		switch message.Role {
		case "user":
			if summary == "" {
				summary = strings.TrimSpace(message.Text)
			}
			record := session.HistoryRecord{Role: "user", Content: message.Text, DisplayID: message.ID, At: message.CreatedAt, EngineTurnID: message.EngineTurnID}
			if message.Selection != nil {
				part := providers.MessageContentPart{Type: "text", Text: message.Text}
				var quote providers.MessageContentPart
				switch message.Selection.Type {
				case "response":
					quote = providers.MessageContentPart{Type: "response_selection", Selection: message.Selection.Response}
				case "file":
					quote = providers.MessageContentPart{Type: "file_selection", Source: message.Selection.File, Intent: "quote"}
				}
				if quote.Type != "" {
					record.ContentParts, err = json.Marshal([]providers.MessageContentPart{quote, part})
					if err != nil {
						return err
					}
				}
			}
			records = append(records, record)
		case "assistant":
			for _, item := range message.Items {
				switch item.Type {
				case ThreadItemReasoning:
					records = append(records, session.HistoryRecord{Role: "assistant", ReasoningContent: item.Text, DisplayID: item.ID, At: message.CreatedAt})
				case ThreadItemToolCall:
					callID := firstNonEmpty(item.SourceID, item.ID)
					result := item.Result
					if result == "" {
						result = item.Error
					}
					call, err := json.Marshal([]providers.ToolCall{{ID: callID, Name: item.Name, Arguments: item.Arguments, Display: item.Display}})
					if err != nil {
						return err
					}
					detail, err := json.Marshal(item.ResultDetail)
					if err != nil {
						return err
					}
					records = append(records,
						session.HistoryRecord{Role: "assistant", ToolCalls: call, At: message.CreatedAt},
						session.HistoryRecord{Role: "tool", ToolCallID: callID, Name: item.Name, Content: result, ToolResult: detail, At: message.CreatedAt})
				}
			}
			if message.Text != "" {
				records = append(records, session.HistoryRecord{Role: "assistant", Content: message.Text, Phase: string(providers.MessagePhaseFinalAnswer), DisplayID: message.ID, At: message.CreatedAt})
			}
			status := message.Status
			if status == "streaming" {
				status = string(TurnStatusInterrupted)
			}
			if status == string(TurnStatusFailed) || status == string(TurnStatusInterrupted) {
				records = append(records, session.HistoryRecord{Role: "meta", Content: turnTerminalHistoryRecord, StopReason: status, DisplayContent: message.ErrorText, At: message.CreatedAt})
			}
		}
	}
	if old.Status == "running" && old.Messages[len(old.Messages)-1].Role == "user" {
		records = append(records, session.HistoryRecord{Role: "meta", Content: turnTerminalHistoryRecord, StopReason: string(TurnStatusInterrupted), At: old.UpdatedAt})
	}
	engineRef := ""
	if old.Status != "running" && agentengine.NormalizeEngineID(old.EngineID) == agentengine.NormalizeEngineID(parent.EngineID) {
		engineRef = old.EngineRef
	}
	_, err = store.CreateInitialized(session.Session{
		ID: old.ID, ParentID: parent.ID, Placement: session.PlacementSide, ForkedFromID: parent.ID,
		CWD: parent.CWD, WorkspaceID: parent.WorkspaceID, EngineID: parent.EngineID, EngineRef: engineRef,
		Provider: parent.Provider, Model: parent.Model, Variant: parent.Variant, Effort: parent.Effort, Speed: parent.Speed,
		ModelOptions: parent.ModelOptions, PermissionMode: parent.PermissionMode, ApproveForMe: parent.ApproveForMe,
		Instructions: parent.Instructions, ToolPolicyJSON: parent.ToolPolicyJSON,
		Summary: summary, Entries: len(records), CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt,
	}, records)
	return err
}

// The previous store left imported JSON in place. Keep its receipts to avoid
// reimporting closed, reset, or deleted conversations from those files.
func importLegacySideChatFiles(binding *runtime.State, dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := storelock.Acquire(dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	ctx := context.Background()
	for _, staged := range []bool{false, true} {
		source, namespace := dir, legacySideChatNamespace
		if staged {
			source, namespace = filepath.Join(dir, ".deleting"), legacySideChatDeletingNamespace
		}
		entries, err := os.ReadDir(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			key := strings.TrimSuffix(entry.Name(), ".json")
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || key == "" {
				return fmt.Errorf("invalid legacy side chat file %q", entry.Name())
			}
			if err := persistence.Update(ctx, binding.Documents, func(tx *persistence.Tx) error {
				var receipt json.RawMessage
				if found, err := tx.Get(legacySideChatReceiptNamespace, key, &receipt); err != nil || found {
					return err
				}
				raw, err := os.ReadFile(filepath.Join(source, entry.Name()))
				if err != nil {
					return err
				}
				var old legacySideChat
				if err := json.Unmarshal(raw, &old); err != nil {
					return err
				}
				if old.ParentID != key || old.ID == "" {
					return fmt.Errorf("legacy side chat identity does not match %q", key)
				}
				for _, existing := range []string{legacySideChatNamespace, legacySideChatDeletingNamespace} {
					var record legacySideChat
					if found, err := tx.Get(existing, key, &record); err != nil {
						return err
					} else if found {
						if record.ID != old.ID {
							return fmt.Errorf("legacy side chat %q conflicts with selected history", key)
						}
						return tx.Put(legacySideChatReceiptNamespace, key, map[string]any{"digest": legacySideChatDigest(raw), "staged": staged})
					}
				}
				if err := tx.Put(namespace, key, json.RawMessage(raw)); err != nil {
					return err
				}
				return tx.Put(legacySideChatReceiptNamespace, key, map[string]any{"digest": legacySideChatDigest(raw), "staged": staged})
			}); err != nil {
				return fmt.Errorf("import legacy side chat %q: %w", key, err)
			}
		}
	}
	return nil
}

func legacySideChatDigest(raw []byte) string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	_ = decoder.Decode(&value) // The caller already decoded and validated this document.
	canonical, _ := json.Marshal(value)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
