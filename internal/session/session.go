package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	dbFileName = "sessions.sqlite3"
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrHistoryUnavailable  = errors.New("session history unavailable")
	ErrHistorySnapshotGone = errors.New("history snapshot is no longer available")
)

var (
	storeInitMu  sync.Mutex
	storeWriteMu sync.Mutex
)

// Session represents one conversation session.
type Session struct {
	ID                    string    `json:"id"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at,omitempty"`
	Title                 string    `json:"title,omitempty"`
	Summary               string    `json:"summary,omitempty"`
	Entries               int       `json:"entries"`
	LatestCompletedTurnID string    `json:"latest_completed_turn_id,omitempty"`
	CWD                   string    `json:"cwd,omitempty"`
	Source                string    `json:"source,omitempty"`
	Owner                 string    `json:"owner,omitempty"`
	Visibility            string    `json:"visibility,omitempty"`
	ParentID              string    `json:"parent_id,omitempty"`
	ContextSource         string    `json:"context_source,omitempty"`
	CreationRequestID     string    `json:"creation_request_id,omitempty"`
	SeedID                string    `json:"seed_id,omitempty"`
	Provider              string    `json:"provider,omitempty"`
	Model                 string    `json:"model,omitempty"`
	Variant               string    `json:"variant,omitempty"`
	Effort                string    `json:"effort,omitempty"`
	Speed                 string    `json:"speed,omitempty"`
	PermissionMode        string    `json:"permission_mode,omitempty"`
	ApproveForMe          bool      `json:"approve_for_me,omitempty"`
	Instructions          string    `json:"instructions,omitempty"`
	ToolPolicyJSON        string    `json:"tool_policy_json,omitempty"`
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

// Create initializes a new session.
// If id is non-empty, it is used as the session ID; otherwise a new one is generated.
func Create(sessDir string, id ...string) (*Session, error) {
	sessID := ""
	if len(id) > 0 {
		sessID = id[0]
	}
	return CreateWithMetadata(sessDir, sessID, "")
}

// CreateWithMetadata initializes a new session with thread-level metadata.
// The workspace identity is bound separately via SetWorkspaceID, so this
// signature stays stable.
func CreateWithMetadata(sessDir, id, cwd string) (*Session, error) {
	return createWithMetadata(sessDir, id, cwd, ForkMetadata{}, ManagedMetadata{})
}

func CreateManagedWithMetadata(sessDir, id, cwd string, managed ManagedMetadata) (*Session, error) {
	return createWithMetadata(sessDir, id, cwd, ForkMetadata{}, managed)
}

// CreateForkWithMetadata initializes a forked session with source metadata.
func CreateForkWithMetadata(sessDir, id, cwd string, fork ForkMetadata) (*Session, error) {
	return createWithMetadata(sessDir, id, cwd, fork, ManagedMetadata{})
}

func CreateManagedForkWithMetadata(sessDir, id, cwd string, fork ForkMetadata, managed ManagedMetadata) (*Session, error) {
	return createWithMetadata(sessDir, id, cwd, fork, managed)
}

// CreateWithWorktree initializes a forked session bound to an isolated git worktree.
func CreateWithWorktree(sessDir, id, cwd string, fork ForkMetadata, worktree WorktreeInfo) (*Session, error) {
	return createWithMetadataAndWorktree(sessDir, id, cwd, fork, worktree, ManagedMetadata{})
}

// CreateManagedWithWorktree initializes a plugin-owned session in an isolated
// workspace while preserving the same ownership and idempotency metadata as
// every other host.session.create call.
func CreateManagedWithWorktree(sessDir, id, cwd string, fork ForkMetadata, worktree WorktreeInfo, managed ManagedMetadata) (*Session, error) {
	return createWithMetadataAndWorktree(sessDir, id, cwd, fork, worktree, managed)
}

func createWithMetadata(sessDir, id, cwd string, fork ForkMetadata, managed ManagedMetadata) (*Session, error) {
	return createWithMetadataAndWorktree(sessDir, id, cwd, fork, WorktreeInfo{}, managed)
}

func createWithMetadataAndWorktree(sessDir, id, cwd string, fork ForkMetadata, worktree WorktreeInfo, managed ManagedMetadata) (*Session, error) {
	sessID := NewID()
	if strings.TrimSpace(id) != "" {
		sessID = strings.TrimSpace(id)
	}

	now := time.Now().UTC()
	sess := &Session{
		ID:               sessID,
		CreatedAt:        now,
		UpdatedAt:        now,
		CWD:              normalizeCWD(cwd),
		ForkedFromID:     strings.TrimSpace(fork.ForkedFromID),
		ForkedFromTurnID: strings.TrimSpace(fork.ForkedFromTurnID),
		ForkedFromItemID: strings.TrimSpace(fork.ForkedFromItemID),
		WorktreePath:     normalizeCWD(worktree.Path),
		WorktreeBaseHEAD: strings.TrimSpace(worktree.BaseHEAD),
		WorktreeBaseRepo: normalizeCWD(worktree.BaseRepo),
		Owner:            strings.TrimSpace(managed.Owner), Visibility: strings.TrimSpace(managed.Visibility),
		ParentID: strings.TrimSpace(managed.ParentID), ContextSource: strings.TrimSpace(managed.ContextSource),
		CreationRequestID: strings.TrimSpace(managed.CreationRequestID),
	}
	return CreateInitialized(sessDir, *sess, nil)
}

// CreateInitialized commits a fully prepared session and its initial durable
// history in one transaction. Callers must resolve external resources and all
// pure validation before invoking it.
func CreateInitialized(sessDir string, sess Session, records []HistoryRecord) (*Session, error) {
	return CreateInitializedWithLaunch(sessDir, sess, records, ContextSeed{}, SessionLaunchRecord{})
}

func CreateInitializedWithLaunch(sessDir string, sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord) (*Session, error) {
	sess.ID = strings.TrimSpace(sess.ID)
	if sess.ID == "" {
		sess.ID = NewID()
	}
	now := time.Now().UTC()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = now
	}

	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin initialized session create: %w", err)
	}
	defer tx.Rollback()
	if strings.TrimSpace(launch.RequestID) != "" {
		existing, ok, err := loadSessionLaunchTx(tx, launch.RequestID)
		if err != nil {
			return nil, err
		}
		if ok && existing.Status == SessionLaunchCommitted && existing.TargetSession != "" {
			if existing.Revision != launch.Revision || existing.Runtime != launch.Runtime || existing.SourceSession != launch.SourceSession || existing.SourceCutoff != launch.SourceCutoff {
				return nil, fmt.Errorf("%w: request %q", ErrSessionLaunchConflict, launch.RequestID)
			}
			loaded, found, err := findSessionTx(tx, existing.TargetSession)
			if err != nil {
				return nil, err
			}
			if found {
				if err := tx.Commit(); err != nil {
					return nil, fmt.Errorf("commit initialized session create: %w", err)
				}
				return &loaded, nil
			}
		}
	}
	if seed.Version != 0 || strings.TrimSpace(seed.Body) != "" {
		written, err := putContextSeedTx(tx, seed)
		if err != nil {
			return nil, err
		}
		seed = written
		sess.SeedID = seed.ID
		if strings.TrimSpace(sess.ContextSource) == "" {
			sess.ContextSource = "seed"
		}
		records = append([]HistoryRecord{seedHistoryRecord(seed)}, records...)
	}
	if err := insertSessionTx(tx, sess); err != nil {
		return nil, err
	}
	for index, record := range records {
		if err := insertHistoryRecordTx(tx, sess.ID, index+1, record); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(launch.RequestID) != "" {
		launch.TargetSession = sess.ID
		launch.SeedID = sess.SeedID
		launch.Status = SessionLaunchCommitted
		if _, err := putSessionLaunchTx(tx, launch); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit initialized session create: %w", err)
	}
	return &sess, nil
}

