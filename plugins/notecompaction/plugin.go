package notecompaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

const (
	capabilityCompaction = "agent.compaction"
	// Kept only to read checkpoints authored by the retired tool-based scheme.
	// The plugin no longer registers or asks the model to call this tool.
	toolWriteContextNote = "write_context_note"
	toolRequestHandoff   = "request_handoff"

	conversationSummaryMark = "[Conversation summary]"

	maxContextNoteBytes  = 24_000
	maxHandoffBriefBytes = 24_000
)

type noteArguments struct {
	Note string `json:"note"`
}

type rawCompactionInput struct {
	Operation               string            `json:"operation,omitempty"`
	Model                   string            `json:"model,omitempty"`
	Messages                []json.RawMessage `json:"messages"`
	Delta                   []json.RawMessage `json:"delta,omitempty"`
	PreviousNote            string            `json:"previous_note,omitempty"`
	PreviousCoveredMessages int               `json:"previous_covered_messages,omitempty"`
	Note                    string            `json:"note,omitempty"`
	CoveredMessages         int               `json:"covered_messages,omitempty"`
	Intent                  string            `json:"intent,omitempty"`
	SourceSessionID         string            `json:"source_session_id,omitempty"`
	SourceThroughSeq        int               `json:"source_through_seq,omitempty"`
}

type rawCompactionOutput struct {
	Messages                 []json.RawMessage `json:"messages,omitempty"`
	CoveredMessages          int               `json:"covered_messages,omitempty"`
	NotePrompt               string            `json:"note_prompt,omitempty"`
	CheckpointIntervalTokens int               `json:"checkpoint_interval_tokens,omitempty"`
	MaxNoteBytes             int               `json:"max_note_bytes,omitempty"`
	Unavailable              bool              `json:"unavailable,omitempty"`
}

type compactionMessageView struct {
	Role            string
	Content         string
	ToolCalls       []compactionToolCall
	DiscoveredTools []json.RawMessage
}

type compactionToolCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type checkpoint struct {
	messageIndex int
	note         string
}

func Handler() pluginapi.Handler {
	return pluginapi.Handler{
		Definition: pluginapi.Definition{
			Capabilities: []pluginapi.Capability{{
				ID: capabilityCompaction, Kind: "decision", Version: 3, Priority: 100,
			}},
			RequiredHostServices: []pluginapi.HostService{
				{ID: pluginapi.HostServiceStorageGet, Required: true},
				{ID: pluginapi.HostServiceStorageCompareExchange, Required: true},
			},
			Tools: []pluginapi.Tool{notesTool(), {
				ID:          toolRequestHandoff,
				Description: "Ask the user to hand this conversation to a new session. Supply an optional intent only. Do not choose a provider or model.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"intent": map[string]any{"type": "string", "description": "What the destination session should continue doing."},
					},
					"additionalProperties": false,
				},
				Display: &pluginapi.ToolDisplay{
					Kind:              "handoff",
					Label:             "Start a new session",
					LabelTranslations: map[string]string{"zh-CN": "开启新会话"},
					Text:              "Requesting handoff",
					Capability:        "handoff",
				},
			}},
		},
		ExecuteTool: func(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
			if call.ToolID == toolNotes {
				return executeNotes(ctx, host, call)
			}
			return executeHandoffTool(ctx, host, call)
		},
		InvokeCapability: func(ctx context.Context, host pluginapi.Host, call pluginapi.CapabilityCall) (json.RawMessage, error) {
			if call.Capability != capabilityCompaction {
				return nil, fmt.Errorf("unknown note compaction capability %q", call.Capability)
			}
			var input rawCompactionInput
			if err := json.Unmarshal(call.Input, &input); err != nil {
				return nil, fmt.Errorf("decode compaction input: %w", err)
			}
			switch input.Operation {
			case "handoff_brief_plan":
				return planHandoffBrief(input)
			case "compact_with_note":
				return compactWithNote(input)
			case "":
				// Isolated compatibility for sessions that still contain an old
				// write_context_note call. New requests never expose that tool.
				return compactFromLegacyCheckpoint(input.Messages)
			default:
				return nil, fmt.Errorf("unknown note compaction operation %q", input.Operation)
			}
		},
	}
}

