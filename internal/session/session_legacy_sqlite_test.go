// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"sync"

	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"os"

	"strings"

	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// Create initializes a new session.
// If id is non-empty, it is used as the session ID; otherwise a new one is generated.
var (
	storeInitMu  sync.Mutex
	storeWriteMu sync.Mutex
)

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

// CreateWithWorktree initializes a session bound to an isolated git worktree.
// Fork metadata is empty for a conversation that starts in a new worktree.
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
	return createInitialized(sessDir, sess, records, seed, launch, nil)
}

// CreateInitializedWithInbox commits the session, its initial execution fence,
// and first dispatch together, so recovery cannot lose the initial brief.
func CreateInitializedWithInbox(sessDir string, sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord, input InboxMessage) (*Session, error) {
	return createInitialized(sessDir, sess, records, seed, launch, &input)
}

func createInitialized(sessDir string, sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord, input *InboxMessage) (*Session, error) {
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
			if existing.Revision != launch.Revision || !existing.Runtime.equal(launch.Runtime) || existing.SourceSession != launch.SourceSession || existing.SourceCutoff != launch.SourceCutoff {
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
	if sess.ForkedFromID != "" {

		if _, err := tx.Exec(`INSERT INTO session_driver_bindings
(session_id, profile, provider_id, contract_version, driver_id, driver_version)
SELECT ?, profile, provider_id, contract_version, driver_id, driver_version
FROM session_driver_bindings WHERE session_id = ?`, sess.ID, sess.ForkedFromID); err != nil {
			return nil, fmt.Errorf("inherit fork driver binding: %w", err)
		}
	}
	if input != nil {
		control := Control{SessionID: sess.ID, ManagerID: sess.ParentID, Revision: 1, State: ControlActive}
		if control.ManagerID == "" {
			return nil, errors.New("initial dispatch needs a manager")
		}
		if _, err := tx.Exec(`INSERT INTO session_controls(session_id,manager_id,revision,state) VALUES(?,?,?,?)`, control.SessionID, control.ManagerID, control.Revision, control.State); err != nil {
			return nil, err
		}
		message := *input
		message.SessionID = sess.ID
		message.Controls = append(append([]Control(nil), message.Controls...), control)
		if err := insertInbox(tx, message); err != nil {
			return nil, err
		}
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
	return listSessions(sessDir, "", nil, limit)
}

func listSessions(sessDir, where string, args []any, limit int) ([]Session, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `
SELECT id, created_at, updated_at, title, title_revision, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, model_options_json, permission_mode, approve_for_me, engine_id, engine_ref, instructions, project_role, tool_policy_json,
	       COALESCE((SELECT m.client_id FROM session_messages m
	                 WHERE m.session_id = sessions.id AND m.role = 'meta'
	                   AND m.content = 'turn_terminal' AND m.client_id <> ''
	                 ORDER BY m.seq DESC LIMIT 1), '')
FROM sessions`
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	if where != "" {

		sessions = make([]Session, 0)
	}
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

	if wsID == "" {
		return listSessions(sessDir, "cwd = ? OR worktree_base_repo = ?", []any{target, target}, limit)
	}
	if target == "" {
		return listSessions(sessDir, "workspace_id = ?", []any{wsID}, limit)
	}
	return listSessions(sessDir,
		"workspace_id = ? OR (cwd = ? AND workspace_id = '') OR (worktree_base_repo = ? AND workspace_id = '')",
		[]any{wsID, target, target}, limit)
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
SELECT id, created_at, updated_at, title, title_revision, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
       provider, model, variant, effort, speed, model_options_json, permission_mode, approve_for_me, engine_id, engine_ref, instructions, project_role, tool_policy_json,
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

// SetProjectMembership moves an ordinary session into a project, or a
// managed session back out: source and parentID carry the relation, and
// instructions replace the session's create-time instructions.
func SetProjectMembership(sessDir, id, source, parentID, instructions string) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Source = strings.TrimSpace(source)
		s.ParentID = strings.TrimSpace(parentID)
		s.Instructions = instructions
		if source == "" {
			s.ProjectRole = ""
		}
	})
}

