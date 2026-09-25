package insight

import (
	"time"

	sessionstore "github.com/blueberrycongee/wuu/internal/session"
)

// SessionMeta is extracted from a session JSONL file without LLM calls.
type SessionMeta struct {
	ID                  string         `json:"id"`
	CreatedAt           time.Time      `json:"created_at"`
	Duration            time.Duration  `json:"duration"`
	UserMessages        int            `json:"user_messages"`
	AssistantMsgs       int            `json:"assistant_messages"`
	ToolCounts          map[string]int `json:"tool_counts"`
	Languages           map[string]int `json:"languages"`
	EstTokens           int            `json:"est_tokens"`
	InputTokens         int            `json:"input_tokens"`
	OutputTokens        int            `json:"output_tokens"`
	CacheCreationTokens int            `json:"cache_creation_tokens"`
	CacheReadTokens     int            `json:"cache_read_tokens"`
	FirstUserMsg        string         `json:"first_user_msg"`
	MessageHours        []int          `json:"message_hours"`
	LinesAdded          int            `json:"lines_added"`
	LinesRemoved        int            `json:"lines_removed"`
	FilesModified       int            `json:"files_modified"`
	UserTimestamps      []time.Time    `json:"user_timestamps"`
	// ModelBreakdowns maps "provider|model" keys to per-bucket usage within
	// this session. The key is an internal identifier; UI code renders
	// empty provider+model as "(unknown)". Populated only when the session
	// has at least one token_usage meta row with usable data.
	ModelBreakdowns map[string]*ModelUsage `json:"model_breakdowns"`
}

// ModelUsage aggregates token consumption and session count for one
// provider/model pair. Empty Provider+Model represents legacy token_usage
// rows persisted before provider/model were tracked; UI code renders
// these as "(unknown)".
type ModelUsage struct {
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	Sessions            int    `json:"sessions"`
}

// TotalContextTokens mirrors providers.TokenUsage.TotalContextTokens.
func (m ModelUsage) TotalContextTokens() int {
	return m.InputTokens + m.CacheCreationTokens + m.CacheReadTokens + m.OutputTokens
}

// CacheHitRate returns the prompt-cache hit rate across this bucket.
// Returns nil when there is no input to cache.
func (m ModelUsage) CacheHitRate() *float64 {
	promptTokens := m.InputTokens + m.CacheReadTokens
	if promptTokens <= 0 {
		return nil
	}
	rate := float64(m.CacheReadTokens) / float64(promptTokens)
	return &rate
}

// TokenUsageRow is one token_usage meta row extracted from a session
// history. It carries the per-row timestamp so callers can bucket usage
// by day or pick the most recent N rows for a "最近记录" list, neither
// of which can be done from the pre-aggregated SessionMeta alone.
// At is taken from the persisted record (UTC); rows with a zero At are
// always included for "all" range but excluded from any time-windowed
// query so a malformed legacy record cannot be pinned to "today".
type TokenUsageRow = sessionstore.TokenUsageRow

// SkillUsage is one load_skill invocation aggregated across session history.
type SkillUsage struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type UsageScan struct {
	TokenRows []TokenUsageRow
	Skills    []SkillUsage
}

// The LLM-driven report pipeline types (Facet, AggregatedData,
// SessionSummary, InsightSection, AtAGlance, Report, ProgressEvent,
// RunConfig) were removed together with the Run/GenerateHTML pipeline:
// nothing ever triggered it in production. See the git history of this
// package for the previous implementation.
