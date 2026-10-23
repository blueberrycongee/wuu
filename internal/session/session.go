package session

import (
	"crypto/rand"

	"encoding/hex"
	"encoding/json"
	"errors"

	"net/url"

	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	dbFileName = "sessions.sqlite3"
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrSessionNotArchived  = errors.New("session is not archived")
	ErrHistoryUnavailable  = errors.New("session history unavailable")
	ErrHistorySnapshotGone = errors.New("history snapshot is no longer available")
)

// Session represents one conversation session.
type Session struct {
	ID                    string            `json:"id"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at,omitempty"`
	Title                 string            `json:"title,omitempty"`
	TitleRevision         int64             `json:"title_revision,omitempty"`
	Summary               string            `json:"summary,omitempty"`
	Entries               int               `json:"entries"`
	LatestCompletedTurnID string            `json:"latest_completed_turn_id,omitempty"`
	CWD                   string            `json:"cwd,omitempty"`
	Source                string            `json:"source,omitempty"`
	Owner                 string            `json:"owner,omitempty"`
	Visibility            string            `json:"visibility,omitempty"`
	ParentID              string            `json:"parent_id,omitempty"`
	ContextSource         string            `json:"context_source,omitempty"`
	CreationRequestID     string            `json:"creation_request_id,omitempty"`
	SeedID                string            `json:"seed_id,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	Model                 string            `json:"model,omitempty"`
	Variant               string            `json:"variant,omitempty"`
	Effort                string            `json:"effort,omitempty"`
	Speed                 string            `json:"speed,omitempty"`
	ModelOptions          map[string]string `json:"model_options,omitempty"`
	PermissionMode        string            `json:"permission_mode,omitempty"`
	ApproveForMe          bool              `json:"approve_for_me,omitempty"`
	ProjectRole           string            `json:"project_role,omitempty"`
	Instructions          string            `json:"instructions,omitempty"`
	ToolPolicyJSON        string            `json:"tool_policy_json,omitempty"`
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

// DBPath returns the SQLite database path for session state.
func DBPath(sessDir string) string {
	return filepath.Join(sessDir, dbFileName)
}

// PinnedArchived captures the pinned/archived state of one session. Thread
// listing resolves these flags for child agent sessions, where a per-session
// store open (and its schema/PRAGMA setup) dominates the cost of a big list.
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

func sqliteDSN(path string) string {

	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}

func insertSessionSQL() string {
	return `INSERT INTO sessions (
		id, created_at, updated_at, title, title_revision, summary, entries, cwd,
		forked_from_id, forked_from_turn_id, forked_from_item_id,
		pinned_at, folder_id, archived_at, archive_reason, worktree_path, worktree_base_head, worktree_base_repo,
		workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
		provider, model, variant, effort, speed, model_options_json, permission_mode, approve_for_me, engine_id, engine_ref, instructions, project_role, tool_policy_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func modelOptionsJSON(options map[string]string) string {
	encoded, _ := json.Marshal(options)
	return string(encoded)
}

func sessionArgs(sess Session) []any {
	return []any{
		sess.ID,
		timeText(sess.CreatedAt),
		timeText(sess.UpdatedAt),
		sess.Title,
		sess.TitleRevision,
		sess.Summary,
		sess.Entries,
		normalizeCWD(sess.CWD),
		sess.ForkedFromID,
		sess.ForkedFromTurnID,
		sess.ForkedFromItemID,
		nullableTimeText(sess.PinnedAt),
		strings.TrimSpace(sess.FolderID),
		nullableTimeText(sess.ArchivedAt),
		sess.ArchiveReason,
		normalizeCWD(sess.WorktreePath),
		sess.WorktreeBaseHEAD,
		normalizeCWD(sess.WorktreeBaseRepo),
		strings.TrimSpace(sess.WorkspaceID),
		strings.TrimSpace(sess.Source),
		strings.TrimSpace(sess.Owner),
		strings.TrimSpace(sess.Visibility),
		strings.TrimSpace(sess.ParentID),
		strings.TrimSpace(sess.ContextSource),
		strings.TrimSpace(sess.CreationRequestID),
		strings.TrimSpace(sess.Provider),
		strings.TrimSpace(sess.Model),
		strings.TrimSpace(sess.Variant),
		strings.TrimSpace(sess.Effort), strings.TrimSpace(sess.Speed), modelOptionsJSON(sess.ModelOptions),
		strings.TrimSpace(sess.PermissionMode),
		boolToInt(sess.ApproveForMe),
		strings.TrimSpace(sess.EngineID),
		strings.TrimSpace(sess.EngineRef),
		sess.Instructions,
		sess.ProjectRole,
		strings.TrimSpace(sess.ToolPolicyJSON),
	}
}

const historyRecordsSelect = `
	SELECT seq, role, content, display_content, origin, origin_id, cause, presentation_kind, related_session_id, read_only, phase, client_id, hidden, steered, reasoning_content,
	       provider_item_id, engine_turn_id, engine_turn_pending, provider_item_model,
	       reasoning_blocks_json, provider_items_json, content_parts_json, images_json, files_json, tool_calls_json, discovered_tools_json,
	       tool_call_id, tool_invocation_id, tool_result_kind, tool_result_json, finish_reason, stop_reason, truncated, name, at, input_tokens, output_tokens, context_tokens, cache_creation_tokens, cache_read_tokens, retry_count, max_retries,
	       provider, model
	FROM session_messages`

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		leftPinned := sessions[i].PinnedAt != nil
		rightPinned := sessions[j].PinnedAt != nil
		if leftPinned != rightPinned {
			return leftPinned
		}
		leftTime := sessionActivityAt(sessions[i])
		rightTime := sessionActivityAt(sessions[j])
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return sessions[i].ID > sessions[j].ID
	})
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

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableTimeText(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return timeText(*t)
}

func nullableValueTimeText(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return timeText(t)
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func rawJSONText(raw json.RawMessage) string {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func rawMessage(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