// List reads sessions and returns the most recent sessions (up to limit).
func List(sessDir string, limit int) ([]Session, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
SELECT id, created_at, updated_at, title, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, permission_mode, approve_for_me, engine_id, engine_ref, instructions, tool_policy_json,
	       COALESCE((SELECT m.client_id FROM session_messages m
	                 WHERE m.session_id = sessions.id AND m.role = 'meta'
	                   AND m.content = 'turn_terminal' AND m.client_id <> ''
	                 ORDER BY m.seq DESC LIMIT 1), '')
FROM sessions`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan sessions: %w", err)
	}

	sortSessions(sessions)
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

// ListForCWD reads sessions scoped to a workspace. When workspaceID is set
// (a registered project) sessions are matched by that stable id so they follow
// the project across moves; otherwise they are matched by cwd.
func ListForCWD(sessDir, cwd, workspaceID string, limit int) ([]Session, error) {
	return listForCWD(sessDir, cwd, workspaceID, limit)
}

func listForCWD(sessDir, cwd, workspaceID string, limit int) ([]Session, error) {
	target := normalizeCWD(cwd)
	wsID := strings.TrimSpace(workspaceID)
	if target == "" && wsID == "" {
		return List(sessDir, limit)
	}
	sessions, err := List(sessDir, 0)
	if err != nil {
		return nil, err
	}
	matchesCWD := func(s Session) bool {
		return target != "" &&
			(normalizeCWD(s.CWD) == target || normalizeCWD(s.WorktreeBaseRepo) == target)
	}
	filtered := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		if wsID != "" {
			// A workspace with a stable id (a registered project) matches by
			// that id, so its sessions follow the project across moves even
			// though CWD still records the old path. As a graceful transition,
			// sessions predating the id still match by path while they live at
			// the workspace's current location.
			if strings.TrimSpace(s.WorkspaceID) == wsID ||
				(strings.TrimSpace(s.WorkspaceID) == "" && matchesCWD(s)) {
				filtered = append(filtered, s)
			}
			continue
		}
		if matchesCWD(s) {
			filtered = append(filtered, s)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// Find returns metadata for a session ID.
func Find(sessDir, id string) (Session, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, false, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return Session{}, false, err
	}
	defer db.Close()
	return findSessionDB(db, id)
}

// PinnedArchived captures the pinned/archived state of one session. Thread
// listing resolves these flags for child agent sessions, where a per-session
// store open (and its schema/PRAGMA setup) dominates the cost of a big list.
type PinnedArchived struct {
	Pinned   bool
	Archived bool
}

// FindPinnedArchived resolves pinned/archived flags for many session IDs with
// a single store open. IDs that do not exist are simply absent from the
// result, mirroring Find's ok=false semantics. Queries are batched to stay
// well under SQLite's variable limit.
func FindPinnedArchived(sessDir string, ids []string) (map[string]PinnedArchived, error) {
	result := make(map[string]PinnedArchived, len(ids))
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return result, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	const batchSize = 500
	for start := 0; start < len(unique); start += batchSize {
		end := start + batchSize
		if end > len(unique) {
			end = len(unique)
		}
		batch := unique[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.Query(`
SELECT id, pinned_at, archived_at FROM sessions WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("find pinned/archived sessions: %w", err)
		}
		for rows.Next() {
			var id string
			var pinnedAt, archivedAt *string
			if err := rows.Scan(&id, &pinnedAt, &archivedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan pinned/archived session: %w", err)
			}
			result[id] = PinnedArchived{
				Pinned:   pinnedAt != nil,
				Archived: archivedAt != nil,
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pinned/archived sessions: %w", err)
		}
		rows.Close()
	}
	return result, nil
}

// FindManagedByRequest returns the session created by one owner for an opaque
// creation request. The pair is unique, making session.create idempotent
// across runtime restarts and concurrent retries.
func FindManagedByRequest(sessDir, owner, requestID string) (Session, bool, error) {
	owner = strings.TrimSpace(owner)
	requestID = strings.TrimSpace(requestID)
	if owner == "" || requestID == "" {
		return Session{}, false, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return Session{}, false, err
	}
	defer db.Close()
	row := db.QueryRow(`
SELECT id, created_at, updated_at, title, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
       provider, model, variant, effort, speed, permission_mode, approve_for_me, engine_id, engine_ref, instructions, tool_policy_json,
       COALESCE((SELECT m.client_id FROM session_messages m
                 WHERE m.session_id = sessions.id AND m.role = 'meta'
                   AND m.content = 'turn_terminal' AND m.client_id <> ''
                 ORDER BY m.seq DESC LIMIT 1), '')
FROM sessions
WHERE owner = ? AND creation_request_id = ?`, owner, requestID)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("find managed session: %w", err)
	}
	return sess, true, nil
}

// UpdateIndex updates the entries count and summary for a session.
func UpdateIndex(sessDir string, id string, entries int, summary string) error {
	now := time.Now().UTC()
	_, err := updateMetadata(sessDir, id, true, func(s *Session) {
		s.Entries = entries
		s.UpdatedAt = now
		if strings.TrimSpace(summary) != "" && strings.TrimSpace(s.Summary) == "" {
			s.Summary = summary
		}
	})
	return err
}

// UpdateGeneratedTitle sets a title only when the session does not already have one.
func UpdateGeneratedTitle(sessDir, id string, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, fmt.Errorf("title is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	})
}

// UpdateTitle overwrites a session title unconditionally. Differs from
// UpdateGeneratedTitle, which only fills an empty title. The right-click
// Rename menu uses UpdateTitle to overwrite both the auto-generated
// preview and any prior user-edited title.
func UpdateTitle(sessDir, id string, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, fmt.Errorf("title is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Title = title
	})
}

// UpdateWorkspaceBinding persists the workspace used by a session after the
// main agent explicitly moves the task into a linked worktree.
func UpdateWorkspaceBinding(
	sessDir, id, cwd, worktreePath, worktreeBaseHEAD, worktreeBaseRepo string,
) (Session, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return Session{}, errors.New("workspace cwd is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.CWD = cwd
		s.WorktreePath = strings.TrimSpace(worktreePath)
		s.WorktreeBaseHEAD = strings.TrimSpace(worktreeBaseHEAD)
		s.WorktreeBaseRepo = strings.TrimSpace(worktreeBaseRepo)
	})
}

// UpdatePinned marks a session as pinned or unpinned.
func UpdatePinned(sessDir, id string, pinned bool) (Session, error) {
	return updateSessionOrganization(sessDir, id, nil, &pinned)
}

// SetWorkspaceID binds a session to a stable, location-independent workspace
// identity (the desktop's registered-project id), so the session's state and
// thread listing follow the workspace across moves/renames. Set once at
// creation for registered-project threads; left empty for location-anchored
// scratch threads, which match by cwd.
func SetWorkspaceID(sessDir, id, workspaceID string) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.WorkspaceID = strings.TrimSpace(workspaceID)
	})
}

func SetSource(sessDir, id, source string) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Source = strings.TrimSpace(source)
	})
}

type RuntimeSelection struct {
	Provider       string
	Model          string
	Variant        string
	Effort         string
	Speed          string
	PermissionMode string
	ApproveForMe   bool
}

// SetRuntimeSelection persists the runtime defaults pinned to one conversation.
func SetRuntimeSelection(sessDir, id string, selection RuntimeSelection) (Session, error) {
	selection.Provider = strings.TrimSpace(selection.Provider)
	selection.Model = strings.TrimSpace(selection.Model)
	if selection.Provider == "" {
		return Session{}, fmt.Errorf("provider is required")
	}
	if selection.Model == "" && !allowsEmptyModel(sessDir, id) {
		return Session{}, fmt.Errorf("model is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Provider = selection.Provider
		s.Model = selection.Model
		s.Variant = strings.TrimSpace(selection.Variant)
		s.Effort = strings.TrimSpace(selection.Effort)
		s.Speed = selection.Speed
		s.PermissionMode = strings.TrimSpace(selection.PermissionMode)
		s.ApproveForMe = selection.ApproveForMe
	})
}

// SetEngine binds a session to an agent engine. A thread binds its engine at
// creation and never silently switches; changing it requires an explicit
// product action. The engine id must be non-empty; callers pass the
// normalized id of a known engine (the built-in id is "wuu").
// Protocol engines may persist an empty model to mean the agent's native default.
func allowsEmptyModel(sessDir, id string) bool {
	sess, ok, err := Find(sessDir, id)
	if err != nil || !ok {
		return false
	}
	engineID := strings.TrimSpace(sess.EngineID)
	return engineID != "" && engineID != "wuu"
}

func SetEngine(sessDir, id, engineID string) (Session, error) {
	engineID = strings.TrimSpace(engineID)
	if engineID == "" {
		return Session{}, fmt.Errorf("engine id is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.EngineID = engineID
	})
}

