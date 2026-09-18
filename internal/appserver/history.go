package appserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	sessionstore "github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type persistedToolCall struct {
	ID                   string                     `json:"id"`
	ProviderItemID       string                     `json:"provider_item_id,omitempty"`
	ProviderItemProvider string                     `json:"provider_item_provider,omitempty"`
	ProviderItemModel    string                     `json:"provider_item_model,omitempty"`
	Name                 string                     `json:"name"`
	Arguments            string                     `json:"arguments"`
	Kind                 string                     `json:"kind,omitempty"`
	Display              *providers.ToolCallDisplay `json:"display,omitempty"`
}

type persistedImage struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Width     uint32 `json:"width,omitempty"`
	Height    uint32 `json:"height,omitempty"`
}

type persistedFile struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	Filename  string `json:"filename,omitempty"`
}

type persistedMessage struct {
	// Seq is the message's stable per-thread address. It is not serialized to
	// the store's message columns because seq is the row's own key.
	Seq                 int                                `json:"seq,omitempty"`
	Role                string                             `json:"role"`
	Content             string                             `json:"content"`
	DisplayContent      string                             `json:"display_content,omitempty"`
	Origin              string                             `json:"origin,omitempty"`
	OriginID            string                             `json:"origin_id,omitempty"`
	Cause               string                             `json:"cause,omitempty"`
	PresentationKind    string                             `json:"presentation_kind,omitempty"`
	RelatedSessionID    string                             `json:"related_session_id,omitempty"`
	ReadOnly            bool                               `json:"read_only,omitempty"`
	Phase               string                             `json:"phase,omitempty"`
	ProviderItemID      string                             `json:"provider_item_id,omitempty"`
	ProviderItemModel   string                             `json:"provider_item_model,omitempty"`
	ClientID            string                             `json:"client_id,omitempty"`
	Hidden              bool                               `json:"hidden,omitempty"`
	Steered             bool                               `json:"steered,omitempty"`
	ReasoningContent    string                             `json:"reasoning_content,omitempty"`
	ReasoningBlocks     []providers.ReasoningBlock         `json:"reasoning_blocks,omitempty"`
	ProviderItems       []providers.ProviderItem           `json:"provider_items,omitempty"`
	ContentParts        []providers.MessageContentPart     `json:"content_parts,omitempty"`
	Images              []persistedImage                   `json:"images,omitempty"`
	Files               []persistedFile                    `json:"files,omitempty"`
	ToolCalls           []persistedToolCall                `json:"tool_calls,omitempty"`
	DiscoveredTools     []providers.LoadableToolDefinition `json:"discovered_tools,omitempty"`
	ToolCallID          string                             `json:"tool_call_id,omitempty"`
	ToolInvocationID    string                             `json:"tool_invocation_id,omitempty"`
	ToolResultKind      string                             `json:"tool_result_kind,omitempty"`
	ToolResult          *toolresult.Result                 `json:"tool_result,omitempty"`
	FinishReason        string                             `json:"finish_reason,omitempty"`
	StopReason          string                             `json:"stop_reason,omitempty"`
	Truncated           bool                               `json:"truncated,omitempty"`
	Name                string                             `json:"name,omitempty"`
	At                  time.Time                          `json:"at,omitempty"`
	InputTokens         int                                `json:"input_tokens,omitempty"`
	OutputTokens        int                                `json:"output_tokens,omitempty"`
	ContextTokens       int                                `json:"context_tokens,omitempty"`
	CacheCreationTokens int                                `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int                                `json:"cache_read_tokens,omitempty"`
	// RetryCount and MaxRetries carry the final retry counters of a
	// stream_reconnect meta row. Zero for all other record types.
	RetryCount int `json:"retry_count,omitempty"`
	MaxRetries int `json:"max_retries,omitempty"`
	// Provider carries native-state provenance for chat rows and token-usage
	// provenance for meta rows. Model is currently used by token-usage rows;
	// provider-native chat state keeps its model in ProviderItemModel.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type persistedAgentHistory struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	TaskName     string                  `json:"task_name,omitempty"`
	AgentProfile string                  `json:"agent_profile,omitempty"`
	AgentPath    string                  `json:"agent_path,omitempty"`
	ParentID     string                  `json:"parent_id,omitempty"`
	Description  string                  `json:"description"`
	Status       string                  `json:"status"`
	StartedAt    time.Time               `json:"started_at"`
	CompletedAt  time.Time               `json:"completed_at"`
	Model        string                  `json:"model"`
	Prompt       string                  `json:"prompt"`
	Result       string                  `json:"result,omitempty"`
	Error        string                  `json:"error,omitempty"`
	Messages     []providers.ChatMessage `json:"messages,omitempty"`
}

func loadChatMessages(sessDir, id string) ([]providers.ChatMessage, error) {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	records, _, err := loadProviderPersistedMessages(sessDir, id, false)
	if err != nil {
		return nil, err
	}
	return chatMessagesFromPersistedMessages(records), nil
}

func chatMessagesFromPersistedMessages(records []persistedMessage) []providers.ChatMessage {
	var messages []providers.ChatMessage
	for _, rec := range records {
		role := strings.ToLower(strings.TrimSpace(rec.Role))
		if role == "" || role == "meta" {
			continue
		}
		msg := providers.ChatMessage{
			Seq:                  rec.Seq,
			Role:                 role,
			Name:                 rec.Name,
			ClientID:             rec.ClientID,
			Content:              rec.Content,
			DisplayContent:       rec.DisplayContent,
			Origin:               rec.Origin,
			OriginID:             rec.OriginID,
			Cause:                rec.Cause,
			PresentationKind:     rec.PresentationKind,
			RelatedSessionID:     rec.RelatedSessionID,
			ReadOnly:             rec.ReadOnly,
			Phase:                providers.NormalizeMessagePhase(rec.Phase),
			Hidden:               rec.Hidden,
			ProviderItemID:       rec.ProviderItemID,
			ProviderItemProvider: rec.Provider,
			ProviderItemModel:    rec.ProviderItemModel,
			Steered:              rec.Steered,
			ReasoningContent:     rec.ReasoningContent,
			ReasoningBlocks:      append([]providers.ReasoningBlock(nil), rec.ReasoningBlocks...),
			ProviderItems:        append([]providers.ProviderItem(nil), rec.ProviderItems...),
			ContentParts:         append([]providers.MessageContentPart(nil), rec.ContentParts...),
			ToolCallID:           rec.ToolCallID,
			ToolInvocationID:     rec.ToolInvocationID,
			ToolResultKind:       providers.NormalizeToolCallKind(rec.ToolResultKind),
			ToolResult:           cloneToolResult(rec.ToolResult),
			FinishReason:         providers.FinishReason(strings.TrimSpace(rec.FinishReason)),
			StopReason:           strings.ToLower(strings.TrimSpace(rec.StopReason)),
			Truncated:            rec.Truncated,
			DiscoveredTools:      providers.CloneLoadableToolDefinitions(rec.DiscoveredTools),
		}
		for _, image := range rec.Images {
			if strings.TrimSpace(image.Data) == "" {
				continue
			}
			msg.Images = append(msg.Images, providers.InputImage{
				MediaType: image.MediaType,
				Data:      image.Data,
				Width:     image.Width,
				Height:    image.Height,
			})
		}
		for _, file := range rec.Files {
			if strings.TrimSpace(file.Data) == "" {
				continue
			}
			msg.Files = append(msg.Files, providers.InputFile{
				MediaType: file.MediaType,
				Data:      file.Data,
				Filename:  file.Filename,
			})
		}
		for _, tc := range rec.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
				ID:                   tc.ID,
				ProviderItemID:       tc.ProviderItemID,
				ProviderItemProvider: tc.ProviderItemProvider,
				ProviderItemModel:    tc.ProviderItemModel,
				Name:                 tc.Name,
				Arguments:            tc.Arguments,
				Kind:                 providers.NormalizeToolCallKind(tc.Kind),
				Display:              cloneToolCallDisplay(tc.Display),
			})
		}
		messages = append(messages, msg)
	}
	return messages
}

func loadAgentHistory(path string) (persistedAgentHistory, error) {
	if strings.TrimSpace(path) == "" {
		return persistedAgentHistory{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return persistedAgentHistory{}, fmt.Errorf("read worker history: %w", err)
	}
	var rec persistedAgentHistory
	if err := json.Unmarshal(data, &rec); err != nil {
		return persistedAgentHistory{}, fmt.Errorf("decode worker history: %w", err)
	}
	return rec, nil
}

// appendChatMessage persists a chat message and returns the seq assigned to
// it. Returns seq 0 when the message is not persisted.
func appendChatMessage(sessDir, id string, msg providers.ChatMessage) (int, error) {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" || !shouldPersistMessage(msg) {
		return 0, nil
	}
	rec := persistedMessageFromChatMessage(msg)
	return sessionstore.AppendHistoryRecordReturningSeq(sessDir, id, historyRecordFromPersistedMessage(rec))
}

func appendChatMessages(sessDir, id string, msgs []providers.ChatMessage) error {
	_, _, err := appendChatMessagesReturningRange(sessDir, id, msgs)
	return err
}

func appendChatMessagesReturningRange(sessDir, id string, msgs []providers.ChatMessage) (int, int, error) {
	seqs, endSeq, err := appendChatMessagesReturningSeqs(sessDir, id, msgs)
	if err != nil {
		return 0, 0, err
	}
	for _, seq := range seqs {
		if seq > 0 {
			return seq, endSeq, nil
		}
	}
	return 0, endSeq, nil
}

func appendChatMessagesReturningSeqs(sessDir, id string, msgs []providers.ChatMessage) ([]int, int, error) {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" || len(msgs) == 0 {
		return make([]int, len(msgs)), 0, nil
	}
	records := make([]sessionstore.HistoryRecord, 0, len(msgs))
	indexes := make([]int, 0, len(msgs))
	for index, msg := range msgs {
		if !shouldPersistMessage(msg) {
			continue
		}
		records = append(records, historyRecordFromPersistedMessage(persistedMessageFromChatMessage(msg)))
		indexes = append(indexes, index)
	}
	startSeq, endSeq, err := sessionstore.AppendHistoryRecordsReturningRange(sessDir, id, records)
	if err != nil {
		return nil, 0, err
	}
	seqs := make([]int, len(msgs))
	for offset, index := range indexes {
		seqs[index] = startSeq + offset
	}
	return seqs, endSeq, nil
}

func rewriteChatHistory(sessDir, id string, msgs []providers.ChatMessage) error {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" {
		return nil
	}
	preserved, err := loadRewritePreservedMessages(sessDir, id)
	if err != nil {
		return fmt.Errorf("load preserved session history: %w", err)
	}
	records := historyRecordsFromChatMessages(msgs)
	for _, rec := range preserved {
		records = append(records, historyRecordFromPersistedMessage(rec))
	}
	return sessionstore.RewriteHistoryRecords(sessDir, id, records)
}

// rewriteChatHistoryAtBaseline replaces the model-visible history while
// preserving records appended after baselineSeq in one store transaction.
func rewriteChatHistoryAtBaseline(sessDir, id string, msgs []providers.ChatMessage, baselineSeq int) error {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" {
		return nil
	}
	records := historyRecordsFromChatMessages(msgs)
	return sessionstore.RewriteHistoryRecordsAtBaseline(sessDir, id, records, baselineSeq)
}

func historyRecordsFromChatMessages(msgs []providers.ChatMessage) []sessionstore.HistoryRecord {
	records := make([]sessionstore.HistoryRecord, 0, len(msgs))
	for _, msg := range msgs {
		if !shouldPersistMessage(msg) {
			continue
		}
		records = append(records, historyRecordFromPersistedMessage(persistedMessageFromChatMessage(msg)))
	}
	return records
}

func maxHistorySeq(msgs []providers.ChatMessage) int {
	maxSeq := 0
	for _, msg := range msgs {
		if msg.Seq > maxSeq {
			maxSeq = msg.Seq
		}
	}
	return maxSeq
}

// appendTokenUsage persists one cumulative token usage snapshot to the session
// history. provider and model tag the row so the insight scanner can aggregate
// usage per provider/model across sessions. Empty values are preserved as empty
// strings, which the scanner interprets as "unknown provider/model".
func appendTokenUsage(sessDir, id, provider, model string, usage providers.TokenUsage, contextTokens int) error {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" || (usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheCreationTokens == 0 && usage.CacheReadTokens == 0 && contextTokens == 0) {
		return nil
	}
	rec := persistedMessage{
		Role:                "meta",
		Content:             "token_usage",
		Provider:            strings.TrimSpace(provider),
		Model:               strings.TrimSpace(model),
		At:                  time.Now().UTC(),
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		ContextTokens:       contextTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		CacheReadTokens:     usage.CacheReadTokens,
	}
	return sessionstore.AppendHistoryRecord(sessDir, id, historyRecordFromPersistedMessage(rec))
}

func persistedMessageFromChatMessage(msg providers.ChatMessage) persistedMessage {
	out := persistedMessage{
		Seq:               msg.Seq,
		Role:              strings.ToLower(msg.Role),
		Content:           msg.Content,
		DisplayContent:    msg.DisplayContent,
		Origin:            msg.Origin,
		OriginID:          msg.OriginID,
		Cause:             msg.Cause,
		PresentationKind:  msg.PresentationKind,
		RelatedSessionID:  msg.RelatedSessionID,
		ReadOnly:          msg.ReadOnly,
		Phase:             string(msg.Phase),
		Hidden:            msg.Hidden,
		ProviderItemID:    msg.ProviderItemID,
		ProviderItemModel: msg.ProviderItemModel,
		Provider:          msg.ProviderItemProvider,
		ClientID:          msg.ClientID,
		Steered:           msg.Steered,
		ReasoningContent:  msg.ReasoningContent,
		ReasoningBlocks:   append([]providers.ReasoningBlock(nil), msg.ReasoningBlocks...),
		ProviderItems:     append([]providers.ProviderItem(nil), msg.ProviderItems...),
		DiscoveredTools:   providers.CloneLoadableToolDefinitions(msg.DiscoveredTools),
		ToolCallID:        msg.ToolCallID,
		ToolInvocationID:  msg.ToolInvocationID,
		ToolResultKind:    string(msg.ToolResultKind),
		ToolResult:        cloneToolResult(msg.ToolResult),
		FinishReason:      string(msg.FinishReason),
		StopReason:        strings.ToLower(strings.TrimSpace(msg.StopReason)),
		Truncated:         msg.Truncated,
		Name:              msg.Name,
		At:                time.Now().UTC(),
	}
	for _, image := range msg.Images {
		data := strings.TrimSpace(image.Data)
		if data == "" {
			continue
		}
		out.Images = append(out.Images, persistedImage{
			MediaType: image.MediaType,
			Data:      data,
			Width:     image.Width,
			Height:    image.Height,
		})
	}
	for _, file := range msg.Files {
		data := strings.TrimSpace(file.Data)
		if data == "" {
			continue
		}
		out.Files = append(out.Files, persistedFile{
			MediaType: file.MediaType,
			Data:      data,
			Filename:  file.Filename,
		})
	}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, persistedToolCall{
			ID:                   tc.ID,
			ProviderItemID:       tc.ProviderItemID,
			ProviderItemProvider: tc.ProviderItemProvider,
			ProviderItemModel:    tc.ProviderItemModel,
			Name:                 tc.Name,
			Arguments:            tc.Arguments,
			Kind:                 string(tc.Kind),
			Display:              cloneToolCallDisplay(tc.Display),
		})
	}
	return out
}

func loadMetaMessages(sessDir, id string) ([]persistedMessage, error) {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	if records, err := loadPersistedMessages(sessDir, id, true); err != nil {
		return nil, err
	} else if records != nil {
		metas := make([]persistedMessage, 0)
		for _, rec := range records {
			if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
				metas = append(metas, rec)
			}
		}
		return metas, nil
	}
	return nil, nil
}

func loadPersistedMessages(sessDir, id string, includeMeta bool) ([]persistedMessage, error) {
	records, err := sessionstore.LoadHistoryRecords(sessDir, id, includeMeta)
	if err != nil {
		return nil, fmt.Errorf("load session history: %w", err)
	}
	out := make([]persistedMessage, 0, len(records))
	for _, rec := range records {
		msg, err := persistedMessageFromHistoryRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// loadProviderPersistedMessages returns the current logical provider history
// together with the physical message head it was reconstructed from. Raw
// session_messages remain append-only; checkpoints replace the provider-visible
// prefix without deleting or renumbering those physical rows.
func loadProviderPersistedMessages(sessDir, id string, includeMeta bool) ([]persistedMessage, int, error) {
	snapshot, err := sessionstore.LoadProviderHistorySnapshot(sessDir, id)
	if err != nil {
		return nil, 0, fmt.Errorf("load provider history: %w", err)
	}
	out, err := persistedMessagesFromProviderSnapshot(snapshot, includeMeta)
	return out, snapshot.HeadSeq, err
}

func persistedMessagesFromProviderSnapshot(snapshot sessionstore.ProviderHistorySnapshot, includeMeta bool) ([]persistedMessage, error) {
	out := make([]persistedMessage, 0, len(snapshot.Records))
	for _, rec := range snapshot.Records {
		if !includeMeta && strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
			continue
		}
		msg, err := persistedMessageFromHistoryRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// displayHistoryAcrossProviderCheckpoint restores the user-visible transcript
// that predates the current provider checkpoint without putting compacted tool
// payloads and reasoning back on the wire. The provider snapshot remains the
// source of truth from its earliest retained record onward.
func displayHistoryAcrossProviderCheckpoint(raw, provider []persistedMessage) []persistedMessage {
	firstRetainedSeq := 0
	for _, rec := range provider {
		if rec.Seq > 0 && (firstRetainedSeq == 0 || rec.Seq < firstRetainedSeq) {
			firstRetainedSeq = rec.Seq
		}
	}
	if firstRetainedSeq <= 1 {
		return provider
	}
	display := make([]persistedMessage, 0, len(raw)+len(provider))
	for _, rec := range raw {
		if rec.Seq <= 0 || rec.Seq >= firstRetainedSeq {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(rec.Role)) {
		case "tool":
			continue
		case "assistant":
			rec.ToolCalls = nil
			rec.ReasoningContent = ""
			rec.ReasoningBlocks = nil
			if strings.TrimSpace(rec.Content) == "" && strings.TrimSpace(rec.DisplayContent) == "" {
				continue
			}
		}
		display = append(display, rec)
	}
	return append(display, provider...)
}

func historyRecordFromPersistedMessage(rec persistedMessage) sessionstore.HistoryRecord {
	return sessionstore.HistoryRecord{
		Seq:                 rec.Seq,
		Role:                rec.Role,
		Content:             rec.Content,
		DisplayContent:      rec.DisplayContent,
		Origin:              rec.Origin,
		OriginID:            rec.OriginID,
		Cause:               rec.Cause,
		PresentationKind:    rec.PresentationKind,
		RelatedSessionID:    rec.RelatedSessionID,
		ReadOnly:            rec.ReadOnly,
		Phase:               rec.Phase,
		ProviderItemID:      rec.ProviderItemID,
		ProviderItemModel:   rec.ProviderItemModel,
		ClientID:            rec.ClientID,
		Hidden:              rec.Hidden,
		Steered:             rec.Steered,
		ReasoningContent:    rec.ReasoningContent,
		ReasoningBlocks:     mustJSON(rec.ReasoningBlocks),
		ProviderItems:       mustJSON(rec.ProviderItems),
		ContentParts:        mustJSON(rec.ContentParts),
		Images:              mustJSON(rec.Images),
		Files:               mustJSON(rec.Files),
		ToolCalls:           mustJSON(rec.ToolCalls),
		DiscoveredTools:     mustJSON(rec.DiscoveredTools),
		ToolCallID:          rec.ToolCallID,
		ToolInvocationID:    rec.ToolInvocationID,
		ToolResultKind:      rec.ToolResultKind,
		ToolResult:          mustJSON(rec.ToolResult),
		FinishReason:        rec.FinishReason,
		StopReason:          rec.StopReason,
		Truncated:           rec.Truncated,
		Name:                rec.Name,
		At:                  rec.At,
		InputTokens:         rec.InputTokens,
		OutputTokens:        rec.OutputTokens,
		ContextTokens:       rec.ContextTokens,
		CacheCreationTokens: rec.CacheCreationTokens,
		CacheReadTokens:     rec.CacheReadTokens,
		RetryCount:          rec.RetryCount,
		MaxRetries:          rec.MaxRetries,
		Provider:            rec.Provider,
		Model:               rec.Model,
	}
}

func persistedMessageFromHistoryRecord(rec sessionstore.HistoryRecord) (persistedMessage, error) {
	out := persistedMessage{
		// Seq is the message's stable per-thread address; carrying it through the
		// reconstruction is what lets a rendered item be resolved back to its seq.
		Seq:                 rec.Seq,
		Role:                rec.Role,
		Content:             rec.Content,
		DisplayContent:      rec.DisplayContent,
		Origin:              rec.Origin,
		OriginID:            rec.OriginID,
		Cause:               rec.Cause,
		PresentationKind:    rec.PresentationKind,
		RelatedSessionID:    rec.RelatedSessionID,
		ReadOnly:            rec.ReadOnly,
		Phase:               rec.Phase,
		ProviderItemID:      rec.ProviderItemID,
		ProviderItemModel:   rec.ProviderItemModel,
		ClientID:            rec.ClientID,
		Hidden:              rec.Hidden,
		Steered:             rec.Steered,
		ReasoningContent:    rec.ReasoningContent,
		ToolCallID:          rec.ToolCallID,
		ToolInvocationID:    rec.ToolInvocationID,
		ToolResultKind:      rec.ToolResultKind,
		FinishReason:        rec.FinishReason,
		StopReason:          rec.StopReason,
		Truncated:           rec.Truncated,
		Name:                rec.Name,
		At:                  rec.At,
		InputTokens:         rec.InputTokens,
		OutputTokens:        rec.OutputTokens,
		ContextTokens:       rec.ContextTokens,
		CacheCreationTokens: rec.CacheCreationTokens,
		CacheReadTokens:     rec.CacheReadTokens,
		RetryCount:          rec.RetryCount,
		MaxRetries:          rec.MaxRetries,
		Provider:            rec.Provider,
		Model:               rec.Model,
	}
	if err := unmarshalRaw(rec.ReasoningBlocks, &out.ReasoningBlocks); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.ProviderItems, &out.ProviderItems); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.ContentParts, &out.ContentParts); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.Images, &out.Images); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.Files, &out.Files); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.ToolCalls, &out.ToolCalls); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.DiscoveredTools, &out.DiscoveredTools); err != nil {
		return persistedMessage{}, err
	}
	if err := unmarshalRaw(rec.ToolResult, &out.ToolResult); err != nil {
		return persistedMessage{}, err
	}
	out.DiscoveredTools = providers.CloneLoadableToolDefinitions(out.DiscoveredTools)
	return out, nil
}

func loadRewritePreservedMessages(sessDir, id string) ([]persistedMessage, error) {
	if strings.TrimSpace(sessDir) == "" || strings.TrimSpace(id) == "" {
		return nil, nil
	}
	records, err := loadPersistedMessages(sessDir, id, true)
	if err != nil {
		return nil, err
	}
	preserved := make([]persistedMessage, 0)
	for _, rec := range records {
		if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
			preserved = append(preserved, rec)
		}
	}
	return preserved, nil
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" || string(data) == "[]" {
		return nil
	}
	return data
}

func unmarshalRaw(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode session history payload: %w", err)
	}
	return nil
}

func shouldPersistMessage(msg providers.ChatMessage) bool {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	switch role {
	case "user", "assistant", "tool":
		return true
	case "system":
		content := strings.TrimSpace(msg.Content)
		return strings.HasPrefix(content, compact.ConversationSummaryPrefix)
	default:
		return false
	}
}

func persistableMessageCount(msgs []providers.ChatMessage) int {
	var count int
	for _, msg := range msgs {
		if shouldPersistMessage(msg) && !msg.Hidden && !compact.IsInternalContextMessage(msg) {
			count++
		}
	}
	return count
}

func ensureBaseSystemPrompt(history []providers.ChatMessage, prompt string) []providers.ChatMessage {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return history
	}
	if len(history) > 0 && strings.EqualFold(history[0].Role, "system") && history[0].Content == prompt {
		return history
	}
	out := make([]providers.ChatMessage, 0, len(history)+1)
	out = append(out, providers.ChatMessage{Role: "system", Content: prompt})
	out = append(out, history...)
	return out
}

func replaceBaseSystemPrompt(history []providers.ChatMessage, prompt string) []providers.ChatMessage {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return history
	}
	out := cloneHistory(history)
	if len(out) == 0 {
		return []providers.ChatMessage{{Role: "system", Content: prompt}}
	}
	if strings.EqualFold(out[0].Role, "system") {
		// A compact summary is durable conversation state, not the ephemeral
		// runtime prompt. Sessions whose persisted history starts at a compact
		// boundary need the current base prompt inserted before that summary.
		if compact.IsConversationSummaryContent(out[0].Content) {
			return append([]providers.ChatMessage{{Role: "system", Content: prompt}}, out...)
		}
		if out[0].Content == prompt {
			return history
		}
		out[0].Content = prompt
		return out
	}
	return append([]providers.ChatMessage{{Role: "system", Content: prompt}}, out...)
}

func appendControlledChatMessage(dir, id string, msg providers.ChatMessage, control *sessionstore.Control) (int, error) {
	rec := historyRecordFromPersistedMessage(persistedMessageFromChatMessage(msg))
	return sessionstore.AppendControlledHistoryRecord(dir, id, rec, control)
}
