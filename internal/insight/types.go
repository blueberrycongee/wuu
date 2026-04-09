package insight

import (
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// SessionMeta is extracted from a session JSONL file without LLM calls.
type SessionMeta struct {
	ID            string            `json:"id"`
	CreatedAt     time.Time         `json:"created_at"`
	Duration      time.Duration     `json:"duration"`
	UserMessages  int               `json:"user_messages"`
	AssistantMsgs int               `json:"assistant_messages"`
	ToolCounts    map[string]int    `json:"tool_counts"`
	Languages     map[string]int    `json:"languages"`
	EstTokens     int               `json:"est_tokens"`
	InputTokens   int               `json:"input_tokens"`
	OutputTokens  int               `json:"output_tokens"`
	FirstUserMsg  string            `json:"first_user_msg"`
	MessageHours  []int             `json:"message_hours"`
	LinesAdded    int               `json:"lines_added"`
	LinesRemoved  int               `json:"lines_removed"`
	FilesModified int               `json:"files_modified"`
	UserTimestamps []time.Time      `json:"user_timestamps"`
}

// Facet is the LLM-extracted structured analysis of a single session.
type Facet struct {
	SessionID       string            `json:"session_id"`
	Goal            string            `json:"goal"`
	GoalCategories  map[string]int    `json:"goal_categories"`
	Outcome         string            `json:"outcome"`
	Satisfaction    string            `json:"satisfaction"`
	Helpfulness     string            `json:"helpfulness"`
	SessionType     string            `json:"session_type"`
	Friction        map[string]int    `json:"friction"`
	FrictionDetail  string            `json:"friction_detail"`
	PrimarySuccess  string            `json:"primary_success"`
	Summary         string            `json:"summary"`
	ExtractedAt     int64             `json:"extracted_at"`
}

// AggregatedData combines statistics from all sessions and facets.
type AggregatedData struct {
	TotalSessions   int               `json:"total_sessions"`
	SessionsWithFacets int            `json:"sessions_with_facets"`
	DateRange       [2]string         `json:"date_range"`
	TotalMessages   int               `json:"total_messages"`
	TotalDurationH  float64           `json:"total_duration_hours"`
	TotalEstTokens  int               `json:"total_est_tokens"`
	TotalInputTokens  int             `json:"total_input_tokens"`
	TotalOutputTokens int             `json:"total_output_tokens"`
	ToolCounts      map[string]int    `json:"tool_counts"`
	Languages       map[string]int    `json:"languages"`
	GoalCategories  map[string]int    `json:"goal_categories"`
	Outcomes        map[string]int    `json:"outcomes"`
	Satisfaction    map[string]int    `json:"satisfaction"`
	SessionTypes    map[string]int    `json:"session_types"`
	Friction        map[string]int    `json:"friction"`
	Success         map[string]int    `json:"success"`
	Summaries       []SessionSummary  `json:"summaries"`
	TotalLinesAdded   int             `json:"total_lines_added"`
	TotalLinesRemoved int             `json:"total_lines_removed"`
	TotalFilesModified int            `json:"total_files_modified"`
	DaysActive      int               `json:"days_active"`
	MessagesPerDay  float64           `json:"messages_per_day"`
	MessageHours    []int             `json:"message_hours"`
}

// SessionSummary is a short summary entry used in aggregated data.
type SessionSummary struct {
	ID      string `json:"id"`
	Date    string `json:"date"`
	Summary string `json:"summary"`
	Goal    string `json:"goal,omitempty"`
}

// InsightSection is one section of the generated report.
type InsightSection struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// AtAGlance is the four-part summary synthesized from all sections.
type AtAGlance struct {
	WhatsWorking    string `json:"whats_working"`
	WhatsHindering  string `json:"whats_hindering"`
	QuickWins       string `json:"quick_wins"`
	AmbitiousWorkflows string `json:"ambitious_workflows"`
}

// Report is the final assembled insight output.
type Report struct {
	AtAGlance    AtAGlance        `json:"at_a_glance"`
	Sections     []InsightSection `json:"sections"`
	Stats        AggregatedData   `json:"stats"`
	GeneratedAt  time.Time        `json:"generated_at"`
	HTMLPath     string           `json:"-"` // path to generated HTML report file
}

// ProgressEvent is sent from the insight goroutine to the TUI.
type ProgressEvent struct {
	Phase  string  // "scanning", "extracting", "generating", "synthesizing", "done", "error"
	Detail string  // human-readable progress message
	Pct    float64 // 0.0–1.0
	Report *Report // non-nil only when Phase == "done"
	Err    error   // non-nil only when Phase == "error"
}

// RunConfig holds all dependencies for an insight run.
type RunConfig struct {
	SessionDir    string
	WorkspaceRoot string
	Client        providers.Client
	Model         string
	MaxSessions   int
}