// SetEngineRef stores the engine's native session reference for a thread
// (for example a codex thread id). It is written once the engine creates the
// native session and read back to resume it.
func SetEngineRef(sessDir, id, ref string) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.EngineRef = strings.TrimSpace(ref)
	})
}

// SetModelSelection preserves the pre-runtime-selection API for callers that
// only know about model variants.
func SetModelSelection(sessDir, id, provider, model, variant string) (Session, error) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return Session{}, fmt.Errorf("provider is required")
	}
	if model == "" {
		return Session{}, fmt.Errorf("model is required")
	}
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Provider = provider
		s.Model = model
		s.Variant = strings.TrimSpace(variant)
	})
}

// UpdateArchived marks a session as archived or active.
func UpdateArchived(sessDir, id string, archived bool) (Session, error) {
	now := time.Now().UTC()
	return updateMetadata(sessDir, id, false, func(s *Session) {
		if archived {
			s.ArchivedAt = &now
			s.PinnedAt = nil
		} else {
			s.ArchivedAt = nil
			s.ArchiveReason = ""
		}
	})
}

func BindWorktree(sessDir, id string, worktree WorktreeInfo) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.WorktreePath = normalizeCWD(worktree.Path)
		s.WorktreeBaseHEAD = strings.TrimSpace(worktree.BaseHEAD)
		s.WorktreeBaseRepo = normalizeCWD(worktree.BaseRepo)
		if s.CWD == "" {
			s.CWD = s.WorktreePath
		}
	})
}

