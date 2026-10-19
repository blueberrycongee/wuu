package pluginhost

import (
	"fmt"
	"strings"
	"time"
)

const (
	SessionHistoryReadLimit     = 50
	SessionHistorySearchLimit   = 25
	SessionHistoryDefaultLimit  = 20
	SessionHistorySearchDefault = 10
)

type SessionHistoryReadParams struct {
	SessionID   string `json:"session_id"`
	StartSeq    int    `json:"start_seq,omitempty"`
	EndSeq      int    `json:"end_seq,omitempty"`
	SnapshotSeq int    `json:"snapshot_seq,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}

type SessionHistorySearchParams struct {
	View        string `json:"view,omitempty"`
	TimeMin     string `json:"time_min,omitempty"`
	TimeMax     string `json:"time_max,omitempty"`
	SessionID   string `json:"session_id"`
	Query       string `json:"query"`
	StartSeq    int    `json:"start_seq,omitempty"`
	EndSeq      int    `json:"end_seq,omitempty"`
	SnapshotSeq int    `json:"snapshot_seq,omitempty"`
	BeforeSeq   int    `json:"before_seq,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}

type SessionHistoryAttachment struct {
	Kind     string `json:"kind"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type SessionAttachmentReadParams struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	Index     int    `json:"index"`
	SHA256    string `json:"sha256"`
	Offset    int    `json:"offset,omitempty"`
}

type SessionAttachmentReadResult struct {
	Data        string `json:"data"`
	Offset      int    `json:"offset"`
	Total       int    `json:"total"`
	ContentType string `json:"content_type"`
	Name        string `json:"name"`
	SHA256      string `json:"sha256"`
}

type SessionHistoryRecord struct {
	// ProviderItemID matches a projected thread item's source_id when available;
	// EngineTurnID identifies a native engine turn. Neither is a host item/turn ID.
	ProviderItemID   string                     `json:"provider_item_id,omitempty"`
	EngineTurnID     string                     `json:"engine_turn_id,omitempty"`
	At               string                     `json:"at,omitempty"`
	Attachments      []SessionHistoryAttachment `json:"attachments,omitempty"`
	Seq              int                        `json:"seq"`
	Role             string                     `json:"role"`
	Hidden           bool                       `json:"hidden,omitempty"`
	ReadOnly         bool                       `json:"read_only,omitempty"`
	Origin           string                     `json:"origin,omitempty"`
	Name             string                     `json:"name,omitempty"`
	Content          string                     `json:"content,omitempty"`
	DisplayContent   string                     `json:"display_content,omitempty"`
	ReasoningContent string                     `json:"reasoning_content,omitempty"`
	ToolCalls        string                     `json:"tool_calls,omitempty"`
	ToolCallID       string                     `json:"tool_call_id,omitempty"`
	ToolResult       string                     `json:"tool_result,omitempty"`
	FinishReason     string                     `json:"finish_reason,omitempty"`
	Truncated        bool                       `json:"truncated,omitempty"`
}

type SessionHistoryMatch struct {
	At      string `json:"at,omitempty"`
	Seq     int    `json:"seq"`
	Role    string `json:"role"`
	Name    string `json:"name,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

type SessionHistoryPage struct {
	SessionID       string                 `json:"session_id"`
	HeadSeq         int                    `json:"head_seq"`
	SnapshotSeq     int                    `json:"snapshot_seq"`
	Records         []SessionHistoryRecord `json:"records,omitempty"`
	Matches         []SessionHistoryMatch  `json:"matches,omitempty"`
	HasMore         bool                   `json:"has_more,omitempty"`
	BudgetExhausted bool                   `json:"budget_exhausted,omitempty"`
	NextCursor      string                 `json:"next_cursor,omitempty"`
}

func ValidateSessionHistoryReadParams(params SessionHistoryReadParams) error {
	if err := validateDataScopeID("session_id", params.SessionID); err != nil {
		return err
	}
	if params.StartSeq < 0 || params.EndSeq < 0 || params.SnapshotSeq < 0 {
		return &HostServiceError{Code: "invalid_params", Message: "history snapshot bounds must be non-negative"}
	}
	if params.StartSeq < 1 && strings.TrimSpace(params.Cursor) == "" {
		return &HostServiceError{Code: "invalid_params", Message: "history read requires start_seq or cursor"}
	}
	if params.Limit < 0 || params.Limit > SessionHistoryReadLimit {
		return &HostServiceError{Code: "invalid_params", Message: fmt.Sprintf("history read limit must be between 0 and %d", SessionHistoryReadLimit)}
	}
	return nil
}

func ValidateSessionHistorySearchParams(params SessionHistorySearchParams) error {
	if params.View != "" && params.View != "conversation" {
		return &HostServiceError{Code: "invalid_params", Message: "unsupported history search view"}
	}
	var minimum, maximum time.Time
	var err error
	if params.TimeMin != "" {
		minimum, err = time.Parse(time.RFC3339Nano, params.TimeMin)
		if err != nil {
			return &HostServiceError{Code: "invalid_params", Message: "time_min must be an RFC3339 timestamp"}
		}
	}
	if params.TimeMax != "" {
		maximum, err = time.Parse(time.RFC3339Nano, params.TimeMax)
		if err != nil {
			return &HostServiceError{Code: "invalid_params", Message: "time_max must be an RFC3339 timestamp"}
		}
	}
	if !minimum.IsZero() && !maximum.IsZero() && !maximum.After(minimum) {
		return &HostServiceError{Code: "invalid_params", Message: "time_max must follow time_min"}
	}

	if err := validateDataScopeID("session_id", params.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(params.Query) == "" {
		return &HostServiceError{Code: "invalid_params", Message: "history search query is required"}
	}
	if params.StartSeq < 0 || params.EndSeq < 0 || params.SnapshotSeq < 0 || params.BeforeSeq < 0 {
		return &HostServiceError{Code: "invalid_params", Message: "history snapshot bounds must be non-negative"}
	}
	if params.Limit < 0 || params.Limit > SessionHistorySearchLimit {
		return &HostServiceError{Code: "invalid_params", Message: fmt.Sprintf("history search limit must be between 0 and %d", SessionHistorySearchLimit)}
	}
	return nil
}
