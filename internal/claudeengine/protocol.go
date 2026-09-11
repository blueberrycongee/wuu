package claudeengine

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Claude CLI stream-json protocol types. Wire fields are snake_case; the
// top-level "type" field dispatches (system / assistant / user / result /
// stream_event).

// userEnvelope is the stdin input message shape for both user prompts and
// tool results.
type userEnvelope struct {
	Type            string      `json:"type"`
	Message         userMessage `json:"message"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
}

type userMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

// contentBlock is one Anthropic-style content block (text / tool_result /
// image etc.).
type contentBlock struct {
	Source    *imageSource `json:"source,omitempty"`
	Type      string       `json:"type"`
	Text      string       `json:"text,omitempty"`
	ToolUseID string       `json:"tool_use_id,omitempty"`
	Content   any          `json:"content,omitempty"`
	IsError   bool         `json:"is_error,omitempty"`
}

// outgoing envelope helpers -------------------------------------------------

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func userPromptEnvelope(message providers.ChatMessage) userEnvelope {
	var content any = message.Content
	if len(message.Images) > 0 {
		blocks := make([]contentBlock, 0, 1+len(message.Images))
		if strings.TrimSpace(message.Content) != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: message.Content})
		}
		for _, img := range message.Images {
			blocks = append(blocks, contentBlock{Type: "image", Source: &imageSource{
				Type: "base64", MediaType: img.MediaType, Data: img.Data,
			}})
		}
		content = blocks
	}
	return userEnvelope{
		Type:    "user",
		Message: userMessage{Role: "user", Content: content},
	}
}

func toolResultEnvelope(toolUseID, content string, isError bool) userEnvelope {
	return userEnvelope{
		Type: "user",
		Message: userMessage{
			Role: "user",
			Content: []contentBlock{{
				Type:      "tool_result",
				ToolUseID: toolUseID,
				Content:   content,
				IsError:   isError,
			}},
		},
	}
}

// incoming messages ---------------------------------------------------------

// initMessage is system/subtype=init: carries the session id and version.
type initMessage struct {
	SessionID         string `json:"session_id"`
	ClaudeCodeVersion string `json:"claude_code_version,omitempty"`
	Model             string `json:"model,omitempty"`
}

// assistantContentBlock mirrors content blocks in assistant messages.
type assistantContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Input    any    `json:"input,omitempty"`
}

// assistantMessage is a full assistant message (also the basis for partial
// content via stream events).
type assistantMessage struct {
	ID      string                  `json:"id"`
	Content []assistantContentBlock `json:"content,omitempty"`
	Model   string                  `json:"model,omitempty"`
}

// streamEvent is the Anthropic event nested inside a stream_event line.
type streamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	Delta        *streamEventDelta      `json:"delta,omitempty"`
	ContentBlock *assistantContentBlock `json:"content_block,omitempty"`
	Message      *assistantMessage      `json:"message,omitempty"`
	Usage        *tokenUsage            `json:"usage,omitempty"`
	StopReason   string                 `json:"stop_reason,omitempty"`
}

type streamEventDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// tokenUsage mirrors Anthropic usage fields.
type tokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// resultMessage is the per-turn terminal result line.
type resultMessage struct {
	Type       string      `json:"type"`
	Subtype    string      `json:"subtype,omitempty"`
	IsError    bool        `json:"is_error"`
	StopReason string      `json:"stop_reason,omitempty"`
	Result     string      `json:"result,omitempty"`
	Usage      *tokenUsage `json:"usage,omitempty"`
	Error      *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}