func WorktreeInfoForSession(sessDir, id string) (WorktreeInfo, bool, error) {
	sess, ok, err := Find(sessDir, id)
	if err != nil || !ok {
		return WorktreeInfo{}, false, err
	}
	info, bound := sess.WorktreeInfo()
	return info, bound, nil
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

// Delete removes a session and its durable history records.
func Delete(sessDir, id string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	db, err := openStore(sessDir)
	if err != nil {
		return Session{}, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return Session{}, fmt.Errorf("begin session delete: %w", err)
	}
	defer tx.Rollback()

	deleted, ok, err := findSessionTx(tx, id)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	if _, err := tx.Exec(`DELETE FROM plugin_turn_lifecycle_outbox
		WHERE EXISTS (
			SELECT 1 FROM plugin_turn_lifecycle retained
			WHERE retained.plugin_id = plugin_turn_lifecycle_outbox.plugin_id
				AND retained.request_id = plugin_turn_lifecycle_outbox.request_id
				AND retained.session_id = ?
		)`, id); err != nil {
		return Session{}, fmt.Errorf("delete session lifecycle outbox: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM plugin_turn_lifecycle WHERE session_id = ?`, id); err != nil {
		return Session{}, fmt.Errorf("delete retained session lifecycle: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return Session{}, fmt.Errorf("delete session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit session delete: %w", err)
	}
	return deleted, nil
}

func updateMetadata(sessDir, id string, missingOK bool, update func(*Session)) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		if missingOK {
			return Session{}, nil
		}
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	db, err := openStore(sessDir)
	if err != nil {
		return Session{}, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return Session{}, fmt.Errorf("begin session update: %w", err)
	}
	defer tx.Rollback()

	s, ok, err := findSessionTx(tx, id)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		if missingOK {
			return Session{}, nil
		}
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	update(&s)
	if err := updateSessionTx(tx, s); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit session update: %w", err)
	}
	return s, nil
}

// MostRecent returns the most recent session ID, or empty string if none.
func MostRecent(sessDir string) (string, error) {
	sessions, err := List(sessDir, 1)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

// MostRecentForCWD returns the most recent session for a workspace (by stable
// id when workspaceID is set, else by cwd).
func MostRecentForCWD(sessDir, cwd, workspaceID string) (string, error) {
	sessions, err := ListForCWD(sessDir, cwd, workspaceID, 1)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

// AppendHistoryRecord appends one durable history record to a session.
func AppendHistoryRecord(sessDir, id string, rec HistoryRecord) error {
	_, err := AppendHistoryRecordReturningSeq(sessDir, id, rec)
	return err
}

// AppendHistoryRecordReturningSeq is AppendHistoryRecord but also returns the
// seq assigned to the new record — its stable address within the thread.
func AppendHistoryRecordReturningSeq(sessDir, id string, rec HistoryRecord) (int, error) {
	return AppendControlledHistoryRecord(sessDir, id, rec, nil)
}

// AppendControlledHistoryRecord atomically fences automatic input against a
// control change. An accepted input cannot appear after a committed takeover.
func AppendControlledHistoryRecord(sessDir, id string, rec HistoryRecord, control *Control) (int, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin history append: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, id); err != nil {
		return 0, err
	} else if !ok {
		return 0, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	if control != nil {
		var manager, state string
		var revision int64
		if err := tx.QueryRow(`SELECT manager_id,revision,state FROM session_controls WHERE session_id=?`, id).Scan(&manager, &revision, &state); err != nil {
			return 0, err
		}
		if control.SessionID != id || manager != control.ManagerID || revision != control.Revision || state != ControlActive {
			return 0, ErrControlChanged
		}
	}
	seq, err := appendHistoryRecordTx(tx, id, rec)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit history append: %w", err)
	}
	return seq, nil
}

// AppendHistoryRecords appends a related history segment in one transaction.
// Tool-call declarations and their results must use this path so a crash can
// never expose a partially projected provider message sequence.
func AppendHistoryRecords(sessDir, id string, records []HistoryRecord) error {
	_, _, err := AppendHistoryRecordsReturningRange(sessDir, id, records)
	return err
}

// AppendHistoryRecordsReturningRange appends a related history segment in one
// transaction and returns the inclusive sequence range assigned to it. An empty
// segment returns (0, 0, nil).
func AppendHistoryRecordsReturningRange(sessDir, id string, records []HistoryRecord) (int, int, error) {
	if len(records) == 0 {
		return 0, 0, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin history batch append: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, id); err != nil {
		return 0, 0, err
	} else if !ok {
		return 0, 0, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	startSeq := 0
	endSeq := 0
	for _, rec := range records {
		seq, err := appendHistoryRecordTx(tx, id, rec)
		if err != nil {
			return 0, 0, err
		}
		if startSeq == 0 {
			startSeq = seq
		}
		endSeq = seq
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit history batch append: %w", err)
	}
	return startSeq, endSeq, nil
}

// RewriteHistoryRecords replaces a session's durable history records.
func RewriteHistoryRecords(sessDir, id string, records []HistoryRecord) error {
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin history rewrite: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, id); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	if _, err := tx.Exec(`DELETE FROM session_messages WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("clear session history: %w", err)
	}
	// Physical replacement reuses sequence addresses, so branch ranges from
	// the previous transcript no longer identify the same records.
	if _, err := tx.Exec(`DELETE FROM session_history_retractions WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("clear session history retractions: %w", err)
	}
	for i, rec := range records {
		if err := insertHistoryRecordTx(tx, id, i+1, rec); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit history rewrite: %w", err)
	}
	return nil
}

// LoadHistoryRecords returns history records in write order.
func LoadHistoryRecords(sessDir, id string, includeMeta bool) ([]HistoryRecord, error) {
	return loadHistoryRecords(sessDir, id, includeMeta, false)
}

// LoadActiveHistoryRecords returns the current conversation branch, including
// history released by compaction but excluding suffixes retracted by editing.
// Use LoadHistoryRecords or ReadHistoryQuery to inspect the physical audit log.
func LoadActiveHistoryRecords(sessDir, id string, includeMeta bool) ([]HistoryRecord, error) {
	return loadHistoryRecords(sessDir, id, includeMeta, true)
}

func loadHistoryRecords(sessDir, id string, includeMeta, activeOnly bool) ([]HistoryRecord, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if ok, err := sessionExistsDB(db, id); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	return loadHistoryRecordsDB(db, id, includeMeta, activeOnly)
}

func openStore(sessDir string) (*sql.DB, error) {
	// Directory at 0o700, file at 0o600. modernc.org/sqlite ignores
	// mode arguments in its DSN parser and falls back to the umask when
	// creating the DB file, so we pre-create the file at the right mode
	// before handing it to the driver. The driver re-uses an existing
	// file instead of recreating it, so the mode set here is what the
	// file ends up with on disk.
	if err := securefs.Mkdir(sessDir); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	dbPath := DBPath(sessDir)
	if err := securefs.PreCreateFile(dbPath); err != nil {
		return nil, fmt.Errorf("precreate sessions db: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sessions database: %w", err)
	}
	db.SetMaxOpenConns(1)
	storeInitMu.Lock()
	defer storeInitMu.Unlock()
	if err := configureDB(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenStore exposes the shared sessions database to sibling persistence
// components that own independent tables, such as the durable tool ledger.
func OpenStore(sessDir string) (*sql.DB, error) {
	return openStore(sessDir)
}

// openStoreForScan opens the sessions database for a pure read-only scan.
// Unlike openStore it never creates the directory, database file, or schema:
// recovery scans run on their own cadence after arbitrary external cleanup,
// and a reader that resurrects the store it is probing would fight that
// cleanup forever. A missing store reports ok=false and means there is
// nothing to scan.
func openStoreForScan(sessDir string) (*sql.DB, bool, error) {
	dbPath := DBPath(sessDir)
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("probe sessions database: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, false, fmt.Errorf("open sessions database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, true, nil
}

// storeTableExists reports whether a table is present without running the
// schema migration, so scan paths can treat a store from before the table's
// introduction as having no rows instead of creating the table.
func storeTableExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe table %q: %w", name, err)
	}
	return true, nil
}

func sqliteDSN(path string) string {
	// Normalize Windows paths for file URI: C:\Users\... → /C:/Users/...
	// On Unix filepath.ToSlash is a no-op and paths already start with /.
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

func configureDB(db *sql.DB) error {
	pragmas := []string{
		`PRAGMA auto_vacuum = INCREMENTAL`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, stmt := range pragmas {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("configure sessions database: %w", err)
		}
	}
	return nil
}

func migrateSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			entries INTEGER NOT NULL DEFAULT 0,
			cwd TEXT NOT NULL DEFAULT '',
			forked_from_id TEXT NOT NULL DEFAULT '',
			forked_from_turn_id TEXT NOT NULL DEFAULT '',
			forked_from_item_id TEXT NOT NULL DEFAULT '',
			pinned_at TEXT,
			folder_id TEXT NOT NULL DEFAULT '',
			archived_at TEXT,
			worktree_path TEXT NOT NULL DEFAULT '',
			worktree_base_head TEXT NOT NULL DEFAULT '',
			worktree_base_repo TEXT NOT NULL DEFAULT '',
			owner TEXT NOT NULL DEFAULT '',
			visibility TEXT NOT NULL DEFAULT '',
			parent_id TEXT NOT NULL DEFAULT '',
			context_source TEXT NOT NULL DEFAULT '',
			creation_request_id TEXT NOT NULL DEFAULT '',
			instructions TEXT NOT NULL DEFAULT '',
			tool_policy_json TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS session_folders (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd ON sessions(cwd)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at, id)`,
		`CREATE TABLE IF NOT EXISTS session_messages (
				session_id TEXT NOT NULL,
				seq INTEGER NOT NULL,
				role TEXT NOT NULL,
				content TEXT NOT NULL DEFAULT '',
				display_content TEXT NOT NULL DEFAULT '',
				origin TEXT NOT NULL DEFAULT '',
				origin_id TEXT NOT NULL DEFAULT '',
				cause TEXT NOT NULL DEFAULT '',
				presentation_kind TEXT NOT NULL DEFAULT '',
				related_session_id TEXT NOT NULL DEFAULT '',
				read_only INTEGER NOT NULL DEFAULT 0,
				phase TEXT NOT NULL DEFAULT '',
				provider_item_id TEXT NOT NULL DEFAULT '',
				provider_item_model TEXT NOT NULL DEFAULT '',
				client_id TEXT NOT NULL DEFAULT '',
				hidden INTEGER NOT NULL DEFAULT 0,
				steered INTEGER NOT NULL DEFAULT 0,
				reasoning_content TEXT NOT NULL DEFAULT '',
			reasoning_blocks_json TEXT NOT NULL DEFAULT '',
			content_parts_json TEXT NOT NULL DEFAULT '',
			images_json TEXT NOT NULL DEFAULT '',
			files_json TEXT NOT NULL DEFAULT '',
			tool_calls_json TEXT NOT NULL DEFAULT '',
			discovered_tools_json TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT NOT NULL DEFAULT '',
			tool_invocation_id TEXT NOT NULL DEFAULT '',
			tool_result_kind TEXT NOT NULL DEFAULT '',
			tool_result_json TEXT NOT NULL DEFAULT '',
			finish_reason TEXT NOT NULL DEFAULT '',
			stop_reason TEXT NOT NULL DEFAULT '',
			truncated INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			at TEXT,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			context_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(session_id, seq),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_role ON session_messages(session_id, role, seq)`,
		`CREATE TABLE IF NOT EXISTS session_controls (
			session_id TEXT PRIMARY KEY,
			manager_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			state TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS plugin_turn_lifecycle_outbox (
				plugin_id TEXT NOT NULL,
				request_id TEXT NOT NULL,
				payload_json TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				PRIMARY KEY(plugin_id, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plugin_turn_lifecycle_outbox_updated
			ON plugin_turn_lifecycle_outbox(updated_at, plugin_id, request_id)`,
		`CREATE TABLE IF NOT EXISTS plugin_turn_lifecycle (
			plugin_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			queue_id TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(plugin_id, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plugin_turn_lifecycle_session
			ON plugin_turn_lifecycle(plugin_id, session_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS held_user_work (
				session_id TEXT NOT NULL,
				position INTEGER NOT NULL,
				id TEXT NOT NULL,
				origin TEXT NOT NULL,
				message_json TEXT NOT NULL,
				runtime_json TEXT NOT NULL,
				PRIMARY KEY(session_id, id),
				UNIQUE(session_id, position),
				FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS session_history_retractions (
			session_id TEXT NOT NULL,
			from_seq INTEGER NOT NULL,
			through_seq INTEGER NOT NULL,
			PRIMARY KEY(session_id, from_seq, through_seq),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_history_checkpoints (
			session_id      TEXT NOT NULL,
			version         INTEGER NOT NULL,
			kind            TEXT NOT NULL,
			through_seq     INTEGER NOT NULL,
			replacement_json TEXT NOT NULL,
			created_at      TEXT NOT NULL,
			PRIMARY KEY(session_id, version),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_driver_checkpoints (
			session_id       TEXT PRIMARY KEY,
			contract_version INTEGER NOT NULL,
			driver_id        TEXT NOT NULL,
			driver_version   TEXT NOT NULL,
			state_json       TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_compaction_notes (
			session_id       TEXT NOT NULL,
			provider_key     TEXT NOT NULL,
			markdown         TEXT NOT NULL,
			covered_messages INTEGER NOT NULL,
			covered_hash     TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			PRIMARY KEY(session_id, provider_key),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_model_input_receipts (
			session_id       TEXT NOT NULL,
			operation_id     TEXT NOT NULL,
			contract_version INTEGER NOT NULL,
			driver_id        TEXT NOT NULL,
			driver_version   TEXT NOT NULL,
			receipt_json     TEXT NOT NULL,
			created_at       TEXT NOT NULL,
			PRIMARY KEY(session_id, operation_id),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS participants (
			id         TEXT PRIMARY KEY,
			kind       TEXT NOT NULL,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL DEFAULT '',
			avatar     TEXT NOT NULL DEFAULT '',
			tagline    TEXT NOT NULL DEFAULT '',
			workspace  TEXT NOT NULL DEFAULT '',
			model      TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			retired_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_participants_named_name ON participants(name) WHERE retired_at IS NULL AND kind = 'named'`,
		`CREATE TABLE IF NOT EXISTS participant_runs (
			id             TEXT PRIMARY KEY,
			participant_id TEXT NOT NULL,
			agent_id       TEXT NOT NULL DEFAULT '',
			task_id        TEXT NOT NULL DEFAULT '',
			session_id      TEXT NOT NULL DEFAULT '',
			summary        TEXT NOT NULL DEFAULT '',
			outcome        TEXT NOT NULL DEFAULT '',
			created_at     TEXT NOT NULL,
			updated_at     TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_participant_runs_participant ON participant_runs(participant_id, created_at, id)`,
		// inference_journal_runtimes is the cross-process liveness lease used to
		// distinguish a crashed writer from another live app-server or CLI.
		`CREATE TABLE IF NOT EXISTS inference_journal_runtimes (
			id                 TEXT PRIMARY KEY,
			workspace_scope    TEXT NOT NULL,
			pid                INTEGER NOT NULL,
			started_at         INTEGER NOT NULL,
			heartbeat_at       INTEGER NOT NULL,
			closed_at          INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_journal_runtimes_scope
		 ON inference_journal_runtimes(workspace_scope, heartbeat_at, closed_at)`,
		`CREATE TABLE IF NOT EXISTS inference_workflows (
			id                         TEXT PRIMARY KEY,
			runtime_id                 TEXT NOT NULL,
			workspace_scope            TEXT NOT NULL,
			owner_id                   TEXT NOT NULL,
			workload_profile           TEXT NOT NULL,
			max_operations             INTEGER NOT NULL DEFAULT -1,
			max_attempts               INTEGER NOT NULL DEFAULT -1,
			max_submissions            INTEGER NOT NULL DEFAULT -1,
			max_replays                INTEGER NOT NULL DEFAULT -1,
			max_transport_switches     INTEGER NOT NULL DEFAULT -1,
			max_credential_refreshes   INTEGER NOT NULL DEFAULT -1,
			max_payload_transforms     INTEGER NOT NULL DEFAULT -1,
			max_child_operations       INTEGER NOT NULL DEFAULT -1,
			max_recovery_wait_ms       INTEGER NOT NULL DEFAULT -1,
			max_unknown_billable       INTEGER NOT NULL DEFAULT -1,
			max_usage_tokens           INTEGER NOT NULL DEFAULT -1,
			used_operations            INTEGER NOT NULL DEFAULT 0,
			used_attempts              INTEGER NOT NULL DEFAULT 0,
			used_submissions           INTEGER NOT NULL DEFAULT 0,
			used_replays               INTEGER NOT NULL DEFAULT 0,
			used_transport_switches    INTEGER NOT NULL DEFAULT 0,
			used_credential_refreshes  INTEGER NOT NULL DEFAULT 0,
			used_payload_transforms    INTEGER NOT NULL DEFAULT 0,
			used_child_operations      INTEGER NOT NULL DEFAULT 0,
			used_recovery_wait_ms      INTEGER NOT NULL DEFAULT 0,
			known_submissions          INTEGER NOT NULL DEFAULT 0,
			estimated_submissions      INTEGER NOT NULL DEFAULT 0,
			unknown_billable           INTEGER NOT NULL DEFAULT 0,
			known_usage_tokens         INTEGER NOT NULL DEFAULT 0,
			estimated_usage_tokens     INTEGER NOT NULL DEFAULT 0,
			status                     TEXT NOT NULL DEFAULT 'active',
			created_at                 INTEGER NOT NULL,
			updated_at                 INTEGER NOT NULL,
			terminal_at                INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(runtime_id) REFERENCES inference_journal_runtimes(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_workflows_owner
		 ON inference_workflows(owner_id, created_at, id)`,
		// inference_operations / attempts / submissions form the metadata-only
		// write-ahead journal for provider requests. They intentionally store a
		// SHA-256 request identity instead of prompt or wire payload content.
		`CREATE TABLE IF NOT EXISTS inference_operations (
			id                 TEXT PRIMARY KEY,
			workflow_id        TEXT NOT NULL,
			parent_operation_id TEXT NOT NULL DEFAULT '',
			attempt_limit      INTEGER NOT NULL,
			runtime_id         TEXT NOT NULL,
			workspace_scope    TEXT NOT NULL,
			owner_id           TEXT NOT NULL,
			kind               TEXT NOT NULL,
			workload_profile   TEXT NOT NULL,
			payload_version    INTEGER NOT NULL,
			request_hash       TEXT NOT NULL,
			status             TEXT NOT NULL,
			terminal_outcome   TEXT NOT NULL DEFAULT '',
			recovery_action    TEXT NOT NULL DEFAULT '',
			failure_origin     TEXT NOT NULL DEFAULT '',
			failure_category   TEXT NOT NULL DEFAULT '',
			provider_family    TEXT NOT NULL DEFAULT '',
			provider_code      TEXT NOT NULL DEFAULT '',
			http_status        INTEGER NOT NULL DEFAULT 0,
			confidence         TEXT NOT NULL DEFAULT '',
			failure_message    TEXT NOT NULL DEFAULT '',
			created_at         INTEGER NOT NULL,
			updated_at         INTEGER NOT NULL,
			terminal_at        INTEGER NOT NULL DEFAULT 0
			,FOREIGN KEY(runtime_id) REFERENCES inference_journal_runtimes(id)
			,FOREIGN KEY(workflow_id) REFERENCES inference_workflows(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_operations_recovery
		 ON inference_operations(workspace_scope, status, runtime_id, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_operations_owner
		 ON inference_operations(owner_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS inference_attempts (
			id                 TEXT PRIMARY KEY,
			operation_id       TEXT NOT NULL,
			ordinal            INTEGER NOT NULL,
			request_hash       TEXT NOT NULL,
			phase              TEXT NOT NULL,
			terminal_outcome   TEXT NOT NULL DEFAULT '',
			recovery_action    TEXT NOT NULL DEFAULT '',
			retry_at           INTEGER NOT NULL DEFAULT 0,
			failure_origin     TEXT NOT NULL DEFAULT '',
			failure_category   TEXT NOT NULL DEFAULT '',
			provider_family    TEXT NOT NULL DEFAULT '',
			provider_code      TEXT NOT NULL DEFAULT '',
			http_status        INTEGER NOT NULL DEFAULT 0,
			confidence         TEXT NOT NULL DEFAULT '',
			failure_message    TEXT NOT NULL DEFAULT '',
			prepared_at        INTEGER NOT NULL,
			dispatching_at     INTEGER NOT NULL DEFAULT 0,
			sent_at            INTEGER NOT NULL DEFAULT 0,
			first_event_at     INTEGER NOT NULL DEFAULT 0,
			terminal_at        INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(operation_id) REFERENCES inference_operations(id) ON DELETE CASCADE,
			UNIQUE(operation_id, ordinal),
			UNIQUE(id, operation_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_attempts_operation
		 ON inference_attempts(operation_id, ordinal)`,
		`CREATE TABLE IF NOT EXISTS inference_submissions (
			id                         TEXT PRIMARY KEY,
			operation_id               TEXT NOT NULL,
			attempt_id                 TEXT NOT NULL,
			ordinal                    INTEGER NOT NULL,
			attempt_ordinal            INTEGER NOT NULL,
			provider                   TEXT NOT NULL DEFAULT '',
			protocol                   TEXT NOT NULL DEFAULT '',
			transport                  TEXT NOT NULL DEFAULT '',
			mode                       TEXT NOT NULL DEFAULT '',
			reason                     TEXT NOT NULL DEFAULT '',
			outcome                    TEXT NOT NULL,
			failure_category           TEXT NOT NULL DEFAULT '',
			cost_state                 TEXT NOT NULL,
			reported_input_tokens      INTEGER NOT NULL DEFAULT 0,
			reported_output_tokens     INTEGER NOT NULL DEFAULT 0,
			reported_cache_creation    INTEGER NOT NULL DEFAULT 0,
			reported_cache_read        INTEGER NOT NULL DEFAULT 0,
			reported_cache_unknown     INTEGER NOT NULL DEFAULT 0,
			has_reported_usage         INTEGER NOT NULL DEFAULT 0,
			estimated_input_tokens     INTEGER NOT NULL DEFAULT 0,
			estimated_output_tokens    INTEGER NOT NULL DEFAULT 0,
			estimated_cache_creation   INTEGER NOT NULL DEFAULT 0,
			estimated_cache_read       INTEGER NOT NULL DEFAULT 0,
			estimated_cache_unknown    INTEGER NOT NULL DEFAULT 0,
			has_estimated_usage        INTEGER NOT NULL DEFAULT 0,
			output_bytes               INTEGER NOT NULL DEFAULT 0,
			started_at                 INTEGER NOT NULL,
			completed_at               INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(operation_id) REFERENCES inference_operations(id) ON DELETE CASCADE,
			FOREIGN KEY(attempt_id, operation_id) REFERENCES inference_attempts(id, operation_id) ON DELETE CASCADE,
			UNIQUE(operation_id, ordinal)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_attempts_id_operation
		 ON inference_attempts(id, operation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_inference_submissions_attempt
		 ON inference_submissions(attempt_id, ordinal)`,
		`CREATE TRIGGER IF NOT EXISTS trg_inference_submission_attempt_operation_insert
		 BEFORE INSERT ON inference_submissions
		 WHEN NOT EXISTS (
			SELECT 1 FROM inference_attempts a
			WHERE a.id = NEW.attempt_id AND a.operation_id = NEW.operation_id
		 )
		 BEGIN
			SELECT RAISE(ABORT, 'inference submission attempt/operation mismatch');
		 END`,
		`CREATE TABLE IF NOT EXISTS tool_batches (
			id              TEXT PRIMARY KEY,
			owner_id        TEXT NOT NULL,
			operation_id    TEXT NOT NULL,
			step_index      INTEGER NOT NULL,
			status          TEXT NOT NULL,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			terminal_at     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_batches_owner
		 ON tool_batches(owner_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS tool_invocations (
			id                TEXT PRIMARY KEY,
			batch_id          TEXT NOT NULL,
			provider_call_id  TEXT NOT NULL,
			tool_name         TEXT NOT NULL,
			tool_kind         TEXT NOT NULL DEFAULT '',
			arguments_json    TEXT NOT NULL,
			replay_policy     TEXT NOT NULL,
			state             TEXT NOT NULL,
			result_json       TEXT NOT NULL DEFAULT '',
			prepared_at       INTEGER NOT NULL,
			running_at        INTEGER NOT NULL DEFAULT 0,
			settled_at        INTEGER NOT NULL DEFAULT 0,
			projected_at      INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(batch_id) REFERENCES tool_batches(id) ON DELETE CASCADE,
			UNIQUE(batch_id, provider_call_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_invocations_batch
		 ON tool_invocations(batch_id, prepared_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_invocations_projection
		 ON tool_invocations(state, projected_at, settled_at)`,
		`CREATE TABLE IF NOT EXISTS execution_runs (
				id              TEXT PRIMARY KEY,
				runtime_id      TEXT NOT NULL DEFAULT '',
				status          TEXT NOT NULL,
				request_json    TEXT NOT NULL,
				runtime_json    TEXT NOT NULL,
				thread_id       TEXT NOT NULL DEFAULT '',
				workspace_id    TEXT NOT NULL DEFAULT '',
				workspace_root  TEXT NOT NULL DEFAULT '',
				result_json     TEXT NOT NULL DEFAULT '',
				error_json      TEXT NOT NULL DEFAULT '',
				created_at      INTEGER NOT NULL,
				started_at      INTEGER NOT NULL DEFAULT 0,
				updated_at      INTEGER NOT NULL,
				completed_at    INTEGER NOT NULL DEFAULT 0,
				FOREIGN KEY(runtime_id) REFERENCES inference_journal_runtimes(id)
			)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_runs_updated
		 ON execution_runs(updated_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_runs_workspace
		 ON execution_runs(workspace_id, workspace_root, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_runs_thread
		 ON execution_runs(thread_id, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_runs_active_thread
		 ON execution_runs(thread_id) WHERE status IN ('accepted', 'running')`,
		`CREATE INDEX IF NOT EXISTS idx_execution_runs_runtime
		 ON execution_runs(runtime_id, status, updated_at)`,
		`CREATE TABLE IF NOT EXISTS thread_execution_resets (
			thread_id    TEXT PRIMARY KEY,
			generation   INTEGER NOT NULL DEFAULT 0,
			requested_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS execution_run_turns (
				run_id          TEXT NOT NULL,
				thread_id       TEXT NOT NULL,
				turn_id         TEXT NOT NULL,
				ordinal         INTEGER NOT NULL,
				trace_path      TEXT NOT NULL DEFAULT '',
				attached_at     INTEGER NOT NULL,
				PRIMARY KEY(run_id, turn_id),
				UNIQUE(run_id, ordinal),
				UNIQUE(thread_id, turn_id),
				FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
			)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_run_turns_turn
		 ON execution_run_turns(turn_id, run_id)`,
		`CREATE TABLE IF NOT EXISTS session_plugin_generation_snapshots (
			session_id    TEXT PRIMARY KEY,
			snapshot_json TEXT NOT NULL,
			updated_at    TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS context_seeds (
			id TEXT PRIMARY KEY,
			version INTEGER NOT NULL,
			body TEXT NOT NULL,
			source_session_id TEXT NOT NULL,
			source_through_seq INTEGER NOT NULL,
			references_json TEXT NOT NULL DEFAULT '[]',
			artifacts_json TEXT NOT NULL DEFAULT '[]',
			provenance_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(source_session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS session_launches (
			request_id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			source_session_id TEXT NOT NULL DEFAULT '',
			source_cutoff_seq INTEGER NOT NULL DEFAULT 0,
			seed_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT NOT NULL DEFAULT '',
			initial_turn_id TEXT NOT NULL DEFAULT '',
			owner TEXT NOT NULL DEFAULT '',
			producer TEXT NOT NULL DEFAULT '',
			runtime_json TEXT NOT NULL DEFAULT '{}',
			input_json TEXT NOT NULL DEFAULT '{}',
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			committed_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_launches_target ON session_launches(target_session_id) WHERE target_session_id <> ''`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate sessions database: %w", err)
		}
	}
	if err := addColumnIfMissing(db, "inference_operations", "workflow_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "inference_operations", "parent_operation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "inference_operations", "attempt_limit", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "inference_operations", "failure_message", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "inference_attempts", "failure_message", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Existing journal rows predate workflow identity. Give each historical
	// operation a conservative synthetic workflow and rebuild exact counters
	// from its durable attempts/submissions without inventing hard limits.
	if _, err := db.Exec(`
INSERT OR IGNORE INTO inference_workflows (
    id, runtime_id, workspace_scope, owner_id, workload_profile,
    used_operations, used_attempts, used_submissions, used_replays,
    known_submissions, estimated_submissions, unknown_billable,
    known_usage_tokens, estimated_usage_tokens, status, created_at, updated_at, terminal_at
)
SELECT 'iwf-legacy-' || o.id, o.runtime_id, o.workspace_scope, o.owner_id, o.workload_profile,
       1,
       (SELECT COUNT(*) FROM inference_attempts a WHERE a.operation_id = o.id),
       (SELECT COUNT(*) FROM inference_submissions s WHERE s.operation_id = o.id),
       MAX(0, (SELECT COUNT(*) FROM inference_attempts a WHERE a.operation_id = o.id) - 1),
       (SELECT COUNT(*) FROM inference_submissions s WHERE s.operation_id = o.id AND s.cost_state = 'known'),
       (SELECT COUNT(*) FROM inference_submissions s WHERE s.operation_id = o.id AND s.cost_state = 'estimated'),
       (SELECT COUNT(*) FROM inference_submissions s WHERE s.operation_id = o.id AND s.cost_state = 'unknown_but_billable'),
       COALESCE((SELECT SUM(reported_input_tokens + reported_output_tokens + reported_cache_creation + reported_cache_read)
                 FROM inference_submissions s WHERE s.operation_id = o.id AND s.cost_state = 'known'), 0),
       COALESCE((SELECT SUM(estimated_input_tokens + estimated_output_tokens + estimated_cache_creation + estimated_cache_read)
                 FROM inference_submissions s WHERE s.operation_id = o.id AND s.cost_state = 'estimated'), 0),
       o.status, o.created_at, o.updated_at, o.terminal_at
FROM inference_operations o
WHERE o.workflow_id = ''`); err != nil {
		return fmt.Errorf("migrate inference workflows: %w", err)
	}
	if _, err := db.Exec(`
UPDATE inference_operations
SET workflow_id = 'iwf-legacy-' || id,
    attempt_limit = MAX(attempt_limit, (SELECT COUNT(*) FROM inference_attempts a WHERE a.operation_id = inference_operations.id), 1)
WHERE workflow_id = ''`); err != nil {
		return fmt.Errorf("backfill inference workflow identity: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_inference_operations_workflow
		ON inference_operations(workflow_id, created_at, id)`); err != nil {
		return fmt.Errorf("migrate inference workflow index: %w", err)
	}
	if err := addColumnIfMissing(db, "tool_invocations", "parent_invocation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "phase", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "display_content", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "origin", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "origin_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "cause", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "presentation_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "related_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "read_only", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "provider_item_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "provider_item_model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "hidden", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "content_parts_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "cache_creation_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "cache_read_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "context_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "retry_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "max_retries", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "tool_result_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "tool_invocation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "tool_result_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "finish_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "stop_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "truncated", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "discovered_tools_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "session_messages", "model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "worktree_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "archive_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "worktree_base_head", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "worktree_base_repo", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "workspace_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []string{"owner", "visibility", "parent_id", "context_source", "creation_request_id"} {
		if err := addColumnIfMissing(db, "sessions", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_managed_request ON sessions(owner, creation_request_id) WHERE owner <> '' AND creation_request_id <> ''`); err != nil {
		return fmt.Errorf("migrate managed session request index: %w", err)
	}
	if err := addColumnIfMissing(db, "sessions", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "variant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "speed", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "effort", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "permission_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "approve_for_me", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "engine_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "engine_ref", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "instructions", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "tool_policy_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "folder_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	// Pin groups are gone: pinned state lives solely in pinned_at. Drop the
	// legacy table and column so old installs converge on the new schema.
	if _, err := db.Exec(`DROP TABLE IF EXISTS pin_groups`); err != nil {
		return fmt.Errorf("drop legacy pin groups: %w", err)
	}
	if err := dropColumnIfPresent(db, "sessions", "pin_group_id"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id)`); err != nil {
		return fmt.Errorf("migrate sessions database: %w", err)
	}
	if err := addColumnIfMissing(db, "inference_journal_runtimes", "pid", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "sessions", "seed_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func addColumnIfMissing(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan %s columns: %w", table, err)
	}
	if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		// Multiple workspace app-servers share one sessions DB and migrate
		// concurrently; treat a racing ADD as success when the column landed.
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

func dropColumnIfPresent(db *sql.DB, table, column string) error {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan %s columns: %w", table, err)
	}
	if !found {
		return nil
	}
	if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, column)); err != nil {
		return fmt.Errorf("drop %s.%s column: %w", table, column, err)
	}
	return nil
}

func insertSessionTx(tx *sql.Tx, sess Session) error {
	_, err := tx.Exec(insertSessionSQL(), sessionArgs(sess)...)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func insertSessionSQL() string {
	return `INSERT INTO sessions (
		id, created_at, updated_at, title, summary, entries, cwd,
		forked_from_id, forked_from_turn_id, forked_from_item_id,
		pinned_at, folder_id, archived_at, archive_reason, worktree_path, worktree_base_head, worktree_base_repo,
		workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
		provider, model, variant, effort, speed, permission_mode, approve_for_me, engine_id, engine_ref, instructions, tool_policy_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func updateSessionTx(tx *sql.Tx, sess Session) error {
	_, err := tx.Exec(`
UPDATE sessions
SET created_at = ?, updated_at = ?, title = ?, summary = ?, entries = ?, cwd = ?,
    forked_from_id = ?, forked_from_turn_id = ?, forked_from_item_id = ?,
    pinned_at = ?, folder_id = ?, archived_at = ?, archive_reason = ?, worktree_path = ?, worktree_base_head = ?, worktree_base_repo = ?,
    workspace_id = ?, source = ?, owner = ?, visibility = ?, parent_id = ?, context_source = ?, creation_request_id = ?,
	provider = ?, model = ?, variant = ?, effort = ?, speed = ?, permission_mode = ?, approve_for_me = ?, engine_id = ?, engine_ref = ?, instructions = ?, tool_policy_json = ?
WHERE id = ?`,
		timeText(sess.CreatedAt), timeText(sess.UpdatedAt), sess.Title, sess.Summary, sess.Entries, normalizeCWD(sess.CWD),
		sess.ForkedFromID, sess.ForkedFromTurnID, sess.ForkedFromItemID,
		nullableTimeText(sess.PinnedAt), strings.TrimSpace(sess.FolderID), nullableTimeText(sess.ArchivedAt), sess.ArchiveReason,
		normalizeCWD(sess.WorktreePath), sess.WorktreeBaseHEAD, normalizeCWD(sess.WorktreeBaseRepo),
		strings.TrimSpace(sess.WorkspaceID), strings.TrimSpace(sess.Source),
		strings.TrimSpace(sess.Owner), strings.TrimSpace(sess.Visibility), strings.TrimSpace(sess.ParentID), strings.TrimSpace(sess.ContextSource), strings.TrimSpace(sess.CreationRequestID),
		strings.TrimSpace(sess.Provider), strings.TrimSpace(sess.Model), strings.TrimSpace(sess.Variant),
		strings.TrimSpace(sess.Effort), strings.TrimSpace(sess.Speed), strings.TrimSpace(sess.PermissionMode),
		boolToInt(sess.ApproveForMe),
		strings.TrimSpace(sess.EngineID),
		strings.TrimSpace(sess.EngineRef),
		sess.Instructions,
		strings.TrimSpace(sess.ToolPolicyJSON),
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func sessionArgs(sess Session) []any {
	return []any{
		sess.ID,
		timeText(sess.CreatedAt),
		timeText(sess.UpdatedAt),
		sess.Title,
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
		strings.TrimSpace(sess.Effort), strings.TrimSpace(sess.Speed),
		strings.TrimSpace(sess.PermissionMode),
		boolToInt(sess.ApproveForMe),
		strings.TrimSpace(sess.EngineID),
		strings.TrimSpace(sess.EngineRef),
		sess.Instructions,
		strings.TrimSpace(sess.ToolPolicyJSON),
	}
}

func findSessionDB(db *sql.DB, id string) (Session, bool, error) {
	row := db.QueryRow(`
SELECT id, created_at, updated_at, title, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, permission_mode, approve_for_me, engine_id, engine_ref, instructions, tool_policy_json,
	       COALESCE((SELECT m.client_id FROM session_messages m
	                 WHERE m.session_id = sessions.id AND m.role = 'meta'
	                   AND m.content = 'turn_terminal' AND m.client_id <> ''
	                 ORDER BY m.seq DESC LIMIT 1), '')
FROM sessions
WHERE id = ?`, id)
	return scanSessionRow(row)
}

func findSessionTx(tx *sql.Tx, id string) (Session, bool, error) {
	row := tx.QueryRow(`
SELECT id, created_at, updated_at, title, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, permission_mode, approve_for_me, engine_id, engine_ref, instructions, tool_policy_json,
	       COALESCE((SELECT m.client_id FROM session_messages m
	                 WHERE m.session_id = sessions.id AND m.role = 'meta'
	                   AND m.content = 'turn_terminal' AND m.client_id <> ''
	                 ORDER BY m.seq DESC LIMIT 1), '')
FROM sessions
WHERE id = ?`, id)
	return scanSessionRow(row)
}

func scanSessionRow(scanner interface {
	Scan(dest ...any) error
}) (Session, bool, error) {
	s, err := scanSession(scanner)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return s, true, nil
}

func scanSession(scanner interface {
	Scan(dest ...any) error
}) (Session, error) {
	var s Session
	var createdAt, updatedAt string
	var pinnedAt, archivedAt sql.NullString
	if err := scanner.Scan(
		&s.ID, &createdAt, &updatedAt, &s.Title, &s.Summary, &s.Entries, &s.CWD,
		&s.ForkedFromID, &s.ForkedFromTurnID, &s.ForkedFromItemID,
		&pinnedAt, &s.FolderID, &archivedAt, &s.ArchiveReason,
		&s.WorktreePath, &s.WorktreeBaseHEAD, &s.WorktreeBaseRepo,
		&s.WorkspaceID, &s.Source, &s.Owner, &s.Visibility, &s.ParentID, &s.ContextSource, &s.CreationRequestID,
		&s.Provider, &s.Model, &s.Variant, &s.Effort, &s.Speed, &s.PermissionMode, &s.ApproveForMe, &s.EngineID, &s.EngineRef, &s.Instructions, &s.ToolPolicyJSON,
		&s.LatestCompletedTurnID,
	); err != nil {
		return Session{}, err
	}
	s.CreatedAt = parseTime(createdAt)
	s.UpdatedAt = parseTime(updatedAt)
	if pinnedAt.Valid {
		if t := parseTime(pinnedAt.String); !t.IsZero() {
			s.PinnedAt = &t
		}
	}
	if archivedAt.Valid {
		if t := parseTime(archivedAt.String); !t.IsZero() {
			s.ArchivedAt = &t
		}
	}
	return s, nil
}

func sessionExistsDB(db *sql.DB, id string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, strings.TrimSpace(id)).Scan(&count); err != nil {
		return false, fmt.Errorf("check session exists: %w", err)
	}
	return count > 0, nil
}

func sessionExistsTx(tx *sql.Tx, id string) (bool, error) {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, strings.TrimSpace(id)).Scan(&count); err != nil {
		return false, fmt.Errorf("check session exists: %w", err)
	}
	return count > 0, nil
}

// appendHistoryRecordTx inserts a record with the next per-session seq and
// returns that seq. seq is the message's stable address within the thread
// The returned seq is the stable address of the appended record.
func appendHistoryRecordTx(tx *sql.Tx, id string, rec HistoryRecord) (int, error) {
	var nextSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM session_messages WHERE session_id = ?`, id).Scan(&nextSeq); err != nil {
		return 0, fmt.Errorf("next history sequence: %w", err)
	}
	if err := insertHistoryRecordTx(tx, id, nextSeq, rec); err != nil {
		return 0, err
	}
	return nextSeq, nil
}

func insertHistoryRecordTx(tx *sql.Tx, id string, seq int, rec HistoryRecord) error {
	storedRec := compactHistoryRecordForStorage(rec)
	_, err := tx.Exec(`
			INSERT INTO session_messages (
				session_id, seq, role, content, display_content, origin, origin_id, cause, presentation_kind, related_session_id, read_only, phase, provider_item_id, provider_item_model, client_id, hidden, steered, reasoning_content,
				reasoning_blocks_json, content_parts_json, images_json, files_json, tool_calls_json, discovered_tools_json,
				tool_call_id, tool_invocation_id, tool_result_kind, tool_result_json, finish_reason, stop_reason, truncated, name, at, input_tokens, output_tokens, context_tokens, cache_creation_tokens, cache_read_tokens, retry_count, max_retries,
				provider, model
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				?, ?, ?, ?, ?, ?,
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			)`,
		id, seq, strings.ToLower(strings.TrimSpace(storedRec.Role)), storedRec.Content, storedRec.DisplayContent,
		strings.TrimSpace(storedRec.Origin), strings.TrimSpace(storedRec.OriginID), strings.TrimSpace(storedRec.Cause), strings.TrimSpace(storedRec.PresentationKind), strings.TrimSpace(storedRec.RelatedSessionID), boolInt(storedRec.ReadOnly),
		strings.TrimSpace(storedRec.Phase), strings.TrimSpace(storedRec.ProviderItemID), strings.TrimSpace(storedRec.ProviderItemModel), storedRec.ClientID, boolInt(storedRec.Hidden), boolInt(storedRec.Steered), storedRec.ReasoningContent,
		rawJSONText(storedRec.ReasoningBlocks), rawJSONText(storedRec.ContentParts), rawJSONText(storedRec.Images), rawJSONText(storedRec.Files), rawJSONText(storedRec.ToolCalls), rawJSONText(storedRec.DiscoveredTools),
		storedRec.ToolCallID, storedRec.ToolInvocationID, storedRec.ToolResultKind, rawJSONText(storedRec.ToolResult), storedRec.FinishReason, storedRec.StopReason, boolInt(storedRec.Truncated), storedRec.Name, nullableValueTimeText(storedRec.At), storedRec.InputTokens, storedRec.OutputTokens, storedRec.ContextTokens, storedRec.CacheCreationTokens, storedRec.CacheReadTokens, storedRec.RetryCount, storedRec.MaxRetries,
		strings.TrimSpace(storedRec.Provider), strings.TrimSpace(storedRec.Model),
	)
	if err != nil {
		return fmt.Errorf("insert history record: %w", err)
	}
	if err := projectToolInvocationTx(tx, id, storedRec.ToolInvocationID); err != nil {
		return err
	}
	return nil
}

func projectToolInvocationTx(tx *sql.Tx, ownerID, invocationID string) error {
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return nil
	}
	now := time.Now().UTC().UnixMilli()
	result, err := tx.Exec(`
UPDATE tool_invocations SET projected_at = MAX(projected_at, ?), result_json = ''
WHERE id = ? AND state IN ('succeeded', 'failed')
  AND batch_id IN (SELECT id FROM tool_batches WHERE owner_id = ?)`, now, invocationID, ownerID)
	if err != nil {
		return fmt.Errorf("project tool invocation: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("tool invocation %q is not settled for owner %q", invocationID, ownerID)
	}
	_, err = tx.Exec(`
UPDATE tool_batches SET status = 'projected', updated_at = ?, terminal_at = MAX(terminal_at, ?)
WHERE owner_id = ? AND status = 'settled'
  AND id = (SELECT batch_id FROM tool_invocations WHERE id = ?)
  AND NOT EXISTS (
    SELECT 1 FROM tool_invocations
    WHERE batch_id = tool_batches.id AND state IN ('succeeded', 'failed') AND projected_at = 0
  )`, now, now, ownerID, invocationID)
	if err != nil {
		return fmt.Errorf("project tool batch: %w", err)
	}
	return nil
}

const historyRecordsSelect = `
	SELECT seq, role, content, display_content, origin, origin_id, cause, presentation_kind, related_session_id, read_only, phase, client_id, hidden, steered, reasoning_content,
	       provider_item_id, provider_item_model,
	       reasoning_blocks_json, content_parts_json, images_json, files_json, tool_calls_json, discovered_tools_json,
	       tool_call_id, tool_invocation_id, tool_result_kind, tool_result_json, finish_reason, stop_reason, truncated, name, at, input_tokens, output_tokens, context_tokens, cache_creation_tokens, cache_read_tokens, retry_count, max_retries,
	       provider, model
	FROM session_messages`

func loadHistoryRecordsDB(db *sql.DB, id string, includeMeta, activeOnly bool) ([]HistoryRecord, error) {
	query := historyRecordsSelect + ` WHERE session_id = ?`
	if activeOnly {
		query += ` AND NOT EXISTS (
			SELECT 1 FROM session_history_retractions r
			WHERE r.session_id = session_messages.session_id
			AND session_messages.seq BETWEEN r.from_seq AND r.through_seq
		)`
	}
	args := []any{id}
	if !includeMeta {
		query += ` AND lower(role) <> 'meta'`
	}
	query += ` ORDER BY seq ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("load session history: %w", err)
	}
	return scanHistoryRecords(rows)
}

func scanHistoryRecords(rows *sql.Rows) ([]HistoryRecord, error) {
	defer rows.Close()

	var records []HistoryRecord
	for rows.Next() {
		var rec HistoryRecord
		var hidden, steered, truncated, readOnly int
		var reasoningBlocks, contentParts, images, files, toolCalls, discoveredTools, toolResult string
		var at sql.NullString
		if err := rows.Scan(
			&rec.Seq,
			&rec.Role, &rec.Content, &rec.DisplayContent, &rec.Origin, &rec.OriginID, &rec.Cause, &rec.PresentationKind, &rec.RelatedSessionID, &readOnly, &rec.Phase, &rec.ClientID, &hidden, &steered, &rec.ReasoningContent,
			&rec.ProviderItemID, &rec.ProviderItemModel,
			&reasoningBlocks, &contentParts, &images, &files, &toolCalls, &discoveredTools,
			&rec.ToolCallID, &rec.ToolInvocationID, &rec.ToolResultKind, &toolResult, &rec.FinishReason, &rec.StopReason, &truncated, &rec.Name, &at, &rec.InputTokens, &rec.OutputTokens, &rec.ContextTokens, &rec.CacheCreationTokens, &rec.CacheReadTokens, &rec.RetryCount, &rec.MaxRetries,
			&rec.Provider, &rec.Model,
		); err != nil {
			return nil, fmt.Errorf("scan session history: %w", err)
		}
		rec.Hidden = hidden != 0
		rec.ReadOnly = readOnly != 0
		rec.Steered = steered != 0
		rec.Truncated = truncated != 0
		rec.ReasoningBlocks = rawMessage(reasoningBlocks)
		rec.ContentParts = rawMessage(contentParts)
		rec.Images = rawMessage(images)
		rec.Files = rawMessage(files)
		rec.ToolCalls = rawMessage(toolCalls)
		rec.DiscoveredTools = rawMessage(discoveredTools)
		rec.ToolResult = rawMessage(toolResult)
		hydrateHistoryRecordFromStorage(&rec)
		if at.Valid {
			rec.At = parseTime(at.String)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan session history: %w", err)
	}
	return records, nil
}

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