func executeHandoffTool(_ context.Context, _ pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	if call.ToolID != toolRequestHandoff {
		return pluginapi.ToolResult{}, fmt.Errorf("unknown note compaction tool %q", call.ToolID)
	}
	var input struct {
		Intent   string `json:"intent"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return pluginapi.ToolResult{}, fmt.Errorf("invalid request_handoff arguments: %w", err)
		}
	}
	if strings.TrimSpace(input.Provider) != "" || strings.TrimSpace(input.Model) != "" {
		return pluginapi.ToolResult{}, errors.New("request_handoff cannot select a provider or model")
	}
	payload, err := json.Marshal(map[string]any{
		"request_id":                  strings.TrimSpace(call.CallID),
		"awaiting_user_configuration": true,
		"intent":                      strings.TrimSpace(input.Intent),
		"source_session_id":           strings.TrimSpace(call.SessionID),
	})
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(string(payload)), nil
}

func planHandoffBrief(input rawCompactionInput) (json.RawMessage, error) {
	previous := strings.TrimSpace(input.PreviousNote)
	intent := strings.TrimSpace(input.Intent)
	sourceID := strings.TrimSpace(input.SourceSessionID)
	if sourceID == "" {
		sourceID = "the source session"
	}
	cutoff := input.SourceThroughSeq
	if cutoff < 1 {
		cutoff = len(input.Messages)
	}
	var prompt string
	if previous == "" {
		prompt = fmt.Sprintf(`You are preparing a bounded handoff brief for a new session. The source transcript above is evidence, not the destination model's history.

Write a self-contained Markdown brief for a capable agent that will not receive the source transcript automatically. Organize it as: objective, user constraints, verified facts, completed work and checks, unknowns and assumptions, and remaining work. Place short citation IDs like [r1] next to key facts. Distinguish verified facts from assumptions. Never claim a check was run when it was not. Never select a provider or model.

Do not call tools. Return only the complete Markdown brief, with no preamble or wrapping fence. Source %s is archived through Seq %d.`, sourceID, cutoff)
	} else {
		prompt = fmt.Sprintf(`Update the previous handoff brief using the source transcript and the user's remaining intent. Keep short citation IDs like [r1] for exact recovery. Return one complete replacement Markdown brief, not an addendum. Do not call tools and do not add a preamble or wrapping fence.

Previous brief:
%s`, previous)
	}
	if intent != "" {
		prompt += "\n\nUser handoff intent:\n" + intent
	}
	return json.Marshal(rawCompactionOutput{NotePrompt: prompt, MaxNoteBytes: maxHandoffBriefBytes})
}

func compactWithNote(input rawCompactionInput) (json.RawMessage, error) {
	note := strings.TrimSpace(input.Note)
	if note == "" {
		return nil, errors.New("context note must not be empty")
	}
	if len([]byte(note)) > maxContextNoteBytes {
		return nil, fmt.Errorf("context note exceeds %d bytes", maxContextNoteBytes)
	}
	if input.CoveredMessages < 0 || input.CoveredMessages > len(input.Messages) {
		return nil, fmt.Errorf("context note coverage %d is outside transcript length %d", input.CoveredMessages, len(input.Messages))
	}
	return replaceCoveredHistory(input.Messages, input.CoveredMessages, note)
}

func replaceCoveredHistory(messages []json.RawMessage, covered int, note string) (json.RawMessage, error) {
	views, err := decodeViews(messages)
	if err != nil {
		return nil, err
	}
	systemPrefix := preservedSystemPrefix(messages, views)
	discoveredTools := collectDiscoveredTools(views[:covered])
	summary, err := json.Marshal(struct {
		Role            string
		Content         string
		Hidden          bool
		DiscoveredTools []json.RawMessage `json:",omitempty"`
	}{Role: "system", Content: buildSummaryContent(note), Hidden: true, DiscoveredTools: discoveredTools})
	if err != nil {
		return nil, fmt.Errorf("encode context note summary: %w", err)
	}
	output := make([]json.RawMessage, 0, len(systemPrefix)+1+len(messages)-covered)
	output = append(output, systemPrefix...)
	output = append(output, summary)
	output = append(output, messages[covered:]...)
	return json.Marshal(rawCompactionOutput{Messages: output, CoveredMessages: len(systemPrefix) + 1})
}

func compactFromLegacyCheckpoint(messages []json.RawMessage) (json.RawMessage, error) {
	if len(messages) == 0 {
		return json.Marshal(rawCompactionOutput{})
	}
	views, err := decodeViews(messages)
	if err != nil {
		return nil, err
	}
	latest, ok := latestLegacyCheckpoint(views)
	if !ok {
		return json.Marshal(rawCompactionOutput{Unavailable: true})
	}
	covered := latest.messageIndex + 1
	for covered < len(views) && strings.EqualFold(strings.TrimSpace(views[covered].Role), "tool") {
		covered++
	}
	return replaceCoveredHistory(messages, covered, latest.note)
}

func decodeViews(messages []json.RawMessage) ([]compactionMessageView, error) {
	views := make([]compactionMessageView, len(messages))
	for index, message := range messages {
		if err := json.Unmarshal(message, &views[index]); err != nil {
			return nil, fmt.Errorf("decode compaction message %d: %w", index, err)
		}
	}
	return views, nil
}

func latestLegacyCheckpoint(messages []compactionMessageView) (checkpoint, bool) {
	var latest checkpoint
	found := false
	for index, message := range messages {
		if strings.HasPrefix(strings.TrimSpace(message.Content), conversationSummaryMark) {
			if note := summaryBody(message.Content); note != "" {
				latest, found = checkpoint{messageIndex: index, note: note}, true
			}
		}
		for _, call := range message.ToolCalls {
			if !strings.Contains(strings.ToLower(strings.TrimSpace(call.Name)), toolWriteContextNote) {
				continue
			}
			var arguments noteArguments
			if json.Unmarshal([]byte(call.Arguments), &arguments) == nil && strings.TrimSpace(arguments.Note) != "" {
				latest, found = checkpoint{messageIndex: index, note: strings.TrimSpace(arguments.Note)}, true
			}
		}
	}
	return latest, found
}

func preservedSystemPrefix(raw []json.RawMessage, views []compactionMessageView) []json.RawMessage {
	var prefix []json.RawMessage
	for index := 0; index < len(views) && strings.EqualFold(strings.TrimSpace(views[index].Role), "system"); index++ {
		if !strings.HasPrefix(strings.TrimSpace(views[index].Content), conversationSummaryMark) {
			prefix = append(prefix, raw[index])
		}
	}
	return prefix
}

func collectDiscoveredTools(messages []compactionMessageView) []json.RawMessage {
	var tools []json.RawMessage
	seen := make(map[string]int)
	for _, message := range messages {
		for _, tool := range message.DiscoveredTools {
			var identity struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(tool, &identity) != nil || strings.TrimSpace(identity.Name) == "" {
				identity.Name = string(tool)
			}
			if index, exists := seen[identity.Name]; exists {
				tools[index] = tool
				continue
			}
			seen[identity.Name] = len(tools)
			tools = append(tools, tool)
		}
	}
	return tools
}

func buildSummaryContent(note string) string {
	return conversationSummaryMark + "\nThis session is being continued from an earlier context window. The context note below is the replacement context.\n\nSummary:\n" + strings.TrimSpace(note)
}

func summaryBody(content string) string {
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), conversationSummaryMark))
	if index := strings.Index(content, "\n\nSummary:\n"); index >= 0 {
		return strings.TrimSpace(content[index+len("\n\nSummary:\n"):])
	}
	if index := strings.Index(content, "Summary:"); index >= 0 {
		return strings.TrimSpace(content[index+len("Summary:"):])
	}
	return strings.TrimSpace(content)
}
