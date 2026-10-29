package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	dbFileName = "sessions.sqlite3"
)

var (
	ErrSessionNotFound       = errors.New("session not found")
	ErrSessionNotArchived    = errors.New("session is not archived")
	ErrHistoryUnavailable    = errors.New("session history unavailable")
	ErrHistorySnapshotGone   = errors.New("history snapshot is no longer available")
	ErrModelSelectionChanged = errors.New("session model selection changed")
)

// Session represents one conversation session.
type Session struct {
	UserSpaceID            string            `json:"user_space_id,omitempty"`
	ProductDomain          string            `json:"product_domain,omitempty"`
	AgentID                string            `json:"agent_id,omitempty"`
	ID                     string            `json:"id"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at,omitempty"`
	Title                  string            `json:"title,omitempty"`
	TitleRevision          int64             `json:"title_revision,omitempty"`
	Summary                string            `json:"summary,omitempty"`
	Entries                int               `json:"entries"`
	LatestCompletedTurnID  string            `json:"latest_completed_turn_id,omitempty"`
	CWD                    string            `json:"cwd,omitempty"`
	Source                 string            `json:"source,omitempty"`
	Owner                  string            `json:"owner,omitempty"`
	Visibility             string            `json:"visibility,omitempty"`
	ParentID               string            `json:"parent_id,omitempty"`
	ContextSource          string            `json:"context_source,omitempty"`
	CreationRequestID      string            `json:"creation_request_id,omitempty"`
	SeedID                 string            `json:"seed_id,omitempty"`
	ModelSelectionRevision uint64            `json:"model_selection_revision,omitempty"`
	ModelSelectionApplyAt  string            `json:"model_selection_apply_at,omitempty"`
	Provider               string            `json:"provider,omitempty"`
	Model                  string            `json:"model,omitempty"`
	Variant                string            `json:"variant,omitempty"`
	Effort                 string            `json:"effort,omitempty"`
	Speed                  string            `json:"speed,omitempty"`
	ModelOptions           map[string]string `json:"model_options,omitempty"`
	PermissionMode         string            `json:"permission_mode,omitempty"`
	ApproveForMe           bool              `json:"approve_for_me,omitempty"`
	ProjectRole            string            `json:"project_role,omitempty"`
	Instructions           string            `json:"instructions,omitempty"`
	ToolPolicyJSON         string            `json:"tool_policy_json,omitempty"`
	// EngineID is the agent engine the thread is bound to. Empty reads as
	// the built-in wuu engine, which is the legacy default for sessions
	// persisted before engine binding existed.
	EngineID string `json:"engine_id,omitempty"`
	// EngineRef is the engine's native session reference for this thread
	// (for example a codex thread id). It is written once the engine creates
	// the native session and read back to resume it.
	EngineRef string `json:"engine_ref,omitempty"`
	// WorkspaceID is the stable, location-independent identity of the workspace
	// this session belongs to (the desktop's registered-project id). Sessions
	// of a workspace with an id are listed by that id, so they follow the
	// project across moves/renames even though CWD still records the old path.
	// Empty for location-anchored scratch threads, which are matched by cwd.
	WorkspaceID      string     `json:"workspace_id,omitempty"`
	ForkedFromID     string     `json:"forked_from_id,omitempty"`
	ForkedFromTurnID string     `json:"forked_from_turn_id,omitempty"`
	ForkedFromItemID string     `json:"forked_from_item_id,omitempty"`
	PinnedAt         *time.Time `json:"pinned_at,omitempty"`
	FolderID         string     `json:"folder_id,omitempty"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	ArchiveReason    string     `json:"archive_reason,omitempty"`
	WorktreePath     string     `json:"worktree_path,omitempty"`
	WorktreeBaseHEAD string     `json:"worktree_base_head,omitempty"`
	WorktreeBaseRepo string     `json:"worktree_base_repo,omitempty"`
}

type ForkMetadata struct {
	ForkedFromID     string
	ForkedFromTurnID string
	ForkedFromItemID string
}

type WorktreeInfo struct {
	Path     string
	BaseHEAD string
	BaseRepo string
}

type ManagedMetadata struct {
	Owner             string
	Visibility        string
	ParentID          string
	ContextSource     string
	CreationRequestID string
}

// HistoryRecord is the durable session message shape. App-server remains
// responsible for provider-specific ChatMessage conversion.
type HistoryRecord struct {
	AcceptedClientInput  *AcceptedClientInput              `json:"-"`
	AcceptedClientTurnID string                            `json:"-"`
	Relation             *providers.MessageRelation        `json:"relation,omitempty"`
	ModelSteps           []pluginprotocol.ModelStepUsageV1 `json:"model_steps,omitempty"`
	// ExpectedHeldMessage is an admission-only compare-and-swap fence for an
	// accepted steer. It is never serialized into conversation history.
	ExpectedHeldMessage json.RawMessage `json:"-"`
	// Seq is the record's per-session sequence (session_messages.seq), its
	// stable address within the thread. Ordinary appends ignore it and allocate
	// the next physical sequence. History checkpoints retain positive addresses
	// and allocate a new physical sequence for records whose Seq is zero.
	Seq                 int             `json:"seq,omitempty"`
	Role                string          `json:"role"`
	Content             string          `json:"content"`
	DisplayContent      string          `json:"display_content,omitempty"`
	Origin              string          `json:"origin,omitempty"`
	OriginID            string          `json:"origin_id,omitempty"`
	Cause               string          `json:"cause,omitempty"`
	PresentationKind    string          `json:"presentation_kind,omitempty"`
	RelatedSessionID    string          `json:"related_session_id,omitempty"`
	ReadOnly            bool            `json:"read_only,omitempty"`
	Phase               string          `json:"phase,omitempty"`
	ProviderItemID      string          `json:"provider_item_id,omitempty"`
	EngineTurnID        string          `json:"engine_turn_id,omitempty"`
	EngineTurnPending   bool            `json:"engine_turn_pending,omitempty"`
	ProviderItemModel   string          `json:"provider_item_model,omitempty"`
	ClientID            string          `json:"client_id,omitempty"`
	Hidden              bool            `json:"hidden,omitempty"`
	Steered             bool            `json:"steered,omitempty"`
	ReasoningContent    string          `json:"reasoning_content,omitempty"`
	ReasoningBlocks     json.RawMessage `json:"reasoning_blocks,omitempty"`
	ProviderItems       json.RawMessage `json:"provider_items,omitempty"`
	ContentParts        json.RawMessage `json:"content_parts,omitempty"`
	Images              json.RawMessage `json:"images,omitempty"`
	Files               json.RawMessage `json:"files,omitempty"`
	ToolCalls           json.RawMessage `json:"tool_calls,omitempty"`
	DiscoveredTools     json.RawMessage `json:"discovered_tools,omitempty"`
	ToolCallID          string          `json:"tool_call_id,omitempty"`
	ToolInvocationID    string          `json:"tool_invocation_id,omitempty"`
	ToolResultKind      string          `json:"tool_result_kind,omitempty"`
	ToolResult          json.RawMessage `json:"tool_result,omitempty"`
	FinishReason        string          `json:"finish_reason,omitempty"`
	StopReason          string          `json:"stop_reason,omitempty"`
	Truncated           bool            `json:"truncated,omitempty"`
	Name                string          `json:"name,omitempty"`
	At                  time.Time       `json:"at,omitempty"`
	InputTokens         int             `json:"input_tokens,omitempty"`
	OutputTokens        int             `json:"output_tokens,omitempty"`
	ContextTokens       int             `json:"context_tokens,omitempty"`
	CacheCreationTokens int             `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int             `json:"cache_read_tokens,omitempty"`
	// Terminal rows preserve admission and answer readiness separately from At,
	// which records settlement. Legacy rows may omit these boundaries.
	StartedAt     *time.Time `json:"started_at,omitempty"`
	AnswerReadyAt *time.Time `json:"answer_ready_at,omitempty"`
	// RetryCount and MaxRetries carry the final retry counters of a
	// stream_reconnect meta row (the reconnect attempt the stream gave up
	// on, out of the configured cap). Zero for all other record types.
	RetryCount int `json:"retry_count,omitempty"`
	MaxRetries int `json:"max_retries,omitempty"`
	// Provider and Model identify the recorded request on token_usage and
	// turn_terminal rows. Legacy records may omit them; readers must treat
	// empty values as unknown rather than use the current session selection.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// NewID generates a human-readable, sortable session ID: YYYYMMDD-HHMMSS-xxxxxxxxxxxxxxxx.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Dir returns the user-level sessions directory.
func Dir(homeDir string) string {
	home, err := statepath.Home(homeDir)
	if err != nil {
		return ""
	}
	return statepath.SessionsDir(home)
}

// DBPath returns the retired SQLite source path for explicit legacy conversion.
func DBPath(sessDir string) string {
	return filepath.Join(sessDir, dbFileName)
}

// PinnedArchived captures the pinned/archived state of one session. Thread
// listing resolves these flags for child agent sessions in one document read.
type PinnedArchived struct {
	Pinned   bool
	Archived bool
}

type RuntimeSelection struct {
	Provider       string
	Model          string
	Variant        string
	Effort         string
	Speed          string
	ModelOptions   map[string]string
	PermissionMode string
	ApproveForMe   bool
}

// NextStepModelSelection is an atomic model-only intent. The expected revision
// and engine fence concurrent selections and scope replacement. PermissionMode
// and ApproveForMe in Selection must match stored values; they are never changed.
type NextStepModelSelection struct {
	ExpectedRevision uint64
	ExpectedEngineID string
	Selection        RuntimeSelection
}

func (s Session) WorktreeInfo() (WorktreeInfo, bool) {
	path := normalizeCWD(s.WorktreePath)
	if path == "" {
		return WorktreeInfo{}, false
	}
	return WorktreeInfo{
		Path:     path,
		BaseHEAD: strings.TrimSpace(s.WorktreeBaseHEAD),
		BaseRepo: normalizeCWD(s.WorktreeBaseRepo),
	}, true
}

func sessionActivityAt(s Session) time.Time {
	if !s.UpdatedAt.IsZero() {
		return s.UpdatedAt
	}
	return s.CreatedAt
}

func normalizeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(abs)
}
