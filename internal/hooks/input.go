package hooks

import "encoding/json"

// Input is the JSON payload sent to a hook process via stdin.
// Only fields relevant to the triggering event are populated; omitempty
// keeps the wire format clean for hook authors.
type Input struct {
	Event        Event           `json:"hook_event_name"`
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse string          `json:"tool_response,omitempty"`
	Error        string          `json:"error,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	FilePath     string          `json:"file_path,omitempty"` // for FileChanged events
}