func SetSource(sessDir, id, source string) (Session, error) {
	return updateMetadata(sessDir, id, false, func(s *Session) {
		s.Source = strings.TrimSpace(source)
	})
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
		s.ModelOptions = selection.ModelOptions
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

// Delete removes a session and its durable history records.
func Delete(sessDir, id string) (Session, error) {
	return deleteSession(sessDir, id, false)
}

// DeleteArchived removes a session and its durable history only if it remains
// archived in the deletion transaction. A restored session is left unchanged.
func DeleteArchived(sessDir, id string) (Session, error) {
	return deleteSession(sessDir, id, true)
}

func deleteSession(sessDir, id string, onlyIfArchived bool) (Session, error) {
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

	if onlyIfArchived && deleted.ArchivedAt == nil {
		return Session{}, fmt.Errorf("%w: %q", ErrSessionNotArchived, id)
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
	previousTitle := s.Title
	update(&s)
	if s.Title != previousTitle {
		s.TitleRevision++
	}
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
	return appendFencedHistoryRecord(sessDir, id, rec, control, nil, nil)
}

// AppendContinuationHistoryRecord checks a Run's original control baseline in
// the history transaction, including adoption or stop during admission hooks.
func AppendContinuationHistoryRecord(sessDir, id string, rec HistoryRecord, control *Control, baseline Control) (int, error) {
	return appendFencedHistoryRecord(sessDir, id, rec, control, &baseline, nil)
}

// AppendAcceptedUserHistoryRecord fences concurrent edits and cancellation in
// the same transaction that admits the input to conversation history.
func AppendAcceptedUserHistoryRecord(sessDir, id string, rec HistoryRecord, accepted HeldUserWork) (int, error) {
	rec.ExpectedHeldMessage = accepted.MessageJSON
	return appendFencedHistoryRecord(sessDir, id, rec, nil, nil, &accepted)
}

func appendFencedHistoryRecord(sessDir, id string, rec HistoryRecord, control, baseline *Control, accepted *HeldUserWork) (int, error) {
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
	if baseline != nil {
		if baseline.SessionID != id {
			return 0, ErrControlChanged
		}
		current := Control{SessionID: id}
		err := tx.QueryRow(`SELECT manager_id,revision,state FROM session_controls WHERE session_id=?`, id).Scan(&current.ManagerID, &current.Revision, &current.State)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err := validateContinuationBaseline(current, *baseline); err != nil {
			return 0, err
		}
	}
	if rec.Role == "user" && rec.ClientID != "" {
		if err := validateInboxControls(tx, rec.ClientID); err != nil {
			return 0, err
		}
	}
	if accepted != nil {
		var state, messageJSON, runtimeJSON, origin string
		err := tx.QueryRow(`SELECT state, origin, message_json, runtime_json FROM held_user_work WHERE session_id = ? AND id = ?`, id, rec.ClientID).Scan(&state, &origin, &messageJSON, &runtimeJSON)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "pending") {
			return 0, ErrHeldUserWorkSettled
		}
		if err != nil {
			return 0, err
		}
		if origin != accepted.Origin || messageJSON != string(accepted.MessageJSON) || runtimeJSON != string(accepted.RuntimeJSON) {
			return 0, errors.New("accepted input changed before admission; review the held message")
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
	migration := schemaMigration{db: db, columns: make(map[string]map[string]bool)}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			title_revision INTEGER NOT NULL DEFAULT 0,
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
			request_context_json TEXT NOT NULL DEFAULT '',
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
				engine_turn_id TEXT NOT NULL DEFAULT '',
				engine_turn_pending INTEGER NOT NULL DEFAULT 0,
				provider_item_model TEXT NOT NULL DEFAULT '',
				client_id TEXT NOT NULL DEFAULT '',
				hidden INTEGER NOT NULL DEFAULT 0,
				steered INTEGER NOT NULL DEFAULT 0,
				reasoning_content TEXT NOT NULL DEFAULT '',
			reasoning_blocks_json TEXT NOT NULL DEFAULT '',
			provider_items_json TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS project_work (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL, version INTEGER NOT NULL,
            revision INTEGER NOT NULL, phase TEXT NOT NULL, created_at TEXT NOT NULL, payload TEXT NOT NULL,
            FOREIGN KEY(project_id) REFERENCES sessions(id) ON DELETE CASCADE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_project_work_project ON project_work(project_id)`,
		`CREATE TABLE IF NOT EXISTS project_work_operations (
            id TEXT PRIMARY KEY, work_id TEXT NOT NULL, payload TEXT NOT NULL,
            FOREIGN KEY(work_id) REFERENCES project_work(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS session_inbox (
			client_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			related_session_id TEXT NOT NULL DEFAULT '',
			cause TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			wake INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			delivered_at TEXT,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_inbox_pending ON session_inbox(session_id, delivered_at, created_at)`,
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
	if err := migration.addColumnIfMissing("sessions", "title_revision", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("held_user_work", "state", "TEXT NOT NULL DEFAULT 'pending'"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("inference_operations", "workflow_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("inference_operations", "parent_operation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("inference_operations", "attempt_limit", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("inference_operations", "failure_message", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("inference_attempts", "failure_message", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

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
	if err := migration.addColumnIfMissing("tool_invocations", "parent_invocation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "phase", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "display_content", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "origin", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "origin_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "cause", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "presentation_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "related_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "read_only", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "engine_turn_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "engine_turn_pending", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "provider_item_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "provider_item_model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "hidden", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "request_context_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "provider_items_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "content_parts_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "cache_creation_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "cache_read_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "context_tokens", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "retry_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "max_retries", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "tool_result_kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "tool_invocation_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "tool_result_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "finish_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "stop_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "truncated", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "discovered_tools_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_messages", "model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "worktree_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "archive_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "worktree_base_head", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "worktree_base_repo", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "workspace_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []string{"owner", "visibility", "parent_id", "context_source", "creation_request_id"} {
		if err := migration.addColumnIfMissing("sessions", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_managed_request ON sessions(owner, creation_request_id) WHERE owner <> '' AND creation_request_id <> ''`); err != nil {
		return fmt.Errorf("migrate managed session request index: %w", err)
	}
	if err := migration.addColumnIfMissing("sessions", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "model", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "variant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "model_options_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "speed", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "effort", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "permission_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "approve_for_me", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "engine_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "engine_ref", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "instructions", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "tool_policy_json", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "folder_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	if _, err := db.Exec(`DROP TABLE IF EXISTS pin_groups`); err != nil {
		return fmt.Errorf("drop legacy pin groups: %w", err)
	}
	if err := dropColumnIfPresent(db, "sessions", "pin_group_id"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id)`); err != nil {
		return fmt.Errorf("migrate sessions database: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_cwd_workspace ON sessions(cwd, workspace_id)`); err != nil {
		return fmt.Errorf("migrate sessions database: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_worktree_workspace ON sessions(worktree_base_repo, workspace_id)`); err != nil {
		return fmt.Errorf("migrate sessions database: %w", err)
	}
	if err := migration.addColumnIfMissing("inference_journal_runtimes", "pid", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "seed_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}

	if _, err := db.Exec(`DELETE FROM sessions WHERE source LIKE 'named-agent:%'`); err != nil {
		return fmt.Errorf("remove retired collaboration sessions: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM session_controls WHERE manager_id GLOB 'agent-[0-9a-f]*'`); err != nil {
		return fmt.Errorf("remove retired collaboration session controls: %w", err)
	}

	if _, err := db.Exec(`DROP TABLE IF EXISTS session_candidates`); err != nil {
		return fmt.Errorf("drop retired project candidates: %w", err)
	}
	if err := migration.addColumnIfMissing("session_inbox", "cause", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_inbox", "wake", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("sessions", "project_role", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_project_side ON sessions(parent_id) WHERE source='project-session' AND project_role='side' AND archived_at IS NULL`); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("project_work", "reconciled_version", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	if err := migration.addColumnIfMissing("session_inbox", "work_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migration.addColumnIfMissing("session_inbox", "work_version", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	if err := migration.addColumnIfMissing("session_inbox", "work_revision", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	if err := migration.addColumnIfMissing("session_inbox", "controls_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	return migrateDriverBindings(db)
}

// Schema inspection is shared only within one migration. Each store open reads
// the schema again, including after an external restore or another process's
// migration, instead of retaining assumptions about a database path.
type schemaMigration struct {
	db      *sql.DB
	columns map[string]map[string]bool
}

func (m *schemaMigration) tableColumns(table string) (map[string]bool, error) {
	if columns, ok := m.columns[table]; ok {
		return columns, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("scan %s columns: %w", table, err)
		}
		columns[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan %s columns: %w", table, err)
	}
	m.columns[table] = columns
	return columns, nil
}

func (m *schemaMigration) addColumnIfMissing(table, column, definition string) error {
	columns, err := m.tableColumns(table)
	if err != nil {
		return err
	}
	key := strings.ToLower(column)
	if columns[key] {
		return nil
	}
	if _, err := m.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {

		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add %s.%s column: %w", table, column, err)
		}
	}
	columns[key] = true
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

func updateSessionTx(tx *sql.Tx, sess Session) error {
	_, err := tx.Exec(`
UPDATE sessions
SET created_at = ?, updated_at = ?, title = ?, title_revision = ?, summary = ?, entries = ?, cwd = ?,
    forked_from_id = ?, forked_from_turn_id = ?, forked_from_item_id = ?,
    pinned_at = ?, folder_id = ?, archived_at = ?, archive_reason = ?, worktree_path = ?, worktree_base_head = ?, worktree_base_repo = ?,
    workspace_id = ?, source = ?, owner = ?, visibility = ?, parent_id = ?, context_source = ?, creation_request_id = ?,
	provider = ?, model = ?, variant = ?, effort = ?, speed = ?, model_options_json = ?, permission_mode = ?, approve_for_me = ?, engine_id = ?, engine_ref = ?, instructions = ?, project_role = ?, tool_policy_json = ?
WHERE id = ?`,
		timeText(sess.CreatedAt), timeText(sess.UpdatedAt), sess.Title, sess.TitleRevision, sess.Summary, sess.Entries, normalizeCWD(sess.CWD),
		sess.ForkedFromID, sess.ForkedFromTurnID, sess.ForkedFromItemID,
		nullableTimeText(sess.PinnedAt), strings.TrimSpace(sess.FolderID), nullableTimeText(sess.ArchivedAt), sess.ArchiveReason,
		normalizeCWD(sess.WorktreePath), sess.WorktreeBaseHEAD, normalizeCWD(sess.WorktreeBaseRepo),
		strings.TrimSpace(sess.WorkspaceID), strings.TrimSpace(sess.Source),
		strings.TrimSpace(sess.Owner), strings.TrimSpace(sess.Visibility), strings.TrimSpace(sess.ParentID), strings.TrimSpace(sess.ContextSource), strings.TrimSpace(sess.CreationRequestID),
		strings.TrimSpace(sess.Provider), strings.TrimSpace(sess.Model), strings.TrimSpace(sess.Variant),
		strings.TrimSpace(sess.Effort), strings.TrimSpace(sess.Speed), modelOptionsJSON(sess.ModelOptions), strings.TrimSpace(sess.PermissionMode),
		boolToInt(sess.ApproveForMe),
		strings.TrimSpace(sess.EngineID),
		strings.TrimSpace(sess.EngineRef),
		sess.Instructions,
		sess.ProjectRole,
		strings.TrimSpace(sess.ToolPolicyJSON),
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

func findSessionDB(db *sql.DB, id string) (Session, bool, error) {
	row := db.QueryRow(`
SELECT id, created_at, updated_at, title, title_revision, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, model_options_json, permission_mode, approve_for_me, engine_id, engine_ref, instructions, project_role, tool_policy_json,
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
SELECT id, created_at, updated_at, title, title_revision, summary, entries, cwd,
       forked_from_id, forked_from_turn_id, forked_from_item_id,
       pinned_at, folder_id, archived_at, archive_reason,
       worktree_path, worktree_base_head, worktree_base_repo,
       workspace_id, source, owner, visibility, parent_id, context_source, creation_request_id,
	       provider, model, variant, effort, speed, model_options_json, permission_mode, approve_for_me, engine_id, engine_ref, instructions, project_role, tool_policy_json,
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
	var createdAt, updatedAt, modelOptions string
	var pinnedAt, archivedAt sql.NullString
	if err := scanner.Scan(
		&s.ID, &createdAt, &updatedAt, &s.Title, &s.TitleRevision, &s.Summary, &s.Entries, &s.CWD,
		&s.ForkedFromID, &s.ForkedFromTurnID, &s.ForkedFromItemID,
		&pinnedAt, &s.FolderID, &archivedAt, &s.ArchiveReason,
		&s.WorktreePath, &s.WorktreeBaseHEAD, &s.WorktreeBaseRepo,
		&s.WorkspaceID, &s.Source, &s.Owner, &s.Visibility, &s.ParentID, &s.ContextSource, &s.CreationRequestID,
		&s.Provider, &s.Model, &s.Variant, &s.Effort, &s.Speed, &modelOptions, &s.PermissionMode, &s.ApproveForMe, &s.EngineID, &s.EngineRef, &s.Instructions, &s.ProjectRole, &s.ToolPolicyJSON,
		&s.LatestCompletedTurnID,
	); err != nil {
		return Session{}, err
	}
	if err := json.Unmarshal([]byte(modelOptions), &s.ModelOptions); err != nil {
		return Session{}, fmt.Errorf("decode model options: %w", err)
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
	if rec.Role == "user" && rec.ClientID != "" {
		if err := consumeHeldUserWorkTx(tx, id, rec); err != nil {
			return 0, err
		}
	}
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
				session_id, seq, role, content, display_content, origin, origin_id, cause, presentation_kind, related_session_id, read_only, phase, provider_item_id, engine_turn_id, engine_turn_pending, provider_item_model, client_id, hidden, steered, reasoning_content,
				reasoning_blocks_json, provider_items_json, content_parts_json, images_json, files_json, tool_calls_json, discovered_tools_json,
				tool_call_id, tool_invocation_id, tool_result_kind, tool_result_json, finish_reason, stop_reason, truncated, name, at, input_tokens, output_tokens, context_tokens, cache_creation_tokens, cache_read_tokens, retry_count, max_retries,
				provider, model
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				?, ?, ?, ?, ?, ?, ?,
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			)`,
		id, seq, strings.ToLower(strings.TrimSpace(storedRec.Role)), storedRec.Content, storedRec.DisplayContent,
		strings.TrimSpace(storedRec.Origin), strings.TrimSpace(storedRec.OriginID), strings.TrimSpace(storedRec.Cause), strings.TrimSpace(storedRec.PresentationKind), strings.TrimSpace(storedRec.RelatedSessionID), boolInt(storedRec.ReadOnly),
		strings.TrimSpace(storedRec.Phase), strings.TrimSpace(storedRec.ProviderItemID), strings.TrimSpace(storedRec.EngineTurnID), boolInt(storedRec.EngineTurnPending), strings.TrimSpace(storedRec.ProviderItemModel), storedRec.ClientID, boolInt(storedRec.Hidden), boolInt(storedRec.Steered), storedRec.ReasoningContent,
		rawJSONText(storedRec.ReasoningBlocks), rawJSONText(storedRec.ProviderItems), rawJSONText(storedRec.ContentParts), rawJSONText(storedRec.Images), rawJSONText(storedRec.Files), rawJSONText(storedRec.ToolCalls), rawJSONText(storedRec.DiscoveredTools),
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
		var hidden, steered, truncated, readOnly, engineTurnPending int
		var reasoningBlocks, providerItems, contentParts, images, files, toolCalls, discoveredTools, toolResult string
		var at sql.NullString
		if err := rows.Scan(
			&rec.Seq,
			&rec.Role, &rec.Content, &rec.DisplayContent, &rec.Origin, &rec.OriginID, &rec.Cause, &rec.PresentationKind, &rec.RelatedSessionID, &readOnly, &rec.Phase, &rec.ClientID, &hidden, &steered, &rec.ReasoningContent,
			&rec.ProviderItemID, &rec.EngineTurnID, &engineTurnPending, &rec.ProviderItemModel,
			&reasoningBlocks, &providerItems, &contentParts, &images, &files, &toolCalls, &discoveredTools,
			&rec.ToolCallID, &rec.ToolInvocationID, &rec.ToolResultKind, &toolResult, &rec.FinishReason, &rec.StopReason, &truncated, &rec.Name, &at, &rec.InputTokens, &rec.OutputTokens, &rec.ContextTokens, &rec.CacheCreationTokens, &rec.CacheReadTokens, &rec.RetryCount, &rec.MaxRetries,
			&rec.Provider, &rec.Model,
		); err != nil {
			return nil, fmt.Errorf("scan session history: %w", err)
		}
		rec.EngineTurnPending = engineTurnPending != 0
		rec.Hidden = hidden != 0
		rec.ReadOnly = readOnly != 0
		rec.Steered = steered != 0
		rec.Truncated = truncated != 0
		rec.ReasoningBlocks = rawMessage(reasoningBlocks)
		rec.ProviderItems = rawMessage(providerItems)
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
