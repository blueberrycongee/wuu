package channels

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	_ "modernc.org/sqlite"
)

const (
	databaseFileName      = "channels.sqlite3"
	agentTokenFile        = ".chat-token"
	agentMemoryIndexFile  = "MEMORY.md"
	namedAgentAvatarCount = 9
	maxAgentAvatarBytes   = 512 * 1024
	defaultAgentRunLimit  = 5
	defaultRoomRunLimit   = 12
	defaultGlobalRunLimit = 24
)

var (
	ErrNotFound     = errors.New("channels record not found")
	ErrConflict     = errors.New("channels record conflict")
	ErrUnauthorized = errors.New("channels authentication failed")
)

type Service struct {
	dir                  string
	db                   *sql.DB
	wake                 WakeSink
	now                  func() time.Time
	mu                   sync.Mutex
	bootstrapMu          sync.Mutex
	agentRunLimit        int
	roomRunLimit         int
	globalRunLimit       int
	roomInputTokenLimit  int64
	roomOutputTokenLimit int64
	stopOnce             sync.Once
	stopCh               chan struct{}
	doneCh               chan struct{}

	sessionControllerMu sync.RWMutex
	sessionController   SessionController

	telemetryMu sync.RWMutex
	telemetry   TelemetrySink
}

func Open(dir string, wake WakeSink) (*Service, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("channels state directory is required")
	}
	if err := securefs.Mkdir(dir); err != nil {
		return nil, fmt.Errorf("create channels state directory: %w", err)
	}
	dbPath := filepath.Join(dir, databaseFileName)
	if err := securefs.PreCreateFile(dbPath); err != nil {
		return nil, fmt.Errorf("precreate channels database: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open channels database: %w", err)
	}
	db.SetMaxOpenConns(1)
	service := &Service{
		dir:            dir,
		db:             db,
		wake:           wake,
		now:            func() time.Time { return time.Now().UTC() },
		agentRunLimit:  defaultAgentRunLimit,
		roomRunLimit:   defaultRoomRunLimit,
		globalRunLimit: defaultGlobalRunLimit,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
	service.agentRunLimit = positiveEnvInt("WUU_COLLAB_AGENT_RUN_LIMIT", service.agentRunLimit)
	service.roomRunLimit = positiveEnvInt("WUU_COLLAB_ROOM_RUN_LIMIT", service.roomRunLimit)
	service.globalRunLimit = positiveEnvInt("WUU_COLLAB_GLOBAL_RUN_LIMIT", service.globalRunLimit)
	service.roomInputTokenLimit = positiveEnvInt64("WUU_COLLAB_ROOM_INPUT_TOKEN_LIMIT", 0)
	service.roomOutputTokenLimit = positiveEnvInt64("WUU_COLLAB_ROOM_OUTPUT_TOKEN_LIMIT", 0)
	if err := service.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := service.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := service.ensureRoomRuntimes(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := service.ensureNamedAgentMemoryIndexes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	go service.runDeadlineLoop()
	return service, nil
}

func positiveEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveEnvInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (s *Service) ensureNamedAgentMemoryIndexes() error {
	rows, err := s.db.Query(`SELECT memory_dir FROM named_agents`)
	if err != nil {
		return fmt.Errorf("list named agent memory directories: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var memoryDir string
		if err := rows.Scan(&memoryDir); err != nil {
			return fmt.Errorf("scan named agent memory directory: %w", err)
		}
		if err := securefs.PreCreateFile(filepath.Join(memoryDir, agentMemoryIndexFile)); err != nil {
			return fmt.Errorf("initialize named agent memory index: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate named agent memory directories: %w", err)
	}
	return nil
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.doneCh
	return s.db.Close()
}

func (s *Service) runDeadlineLoop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = s.ExpireWorkRuns(context.Background())
		case <-s.stopCh:
			return
		}
	}
}

func (s *Service) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// SetCollaborationRunLimits configures Host admission ceilings. Non-positive
// values preserve the current limit.
func (s *Service) SetCollaborationRunLimits(agent, room, global int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if agent > 0 {
		s.agentRunLimit = agent
	}
	if room > 0 {
		s.roomRunLimit = room
	}
	if global > 0 {
		s.globalRunLimit = global
	}
}

func (s *Service) SetWakeSink(wake WakeSink) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.wake = wake
	s.mu.Unlock()
}

func (s *Service) SetTelemetrySink(sink TelemetrySink) {
	if s == nil {
		return
	}
	s.telemetryMu.Lock()
	s.telemetry = sink
	s.telemetryMu.Unlock()
}

func (s *Service) emitTelemetry(event TelemetryEvent) {
	if s == nil {
		return
	}
	s.telemetryMu.RLock()
	sink := s.telemetry
	s.telemetryMu.RUnlock()
	if sink != nil {
		sink.RecordChannelEvent(event)
	}
}

func (s *Service) configure() error {
	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure channels database: %w", err)
		}
	}
	return nil
}

func (s *Service) migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS collaboration_principals (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('named_agent', 'room_runtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS named_agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL COLLATE NOCASE,
			role TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'named' CHECK (kind IN ('named', 'room')),
			room_id TEXT,
			memory_dir TEXT NOT NULL,
			avatar_key TEXT NOT NULL DEFAULT '',
			avatar_image TEXT NOT NULL DEFAULT '',
			engine_override TEXT,
			provider_override TEXT,
			model_override TEXT,
			effort_override TEXT,
			token_hash TEXT NOT NULL,
			autostart INTEGER NOT NULL DEFAULT 0 CHECK (autostart IN (0, 1)),
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rooms (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('channel', 'dm')),
			name TEXT NOT NULL,
			avatar_image TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL,
			membership_revision INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS room_runtimes (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL UNIQUE,
			memory_dir TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			autostart INTEGER NOT NULL DEFAULT 1 CHECK (autostart IN (0, 1)),
			created_at INTEGER NOT NULL,
			FOREIGN KEY (id) REFERENCES collaboration_principals(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS room_members (
			room_id TEXT NOT NULL,
			member_type TEXT NOT NULL CHECK (member_type IN ('human', 'agent')),
			member_id TEXT NOT NULL,
			joined_at INTEGER NOT NULL,
			PRIMARY KEY (room_id, member_type, member_id),
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_room_members_member ON room_members(member_type, member_id, room_id)`,
		`CREATE TABLE IF NOT EXISTS direct_messages (
			human_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			room_id TEXT NOT NULL UNIQUE,
			PRIMARY KEY (human_id, agent_id),
			FOREIGN KEY (agent_id) REFERENCES named_agents(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS room_messages (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			thread_id TEXT,
			author_type TEXT NOT NULL CHECK (author_type IN ('human', 'agent')),
			author_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('text', 'task', 'system')),
			body TEXT NOT NULL,
			images_json TEXT NOT NULL DEFAULT '[]',
			files_json TEXT NOT NULL DEFAULT '[]',
			mentions_json TEXT NOT NULL DEFAULT '[]',
			reply_to TEXT,
			task_title TEXT,
			task_state TEXT,
			task_owner TEXT,
			task_verification_required INTEGER NOT NULL DEFAULT 0 CHECK (task_verification_required IN (0, 1)),
			task_goal_revision INTEGER NOT NULL DEFAULT 0,
			task_candidate_revision INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			UNIQUE (room_id, seq),
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (thread_id) REFERENCES room_messages(id),
			FOREIGN KEY (reply_to) REFERENCES room_messages(id),
			FOREIGN KEY (task_owner) REFERENCES named_agents(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_room_messages_room_seq ON room_messages(room_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_room_messages_thread_seq ON room_messages(room_id, thread_id, seq)`,
		`CREATE TABLE IF NOT EXISTS agent_creation_proposals (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL UNIQUE,
			room_id TEXT NOT NULL,
			name TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL CHECK (state IN ('pending', 'processing', 'approved', 'cancelled')),
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			created_agent_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			resolved_at INTEGER,
			FOREIGN KEY (message_id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_creation_proposals_room ON agent_creation_proposals(room_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS room_cursors (
			room_id TEXT NOT NULL,
			member_type TEXT NOT NULL DEFAULT 'agent' CHECK (member_type IN ('human', 'agent')),
			member_id TEXT NOT NULL,
			last_read_seq INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (room_id, member_type, member_id),
			FOREIGN KEY (room_id, member_type, member_id) REFERENCES room_members(room_id, member_type, member_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS inbox_items (
			id TEXT PRIMARY KEY,
			member_type TEXT NOT NULL DEFAULT 'agent' CHECK (member_type IN ('human', 'agent')),
			member_id TEXT NOT NULL,
			room_id TEXT,
			message_id TEXT,
			reminder_id TEXT,
			kind TEXT NOT NULL CHECK (kind IN ('mention', 'reply', 'thread_update', 'task', 'reminder')),
			created_at INTEGER NOT NULL,
			pulled_at INTEGER,
			FOREIGN KEY (room_id, member_type, member_id) REFERENCES room_members(room_id, member_type, member_id) ON DELETE CASCADE,
			FOREIGN KEY (message_id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (reminder_id) REFERENCES reminders(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS agent_wake_state (
			agent_id TEXT PRIMARY KEY,
			outstanding INTEGER NOT NULL DEFAULT 0 CHECK (outstanding IN (0, 1)),
			pending INTEGER NOT NULL DEFAULT 0 CHECK (pending IN (0, 1)),
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (agent_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS collaboration_messages (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			from_type TEXT NOT NULL CHECK (from_type IN ('human', 'agent')),
			from_id TEXT NOT NULL,
			from_session_ref TEXT,
			to_agent_id TEXT NOT NULL,
			target_session_ref TEXT,
			kind TEXT NOT NULL DEFAULT 'control' CHECK (kind IN ('control', 'assignment', 'peer_result', 'candidate_ready', 'verification_feedback', 'completion', 'work_run_terminal')),
			body TEXT NOT NULL,
			work_id TEXT,
			source_message_id TEXT,
			goal_revision INTEGER NOT NULL DEFAULT 0,
			candidate_revision INTEGER NOT NULL DEFAULT 0,
			artifact_refs_json TEXT NOT NULL DEFAULT '[]',
			reply_to TEXT,
			target_kind TEXT NOT NULL DEFAULT 'named_agent' CHECK (target_kind IN ('room', 'named_agent', 'session', 'room_runtime')),
			target_id TEXT NOT NULL DEFAULT '',
			visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('room', 'private', 'work_private', 'system')),
			correlation_id TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			terminal_state TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			pulled_at INTEGER,
			consumed_at INTEGER,
			invalidated_at INTEGER,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (to_agent_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE,
			FOREIGN KEY (work_id) REFERENCES works(id) ON DELETE CASCADE,
			FOREIGN KEY (source_message_id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (reply_to) REFERENCES collaboration_messages(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collaboration_inbox ON collaboration_messages(to_agent_id, pulled_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS task_verifications (
			task_id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			decision TEXT NOT NULL CHECK (decision IN ('pass', 'block', 'unknown')),
			report TEXT NOT NULL,
			evidence_refs_json TEXT NOT NULL DEFAULT '[]',
			run_ref TEXT,
			attempt INTEGER NOT NULL DEFAULT 1,
			goal_revision INTEGER NOT NULL DEFAULT 0,
			candidate_revision INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (task_id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (owner_id) REFERENCES named_agents(id)
		)`,
		`CREATE TABLE IF NOT EXISTS works (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			source_message_id TEXT NOT NULL,
			owner_named_agent_id TEXT NOT NULL,
			lead_named_agent_id TEXT,
			title TEXT NOT NULL,
			brief TEXT NOT NULL DEFAULT '',
			goal_revision INTEGER NOT NULL DEFAULT 1,
			candidate_revision INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL CHECK (state IN ('open', 'working', 'checking', 'revising', 'integrating', 'needs_human', 'completed', 'failed', 'cancelled', 'interrupted')),
			current_run_ref TEXT,
			candidate_artifact_ref TEXT,
			candidate_workspace_revision TEXT,
			promotion_run_ref TEXT NOT NULL DEFAULT '',
			selection_reason TEXT NOT NULL DEFAULT '',
			promotion_request_id TEXT NOT NULL DEFAULT '',
			verification_state TEXT NOT NULL DEFAULT 'not_required' CHECK (verification_state IN ('not_required', 'pending', 'pass', 'block', 'unknown')),
			verification_required INTEGER NOT NULL DEFAULT 0 CHECK (verification_required IN (0, 1)),
			max_verifier_attempts INTEGER NOT NULL DEFAULT 3,
			max_candidates INTEGER NOT NULL DEFAULT 1,
			verifier_attempts_used INTEGER NOT NULL DEFAULT 0,
			candidates_used INTEGER NOT NULL DEFAULT 0,
			fanout_reason TEXT NOT NULL DEFAULT '',
			max_rounds INTEGER NOT NULL DEFAULT 3,
			current_round INTEGER NOT NULL DEFAULT 1,
			qualified_candidates INTEGER NOT NULL DEFAULT 0,
			max_input_tokens INTEGER NOT NULL DEFAULT 0,
			max_output_tokens INTEGER NOT NULL DEFAULT 0,
			deadline_at INTEGER,
			checks_summary TEXT NOT NULL DEFAULT '',
			changed_files_count INTEGER NOT NULL DEFAULT 0,
			unresolved_items TEXT NOT NULL DEFAULT '',
			failure_reason TEXT NOT NULL DEFAULT '',
			cancelled_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (source_message_id) REFERENCES room_messages(id),
			FOREIGN KEY (owner_named_agent_id) REFERENCES named_agents(id),
			FOREIGN KEY (lead_named_agent_id) REFERENCES named_agents(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_works_room_state ON works(room_id, state, updated_at)`,
		`CREATE TABLE IF NOT EXISTS work_runs (
			id TEXT PRIMARY KEY,
			work_id TEXT NOT NULL,
			named_agent_id TEXT,
			kind TEXT NOT NULL CHECK (kind IN ('producer', 'verifier', 'selector', 'integration')),
			profile TEXT NOT NULL DEFAULT '',
			session_ref TEXT,
			turn_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'interrupted', 'timed_out')),
			goal_revision INTEGER NOT NULL,
			candidate_revision INTEGER NOT NULL DEFAULT 0,
			workspace_revision TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cost_usd REAL NOT NULL DEFAULT 0,
			checks_rerun INTEGER NOT NULL DEFAULT 0,
			findings_count INTEGER NOT NULL DEFAULT 0,
			outcome TEXT NOT NULL DEFAULT '',
			repair_outcome TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			finish_request_id TEXT NOT NULL DEFAULT '',
			round INTEGER NOT NULL DEFAULT 1,
			qualified INTEGER NOT NULL DEFAULT 0 CHECK (qualified IN (0, 1)),
			deadline_at INTEGER,
			queue_reason TEXT NOT NULL DEFAULT '',
			started_at INTEGER,
			ended_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (work_id) REFERENCES works(id) ON DELETE CASCADE,
			FOREIGN KEY (named_agent_id) REFERENCES named_agents(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_work_runs_work ON work_runs(work_id, created_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_runs_active_session ON work_runs(session_ref) WHERE session_ref IS NOT NULL AND state IN ('queued', 'running')`,
		`CREATE TABLE IF NOT EXISTS collaboration_session_bindings (
			session_ref TEXT PRIMARY KEY,
			principal_id TEXT NOT NULL,
			named_agent_id TEXT,
			room_id TEXT,
			work_id TEXT,
			run_id TEXT UNIQUE,
			purpose TEXT NOT NULL CHECK (purpose IN ('conversation', 'coordination', 'work', 'verification')),
			state TEXT NOT NULL CHECK (state IN ('idle', 'queued', 'starting', 'running', 'interrupted', 'missing', 'completed', 'cancelled', 'failed')),
			title TEXT NOT NULL DEFAULT '',
			objective TEXT NOT NULL DEFAULT '',
			parent_session_ref TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			effort TEXT NOT NULL DEFAULT '',
			runtime_version TEXT NOT NULL DEFAULT '',
			failure_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (named_agent_id IS NULL OR named_agent_id = principal_id),
			CHECK (work_id IS NULL OR room_id IS NOT NULL),
			CHECK (run_id IS NULL OR work_id IS NOT NULL),
			FOREIGN KEY (principal_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE,
			FOREIGN KEY (named_agent_id) REFERENCES named_agents(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (work_id) REFERENCES works(id) ON DELETE CASCADE,
			FOREIGN KEY (run_id) REFERENCES work_runs(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS collaboration_session_requests (
			actor_id TEXT NOT NULL,
			source_session_ref TEXT NOT NULL,
			request_id TEXT NOT NULL,
			session_ref TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (actor_id, source_session_ref, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collaboration_sessions_principal ON collaboration_session_bindings(principal_id, state, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_collaboration_sessions_scope ON collaboration_session_bindings(room_id, work_id, principal_id)`,
		`CREATE TABLE IF NOT EXISTS work_artifacts (
			id TEXT PRIMARY KEY,
			work_id TEXT NOT NULL,
			run_id TEXT,
			kind TEXT NOT NULL CHECK (kind IN ('candidate', 'diff', 'snapshot', 'check_log', 'screenshot', 'report', 'other')),
			uri TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			workspace_revision TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			FOREIGN KEY (work_id) REFERENCES works(id) ON DELETE CASCADE,
			FOREIGN KEY (run_id) REFERENCES work_runs(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_work_artifacts_work ON work_artifacts(work_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS work_events (
			id TEXT PRIMARY KEY,
			work_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('state', 'verification', 'correction', 'cancellation', 'recovery')),
			state TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			goal_revision INTEGER NOT NULL DEFAULT 0,
			candidate_revision INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			FOREIGN KEY (work_id) REFERENCES works(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_work_events_work ON work_events(work_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS drafts (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			session_ref TEXT,
			room_id TEXT NOT NULL,
			thread_id TEXT,
			body TEXT NOT NULL,
			basis_seq INTEGER NOT NULL,
			hold_count INTEGER NOT NULL DEFAULT 1,
			state TEXT NOT NULL CHECK (state IN ('held', 'dropped', 'committed', 'expired')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (agent_id) REFERENCES named_agents(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (thread_id) REFERENCES room_messages(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_drafts_agent_state ON drafts(agent_id, state, updated_at)`,
		`CREATE TABLE IF NOT EXISTS reminders (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			fire_at INTEGER NOT NULL,
			note TEXT NOT NULL,
			room_id TEXT,
			thread_id TEXT,
			state TEXT NOT NULL CHECK (state IN ('pending', 'fired', 'cancelled')),
			created_at INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (agent_id) REFERENCES named_agents(id) ON DELETE CASCADE,
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (thread_id) REFERENCES room_messages(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders(state, fire_at, id)`,
		`CREATE TABLE IF NOT EXISTS channel_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("migrate channels database: %w", err)
		}
	}
	// A process can exit after claiming a proposal but before creating the
	// Agent. Return such cards to an actionable state on the next startup.
	if _, err := s.db.Exec(`UPDATE agent_creation_proposals SET state = 'pending', provider = '', model = '' WHERE state = 'processing'`); err != nil {
		return fmt.Errorf("recover agent creation proposals: %w", err)
	}
	if err := s.ensureNamedAgentProviderColumn(); err != nil {
		return err
	}
	if err := s.ensureNamedAgentKindColumns(); err != nil {
		return err
	}
	if err := s.ensureLegacyColumns(); err != nil {
		return err
	}
	if err := s.migrateCollaborationPrincipals(); err != nil {
		return err
	}
	if err := s.migrateInternalDeliveries(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS collaboration_send_requests (
			room_id TEXT NOT NULL,
			from_id TEXT NOT NULL,
			from_session_ref TEXT NOT NULL,
			request_id TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			message_id TEXT NOT NULL,
			PRIMARY KEY (room_id, from_id, from_session_ref, request_id),
			FOREIGN KEY (message_id) REFERENCES collaboration_messages(id) ON DELETE CASCADE
		)`); err != nil {
		return fmt.Errorf("create collaboration request storage: %w", err)
	}
	if err := s.migrateCollaborationSessions(); err != nil {
		return err
	}
	if err := s.migrateWorks(); err != nil {
		return err
	}
	if err := s.ensureNamedAgentAvatars(); err != nil {
		return err
	}
	if err := s.migrateRoomCursors(); err != nil {
		return err
	}
	if err := s.migrateInboxItems(); err != nil {
		return err
	}
	if err := s.recoverPendingVerificationDeliveries(); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_inbox_items_agent_pull ON inbox_items(member_type, member_id, pulled_at, created_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_items_unique ON inbox_items(member_type, member_id, COALESCE(message_id,''), COALESCE(reminder_id,''), kind)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_runs_start_request ON work_runs(work_id, request_id) WHERE request_id != ''`,
		`DROP INDEX IF EXISTS idx_collaboration_request`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_collaboration_session_request ON collaboration_messages(room_id, from_id, COALESCE(from_session_ref, ''), request_id, target_id) WHERE request_id != ''`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("migrate channels inbox index: %w", err)
		}
	}
	return nil
}

// recoverPendingVerificationDeliveries makes the verifier hand-off at-least-once
// across app restarts. A pulled candidate or named-verifier report may have
// lost the room turn that was meant to consume it, and pulled feedback may have
// lost the owner turn. Task and revision predicates keep obsolete deliveries retired.
func (s *Service) recoverPendingVerificationDeliveries() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin verification delivery recovery: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		UPDATE collaboration_messages AS delivery
		SET pulled_at = NULL
		WHERE delivery.pulled_at IS NOT NULL AND (
			(delivery.kind = 'candidate_ready' AND EXISTS (
				SELECT 1 FROM room_messages AS task
				WHERE task.id = delivery.source_message_id
					AND task.kind = 'task'
					AND task.task_verification_required = 1
					AND task.task_state = 'checking'
					AND task.task_goal_revision = delivery.goal_revision
					AND task.task_candidate_revision = delivery.candidate_revision
					AND NOT EXISTS (
						SELECT 1 FROM task_verifications AS verification
						WHERE verification.task_id = task.id
							AND verification.goal_revision = task.task_goal_revision
							AND verification.candidate_revision = task.task_candidate_revision
					)
			)) OR
			(delivery.kind = 'peer_result' AND EXISTS (
				SELECT 1
				FROM room_messages AS task
				JOIN works AS work ON work.id = task.id
				JOIN work_runs AS run ON run.id = work.current_run_ref
				WHERE task.id = delivery.source_message_id
					AND task.task_state = 'checking'
					AND task.task_goal_revision = delivery.goal_revision
					AND task.task_candidate_revision = delivery.candidate_revision
					AND run.kind = 'verifier'
					AND run.state = 'running'
					AND run.profile = delivery.from_id
					AND NOT EXISTS (
						SELECT 1 FROM task_verifications AS verification
						WHERE verification.task_id = task.id
							AND verification.goal_revision = task.task_goal_revision
							AND verification.candidate_revision = task.task_candidate_revision
					)
			)) OR
			(delivery.kind = 'verification_feedback' AND EXISTS (
				SELECT 1
				FROM room_messages AS task
				JOIN task_verifications AS verification ON verification.task_id = task.id
				WHERE task.id = delivery.source_message_id
					AND verification.goal_revision = delivery.goal_revision
					AND verification.candidate_revision = delivery.candidate_revision
					AND task.task_goal_revision = delivery.goal_revision
					AND task.task_candidate_revision = delivery.candidate_revision
					AND ((verification.decision = 'pass' AND task.task_state = 'checking')
						OR (verification.decision = 'block' AND task.task_state = 'revising')
						OR (verification.decision = 'unknown' AND task.task_state = 'needs_human'))
			))
		)`); err != nil {
		return fmt.Errorf("recover verification deliveries: %w", err)
	}
	now := toMillis(s.now())
	if _, err := tx.Exec(`
		INSERT INTO agent_wake_state (agent_id, outstanding, pending, updated_at)
		SELECT DISTINCT delivery.to_agent_id, 1, 0, ?
		FROM collaboration_messages AS delivery
		WHERE delivery.pulled_at IS NULL
				AND delivery.kind IN ('candidate_ready', 'peer_result', 'verification_feedback')
		ON CONFLICT(agent_id) DO UPDATE SET
			outstanding = 1,
			updated_at = excluded.updated_at`, now); err != nil {
		return fmt.Errorf("restore verification delivery wakes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit verification delivery recovery: %w", err)
	}
	return nil
}

func (s *Service) ensureNamedAgentProviderColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(named_agents)`)
	if err != nil {
		return fmt.Errorf("inspect named agent schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("inspect named agent column: %w", err)
		}
		if name == "provider_override" {
			return nil
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE named_agents ADD COLUMN provider_override TEXT`); err != nil {
		return fmt.Errorf("add named agent provider override: %w", err)
	}
	return nil
}

func (s *Service) ensureNamedAgentKindColumns() error {
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "kind", definition: "TEXT NOT NULL DEFAULT 'named'"},
		{name: "room_id", definition: "TEXT"},
	} {
		exists, err := s.tableHasColumn("named_agents", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE named_agents ADD COLUMN %s %s`, column.name, column.definition)); err != nil {
				return fmt.Errorf("add named_agents.%s column: %w", column.name, err)
			}
		}
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS idx_named_agents_name`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_named_agents_name ON named_agents(name) WHERE kind = 'named'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_named_agents_room ON named_agents(room_id) WHERE kind = 'room'`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("migrate named agent role indexes: %w", err)
		}
	}
	return nil
}

func (s *Service) ensureLegacyColumns() error {
	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{table: "room_messages", name: "task_title", definition: "TEXT"},
		{table: "room_messages", name: "task_verification_required", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "room_messages", name: "task_goal_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "room_messages", name: "task_candidate_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "room_messages", name: "images_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "room_messages", name: "files_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "reminders", name: "created_at", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "named_agents", name: "avatar_key", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "named_agents", name: "role", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "named_agents", name: "avatar_image", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "named_agents", name: "engine_override", definition: "TEXT"},
		{table: "named_agents", name: "effort_override", definition: "TEXT"},
		{table: "rooms", name: "avatar_image", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "rooms", name: "membership_revision", definition: "INTEGER NOT NULL DEFAULT 1"},
		{table: "collaboration_messages", name: "kind", definition: "TEXT NOT NULL DEFAULT 'control'"},
		{table: "collaboration_messages", name: "goal_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "collaboration_messages", name: "candidate_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "collaboration_messages", name: "work_id", definition: "TEXT"},
		{table: "collaboration_messages", name: "from_session_ref", definition: "TEXT"},
		{table: "collaboration_messages", name: "target_session_ref", definition: "TEXT"},
		{table: "collaboration_messages", name: "artifact_refs_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "collaboration_messages", name: "consumed_at", definition: "INTEGER"},
		{table: "collaboration_messages", name: "invalidated_at", definition: "INTEGER"},
		{table: "collaboration_messages", name: "target_kind", definition: "TEXT NOT NULL DEFAULT 'named_agent'"},
		{table: "collaboration_messages", name: "target_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_messages", name: "visibility", definition: "TEXT NOT NULL DEFAULT 'private'"},
		{table: "collaboration_messages", name: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_messages", name: "request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_messages", name: "terminal_state", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "task_verifications", name: "goal_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "task_verifications", name: "candidate_revision", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "task_verifications", name: "evidence_refs_json", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "task_verifications", name: "run_ref", definition: "TEXT"},
		{table: "work_runs", name: "named_agent_id", definition: "TEXT"},
		{table: "work_runs", name: "turn_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "work_runs", name: "request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "work_runs", name: "finish_request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "work_runs", name: "round", definition: "INTEGER NOT NULL DEFAULT 1"},
		{table: "work_runs", name: "qualified", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "work_runs", name: "deadline_at", definition: "INTEGER"},
		{table: "work_runs", name: "queue_reason", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "work_runs", name: "cost_usd", definition: "REAL NOT NULL DEFAULT 0"},
		{table: "works", name: "max_rounds", definition: "INTEGER NOT NULL DEFAULT 3"},
		{table: "works", name: "current_round", definition: "INTEGER NOT NULL DEFAULT 1"},
		{table: "works", name: "qualified_candidates", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "works", name: "max_input_tokens", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "works", name: "max_output_tokens", definition: "INTEGER NOT NULL DEFAULT 0"},
		{table: "works", name: "deadline_at", definition: "INTEGER"},
		{table: "works", name: "promotion_run_ref", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "works", name: "selection_reason", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "works", name: "promotion_request_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "drafts", name: "session_ref", definition: "TEXT"},
		{table: "collaboration_session_bindings", name: "title", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "objective", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "parent_session_ref", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "provider", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "model", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "effort", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "runtime_version", definition: "TEXT NOT NULL DEFAULT ''"},
		{table: "collaboration_session_bindings", name: "failure_reason", definition: "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		exists, err := s.tableHasColumn(column.table, column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		statement := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, column.table, column.name, column.definition)
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("add %s.%s column: %w", column.table, column.name, err)
		}
	}
	return nil
}

func (s *Service) ensureNamedAgentAvatars() error {
	rows, err := s.db.Query(`SELECT id FROM named_agents WHERE avatar_key = ''`)
	if err != nil {
		return fmt.Errorf("list named agents without avatars: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan named agent without avatar: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close named agent avatar rows: %w", err)
	}
	for _, id := range ids {
		avatarKey, err := randomNamedAgentAvatarKey()
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE named_agents SET avatar_key = ? WHERE id = ? AND avatar_key = ''`, avatarKey, id); err != nil {
			return fmt.Errorf("assign named agent avatar: %w", err)
		}
	}
	return nil
}

func (s *Service) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read %s schema: %w", table, err)
	}
	return false, nil
}

func (s *Service) migrateRoomCursors() error {
	rows, err := s.db.Query(`PRAGMA table_info(room_cursors)`)
	if err != nil {
		return fmt.Errorf("inspect room cursor schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan room cursor schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close room cursor schema: %w", err)
	}
	if len(columns) == 0 || columns["member_type"] && columns["member_id"] {
		return nil
	}
	if !columns["agent_id"] {
		return errors.New("legacy room_cursors has no member identity column")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin room cursor migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE room_cursors RENAME TO room_cursors_legacy`); err != nil {
		return fmt.Errorf("rename legacy room cursors: %w", err)
	}
	if _, err := tx.Exec(`
		CREATE TABLE room_cursors (
			room_id TEXT NOT NULL,
			member_type TEXT NOT NULL DEFAULT 'agent' CHECK (member_type IN ('human', 'agent')),
			member_id TEXT NOT NULL,
			last_read_seq INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (room_id, member_type, member_id),
			FOREIGN KEY (room_id, member_type, member_id) REFERENCES room_members(room_id, member_type, member_id) ON DELETE CASCADE
		)`); err != nil {
		return fmt.Errorf("create upgraded room cursors: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO room_cursors(room_id, member_type, member_id, last_read_seq)
		SELECT cursor.room_id, 'agent', cursor.agent_id, cursor.last_read_seq
		FROM room_cursors_legacy cursor
		JOIN room_members member
			ON member.room_id = cursor.room_id
			AND member.member_type = 'agent'
			AND member.member_id = cursor.agent_id`); err != nil {
		return fmt.Errorf("copy upgraded room cursors: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE room_cursors_legacy`); err != nil {
		return fmt.Errorf("drop legacy room cursors: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit room cursor migration: %w", err)
	}
	return nil
}

func (s *Service) migrateInboxItems() error {
	rows, err := s.db.Query(`PRAGMA table_info(inbox_items)`)
	if err != nil {
		return fmt.Errorf("inspect channels inbox schema: %w", err)
	}
	columns := make(map[string]bool)
	roomIDNotNull := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan channels inbox schema: %w", err)
		}
		columns[name] = true
		roomIDNotNull = roomIDNotNull || name == "room_id" && notNull != 0
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close channels inbox schema: %w", err)
	}
	if len(columns) == 0 || columns["member_type"] && columns["member_id"] && columns["reminder_id"] && !roomIDNotNull {
		return nil
	}

	memberTypeExpr := "'agent'"
	if columns["member_type"] {
		memberTypeExpr = "member_type"
	}
	memberIDExpr := ""
	if columns["member_id"] {
		memberIDExpr = "member_id"
	} else if columns["agent_id"] {
		memberIDExpr = "agent_id"
	} else {
		return errors.New("legacy inbox_items has no member identity column")
	}
	expr := func(column, fallback string) string {
		if columns[column] {
			return column
		}
		return fallback
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin channels inbox migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE inbox_items RENAME TO inbox_items_legacy`); err != nil {
		return fmt.Errorf("rename legacy channels inbox: %w", err)
	}
	if _, err := tx.Exec(`
		CREATE TABLE inbox_items (
			id TEXT PRIMARY KEY,
			member_type TEXT NOT NULL DEFAULT 'agent' CHECK (member_type IN ('human', 'agent')),
			member_id TEXT NOT NULL,
			room_id TEXT,
			message_id TEXT,
			reminder_id TEXT,
			kind TEXT NOT NULL CHECK (kind IN ('mention', 'reply', 'thread_update', 'task', 'reminder')),
			created_at INTEGER NOT NULL,
			pulled_at INTEGER,
			FOREIGN KEY (room_id, member_type, member_id) REFERENCES room_members(room_id, member_type, member_id) ON DELETE CASCADE,
			FOREIGN KEY (message_id) REFERENCES room_messages(id) ON DELETE CASCADE,
			FOREIGN KEY (reminder_id) REFERENCES reminders(id) ON DELETE CASCADE
		)`); err != nil {
		return fmt.Errorf("create upgraded channels inbox: %w", err)
	}
	copySQL := fmt.Sprintf(`
		INSERT INTO inbox_items(
			id, member_type, member_id, room_id, message_id, reminder_id, kind, created_at, pulled_at
		)
		SELECT id, %s, %s, %s, %s, %s, %s, %s, %s
		FROM inbox_items_legacy`,
		memberTypeExpr, memberIDExpr, expr("room_id", "NULL"), expr("message_id", "NULL"),
		expr("reminder_id", "NULL"), expr("kind", "'mention'"), expr("created_at", "0"), expr("pulled_at", "NULL"))
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy upgraded channels inbox: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE inbox_items_legacy`); err != nil {
		return fmt.Errorf("drop legacy channels inbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit channels inbox migration: %w", err)
	}
	return nil
}

func (s *Service) CreateNamedAgent(ctx context.Context, params CreateNamedAgentParams) (AgentCredential, error) {
	return s.createAgent(ctx, params)
}

func (s *Service) createAgent(ctx context.Context, params CreateNamedAgentParams) (AgentCredential, error) {
	if s == nil || s.db == nil {
		return AgentCredential{}, errors.New("channels service is not configured")
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return AgentCredential{}, errors.New("named agent name is required")
	}
	if len([]rune(name)) > 64 {
		return AgentCredential{}, errors.New("named agent name exceeds 64 characters")
	}
	role := strings.TrimSpace(params.Role)
	if len([]rune(role)) > 280 {
		return AgentCredential{}, errors.New("named agent role exceeds 280 characters")
	}
	avatarKey, err := normalizeNamedAgentAvatarKey(params.AvatarKey)
	if err != nil {
		return AgentCredential{}, err
	}
	if avatarKey == "" {
		avatarKey, err = randomNamedAgentAvatarKey()
		if err != nil {
			return AgentCredential{}, err
		}
	}
	avatarImage, err := normalizeNamedAgentAvatarImage(params.AvatarImage)
	if err != nil {
		return AgentCredential{}, err
	}
	engine := strings.TrimSpace(params.EngineOverride)
	provider := strings.TrimSpace(params.ProviderOverride)
	model := strings.TrimSpace(params.ModelOverride)
	effort := strings.TrimSpace(params.EffortOverride)
	if engine == "" {
		engine = "wuu"
	}
	if engine == "wuu" && (provider == "") != (model == "") {
		return AgentCredential{}, errors.New("named agent provider and model overrides must be set together")
	}
	if engine != "wuu" && (provider != "" || model == "") {
		return AgentCredential{}, errors.New("external named agent engine requires a model and does not accept a provider override")
	}
	if effort != "" && model == "" {
		return AgentCredential{}, errors.New("named agent effort override requires a model override")
	}
	id, err := randomID("agent", 12)
	if err != nil {
		return AgentCredential{}, err
	}
	token, err := randomID("chat", 32)
	if err != nil {
		return AgentCredential{}, err
	}
	now := fromMillis(toMillis(s.now()))
	agent := NamedAgent{
		ID:               id,
		Name:             name,
		Role:             role,
		MemoryDir:        filepath.Join(s.dir, "agents", id, "memory"),
		AvatarKey:        avatarKey,
		AvatarImage:      avatarImage,
		EngineOverride:   engine,
		ProviderOverride: provider,
		ModelOverride:    model,
		EffortOverride:   effort,
		Autostart:        params.Autostart,
		CreatedAt:        now,
	}
	if err := securefs.Mkdir(agent.MemoryDir); err != nil {
		return AgentCredential{}, fmt.Errorf("create named agent memory directory: %w", err)
	}
	if err := securefs.PreCreateFile(filepath.Join(agent.MemoryDir, agentMemoryIndexFile)); err != nil {
		return AgentCredential{}, fmt.Errorf("initialize named agent memory index: %w", err)
	}
	if err := securefs.WriteFileAtomic(filepath.Join(filepath.Dir(agent.MemoryDir), agentTokenFile), []byte(token+"\n")); err != nil {
		return AgentCredential{}, fmt.Errorf("persist named agent token: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCredential{}, fmt.Errorf("begin named agent create: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collaboration_principals(id, kind) VALUES (?, 'named_agent')`, agent.ID); err != nil {
		return AgentCredential{}, fmt.Errorf("insert named agent principal: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
			INSERT INTO named_agents (id, name, role, kind, room_id, memory_dir, avatar_key, avatar_image, engine_override, provider_override, model_override, effort_override, token_hash, autostart, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		agent.ID, agent.Name, agent.Role, "named", nil, agent.MemoryDir, agent.AvatarKey, agent.AvatarImage, nullableString(agent.EngineOverride), nullableString(agent.ProviderOverride), nullableString(agent.ModelOverride), nullableString(agent.EffortOverride), tokenHash(token), boolInt(agent.Autostart), toMillis(now),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return AgentCredential{}, fmt.Errorf("%w: named agent %q already exists", ErrConflict, name)
		}
		return AgentCredential{}, fmt.Errorf("insert named agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_wake_state (agent_id, outstanding, pending, updated_at)
		VALUES (?, 0, 0, ?)`, agent.ID, toMillis(now)); err != nil {
		return AgentCredential{}, fmt.Errorf("initialize named agent wake state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentCredential{}, fmt.Errorf("commit named agent create: %w", err)
	}
	return AgentCredential{Agent: agent, Token: token}, nil
}

func (s *Service) GetNamedAgent(ctx context.Context, id string) (NamedAgent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return NamedAgent{}, errors.New("named agent id is required")
	}
	return scanNamedAgent(s.db.QueryRowContext(ctx, `
		SELECT id, name, role, memory_dir, avatar_key, avatar_image, COALESCE(engine_override, ''), COALESCE(provider_override, ''), COALESCE(model_override, ''), COALESCE(effort_override, ''), autostart, created_at
		FROM named_agents WHERE id = ? AND kind = 'named'`, id))
}

func (s *Service) ListNamedAgents(ctx context.Context) ([]NamedAgent, error) {
	return s.listNamedAgents(ctx)
}

func (s *Service) listNamedAgents(ctx context.Context) ([]NamedAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, role, memory_dir, avatar_key, avatar_image, COALESCE(engine_override, ''), COALESCE(provider_override, ''), COALESCE(model_override, ''), COALESCE(effort_override, ''), autostart, created_at
		FROM named_agents WHERE kind = 'named' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list named agents: %w", err)
	}
	defer rows.Close()
	agents := make([]NamedAgent, 0)
	for rows.Next() {
		agent, err := scanNamedAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list named agents: %w", err)
	}
	return agents, nil
}

func (s *Service) UpdateNamedAgent(ctx context.Context, params UpdateNamedAgentParams) (NamedAgent, error) {
	id := strings.TrimSpace(params.ID)
	name := strings.TrimSpace(params.Name)
	role := strings.TrimSpace(params.Role)
	engine := strings.TrimSpace(params.EngineOverride)
	provider := strings.TrimSpace(params.ProviderOverride)
	model := strings.TrimSpace(params.ModelOverride)
	effort := strings.TrimSpace(params.EffortOverride)
	if id == "" || name == "" {
		return NamedAgent{}, errors.New("named agent id and name are required")
	}
	if len([]rune(role)) > 280 {
		return NamedAgent{}, errors.New("named agent role exceeds 280 characters")
	}
	if engine == "" {
		engine = "wuu"
	}
	if engine == "wuu" && (provider == "") != (model == "") {
		return NamedAgent{}, errors.New("named agent provider and model overrides must be set together")
	}
	if engine != "wuu" && (provider != "" || model == "") {
		return NamedAgent{}, errors.New("external named agent engine requires a model and does not accept a provider override")
	}
	if effort != "" && model == "" {
		return NamedAgent{}, errors.New("named agent effort override requires a model override")
	}
	avatarKey, err := normalizeNamedAgentAvatarKey(params.AvatarKey)
	if err != nil {
		return NamedAgent{}, err
	}
	current, err := s.GetNamedAgent(ctx, id)
	if err != nil {
		return NamedAgent{}, err
	}
	if avatarKey == "" {
		avatarKey = current.AvatarKey
	}
	avatarImage := current.AvatarImage
	if params.AvatarImage != nil {
		avatarImage, err = normalizeNamedAgentAvatarImage(*params.AvatarImage)
		if err != nil {
			return NamedAgent{}, err
		}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE named_agents SET name = ?, role = ?, avatar_key = ?, avatar_image = ?, engine_override = ?, provider_override = ?, model_override = ?, effort_override = ? WHERE id = ?`,
		name, role, avatarKey, avatarImage, nullableString(engine), nullableString(provider), nullableString(model), nullableString(effort), id)
	if err != nil {
		if isUniqueConstraint(err) {
			return NamedAgent{}, fmt.Errorf("%w: named agent %q already exists", ErrConflict, name)
		}
		return NamedAgent{}, fmt.Errorf("update named agent: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return NamedAgent{}, ErrNotFound
	}
	return s.GetNamedAgent(ctx, id)
}

func (s *Service) DeleteNamedAgent(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("named agent id is required")
	}
	agent, err := s.GetNamedAgent(ctx, id)
	if err != nil {
		return err
	}
	return s.deleteAgent(ctx, agent)
}

func (s *Service) deleteAgent(ctx context.Context, agent NamedAgent) error {
	id := agent.ID
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin named agent delete: %w", err)
	}
	defer tx.Rollback()
	var taskCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_messages WHERE task_owner = ?`, id).Scan(&taskCount); err != nil {
		return fmt.Errorf("count named agent task history: %w", err)
	}
	if taskCount > 0 {
		return fmt.Errorf("%w: named agent %q has task history and cannot be deleted", ErrConflict, id)
	}
	type roomRemoval struct {
		roomID, createdBy, runtimeID string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT room.id, room.created_by, COALESCE(runtime.id, '')
		FROM room_members member
		JOIN rooms room ON room.id = member.room_id AND room.kind = 'channel'
		LEFT JOIN room_runtimes runtime ON runtime.room_id = room.id
		WHERE member.member_type = 'agent' AND member.member_id = ?
		ORDER BY room.id`, id)
	if err != nil {
		return fmt.Errorf("list named agent room removals: %w", err)
	}
	var removals []roomRemoval
	for rows.Next() {
		var removal roomRemoval
		if err := rows.Scan(&removal.roomID, &removal.createdBy, &removal.runtimeID); err != nil {
			rows.Close()
			return fmt.Errorf("scan named agent room removal: %w", err)
		}
		removals = append(removals, removal)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close named agent room removals: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate named agent room removals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM rooms
		WHERE kind = 'dm' AND id IN (
			SELECT room_id FROM room_members WHERE member_type = 'agent' AND member_id = ?
		)`, id); err != nil {
		return fmt.Errorf("delete named agent direct messages: %w", err)
	}
	wakeIDs := make([]string, 0, len(removals))
	now := toMillis(s.now())
	for _, removal := range removals {
		if _, err := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`, removal.roomID, id); err != nil {
			return fmt.Errorf("remove named agent membership: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drafts SET state = 'dropped', updated_at = ? WHERE room_id = ? AND agent_id = ? AND state = 'held'`, now, removal.roomID, id); err != nil {
			return fmt.Errorf("drop deleted named agent drafts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at = ? WHERE room_id = ? AND (to_agent_id = ? OR from_id = ?) AND consumed_at IS NULL AND invalidated_at IS NULL`, now, removal.roomID, id, id); err != nil {
			return fmt.Errorf("invalidate deleted named agent deliveries: %w", err)
		}
		if err := recordMembershipChangeTx(ctx, tx, removal.roomID, removal.createdBy, nil, []string{agent.Name}, now); err != nil {
			return err
		}
		if removal.runtimeID != "" {
			shouldWake, err := requestWakeTx(ctx, tx, removal.runtimeID, now)
			if err != nil {
				return err
			}
			if shouldWake {
				wakeIDs = append(wakeIDs, removal.runtimeID)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM named_agents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete named agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM collaboration_principals WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete named agent principal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit named agent delete: %w", err)
	}
	for _, runtimeID := range wakeIDs {
		if s.wake != nil {
			s.wake.Deliver(runtimeID)
		}
	}
	return os.RemoveAll(filepath.Dir(agent.MemoryDir))
}

func (s *Service) AuthenticateAgent(ctx context.Context, agentID, token string) (NamedAgent, error) {
	agentID = strings.TrimSpace(agentID)
	token = strings.TrimSpace(token)
	if agentID == "" || token == "" {
		return NamedAgent{}, ErrUnauthorized
	}
	var storedHash string
	var agent NamedAgent
	var autostart int
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, role, memory_dir, avatar_key, avatar_image, COALESCE(engine_override, ''), COALESCE(provider_override, ''), COALESCE(model_override, ''), COALESCE(effort_override, ''), autostart, created_at, token_hash
		FROM named_agents WHERE id = ? AND kind = 'named'`, agentID,
	).Scan(&agent.ID, &agent.Name, &agent.Role, &agent.MemoryDir, &agent.AvatarKey, &agent.AvatarImage, &agent.EngineOverride, &agent.ProviderOverride, &agent.ModelOverride, &agent.EffortOverride, &autostart, &createdAt, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return NamedAgent{}, ErrUnauthorized
	}
	if err != nil {
		return NamedAgent{}, fmt.Errorf("authenticate named agent: %w", err)
	}
	actual := tokenHash(token)
	if len(actual) != len(storedHash) || subtle.ConstantTimeCompare([]byte(actual), []byte(storedHash)) != 1 {
		return NamedAgent{}, ErrUnauthorized
	}
	agent.Autostart = autostart != 0
	agent.CreatedAt = fromMillis(createdAt)
	return agent, nil
}

func (s *Service) loadAgentToken(ctx context.Context, agentID string) (string, error) {
	agent, err := s.GetNamedAgent(ctx, agentID)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(agent.MemoryDir), agentTokenFile))
	if err != nil {
		return "", fmt.Errorf("read named agent token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if _, err := s.AuthenticateAgent(ctx, agent.ID, token); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) CreateRoom(ctx context.Context, params CreateRoomParams) (Room, error) {
	if params.Kind != RoomChannel && params.Kind != RoomDM {
		return Room{}, fmt.Errorf("invalid room kind %q", params.Kind)
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return Room{}, errors.New("room name is required")
	}
	createdBy := strings.TrimSpace(params.CreatedBy)
	if createdBy == "" {
		return Room{}, errors.New("room creator is required")
	}
	avatarImage, err := normalizeAvatarImage(params.AvatarImage, "room avatar")
	if err != nil {
		return Room{}, err
	}
	members, err := normalizeMembers(params.Members, createdBy)
	if err != nil {
		return Room{}, err
	}
	if roomAgentCount(members) > MaxRoomAgents {
		return Room{}, fmt.Errorf("room agent limit is %d", MaxRoomAgents)
	}
	if params.Kind == RoomDM && len(members) != 2 {
		return Room{}, errors.New("dm rooms require exactly two members")
	}
	id, err := randomID("room", 12)
	if err != nil {
		return Room{}, err
	}
	now := fromMillis(toMillis(s.now()))
	room := Room{ID: id, Kind: params.Kind, Name: name, AvatarImage: avatarImage, CreatedBy: createdBy, CreatedAt: now, MembershipRevision: 1}
	for index := range members {
		members[index].RoomID = id
		members[index].JoinedAt = now
	}
	room.Members = members
	var runtimeCredential roomRuntimeCredential
	keepRoomRuntime := true
	if room.Kind == RoomChannel {
		credential, err := s.prepareRoomRuntime(room.ID, room.Name)
		if err != nil {
			return Room{}, fmt.Errorf("prepare room runtime: %w", err)
		}
		runtimeCredential = credential
		room.RuntimeID = credential.Runtime.ID
		room.AgentID = room.RuntimeID
		room.AvatarKey = room.ID
		keepRoomRuntime = false
		defer func() {
			if !keepRoomRuntime {
				_ = os.RemoveAll(filepath.Dir(runtimeCredential.Runtime.MemoryDir))
			}
		}()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, fmt.Errorf("begin room create: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rooms (id, kind, name, avatar_image, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		room.ID, room.Kind, room.Name, room.AvatarImage, room.CreatedBy, toMillis(room.CreatedAt)); err != nil {
		return Room{}, fmt.Errorf("insert room: %w", err)
	}
	if room.Kind == RoomChannel {
		if err := insertRoomRuntimeTx(ctx, tx, runtimeCredential); err != nil {
			return Room{}, err
		}
	}
	for _, member := range room.Members {
		if member.MemberType == MemberAgent {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM named_agents WHERE id = ? AND kind = 'named'`, member.MemberID).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Room{}, fmt.Errorf("%w: named agent %q", ErrNotFound, member.MemberID)
				}
				return Room{}, fmt.Errorf("validate room agent member: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO room_members (room_id, member_type, member_id, joined_at) VALUES (?, ?, ?, ?)`,
			room.ID, member.MemberType, member.MemberID, toMillis(member.JoinedAt)); err != nil {
			return Room{}, fmt.Errorf("insert room member: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO room_cursors (room_id, member_type, member_id, last_read_seq) VALUES (?, ?, ?, 0)`, room.ID, string(member.MemberType), member.MemberID); err != nil {
			return Room{}, fmt.Errorf("initialize room cursor: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Room{}, fmt.Errorf("commit room create: %w", err)
	}
	keepRoomRuntime = true
	return room, nil
}

func roomAgentName(roomName string) string {
	runes := []rune(strings.TrimSpace(roomName))
	if len(runes) > 64 {
		runes = runes[:64]
	}
	return string(runes)
}

// OpenDirectMessage returns the one persistent DM shared by a human and a
// named agent, creating it when needed. The pair table makes this operation
// idempotent across restarts and protects against concurrent creators.
func (s *Service) OpenDirectMessage(ctx context.Context, humanID, agentID string) (Room, error) {
	humanID = strings.TrimSpace(humanID)
	agentID = strings.TrimSpace(agentID)
	if humanID == "" {
		return Room{}, errors.New("direct message human id is required")
	}
	if agentID == "" {
		return Room{}, errors.New("direct message agent id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var agentName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM named_agents WHERE id = ? AND kind = 'named'`, agentID).Scan(&agentName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Room{}, fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
		}
		return Room{}, fmt.Errorf("load direct message agent: %w", err)
	}
	var roomID string
	err := s.db.QueryRowContext(ctx, `
		SELECT room_id FROM direct_messages WHERE human_id = ? AND agent_id = ?`, humanID, agentID,
	).Scan(&roomID)
	if err == nil {
		return s.GetRoom(ctx, roomID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("find direct message: %w", err)
	}

	// Adopt a DM created before the pair index existed instead of duplicating
	// it during migration from the original room-only representation.
	err = s.db.QueryRowContext(ctx, `
		SELECT rooms.id
		FROM rooms
		JOIN room_members AS human_member
			ON human_member.room_id = rooms.id AND human_member.member_type = 'human' AND human_member.member_id = ?
		JOIN room_members AS agent_member
			ON agent_member.room_id = rooms.id AND agent_member.member_type = 'agent' AND agent_member.member_id = ?
		WHERE rooms.kind = 'dm'
			AND (SELECT COUNT(*) FROM room_members WHERE room_id = rooms.id) = 2
		ORDER BY rooms.created_at, rooms.id
		LIMIT 1`, humanID, agentID,
	).Scan(&roomID)
	if err == nil {
		if _, insertErr := s.db.ExecContext(ctx, `
			INSERT INTO direct_messages (human_id, agent_id, room_id) VALUES (?, ?, ?)
			ON CONFLICT(human_id, agent_id) DO NOTHING`, humanID, agentID, roomID); insertErr != nil {
			return Room{}, fmt.Errorf("index existing direct message: %w", insertErr)
		}
		if lookupErr := s.db.QueryRowContext(ctx, `
			SELECT room_id FROM direct_messages WHERE human_id = ? AND agent_id = ?`, humanID, agentID,
		).Scan(&roomID); lookupErr != nil {
			return Room{}, fmt.Errorf("reload indexed direct message: %w", lookupErr)
		}
		return s.GetRoom(ctx, roomID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("find legacy direct message: %w", err)
	}

	id, err := randomID("room", 12)
	if err != nil {
		return Room{}, err
	}
	now := fromMillis(toMillis(s.now()))
	room := Room{
		ID: id, Kind: RoomDM, Name: agentName, CreatedBy: humanID, CreatedAt: now, MembershipRevision: 1,
		Members: []RoomMember{
			{RoomID: id, MemberType: MemberHuman, MemberID: humanID, JoinedAt: now},
			{RoomID: id, MemberType: MemberAgent, MemberID: agentID, JoinedAt: now},
		},
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, fmt.Errorf("begin direct message create: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rooms (id, kind, name, avatar_image, created_by, created_at) VALUES (?, ?, ?, '', ?, ?)`,
		room.ID, room.Kind, room.Name, room.CreatedBy, toMillis(room.CreatedAt)); err != nil {
		return Room{}, fmt.Errorf("insert direct message room: %w", err)
	}
	for _, member := range room.Members {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO room_members (room_id, member_type, member_id, joined_at) VALUES (?, ?, ?, ?)`,
			room.ID, member.MemberType, member.MemberID, toMillis(member.JoinedAt)); err != nil {
			return Room{}, fmt.Errorf("insert direct message member: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO room_cursors (room_id, member_type, member_id, last_read_seq) VALUES (?, ?, ?, 0)`,
			room.ID, member.MemberType, member.MemberID); err != nil {
			return Room{}, fmt.Errorf("initialize direct message cursor: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO direct_messages (human_id, agent_id, room_id) VALUES (?, ?, ?)`, humanID, agentID, room.ID); err != nil {
		if isUniqueConstraint(err) {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return Room{}, fmt.Errorf("resolve direct message create conflict: %w", rollbackErr)
			}
			if lookupErr := s.db.QueryRowContext(ctx, `
				SELECT room_id FROM direct_messages WHERE human_id = ? AND agent_id = ?`, humanID, agentID,
			).Scan(&roomID); lookupErr != nil {
				return Room{}, fmt.Errorf("reload concurrent direct message: %w", lookupErr)
			}
			return s.GetRoom(ctx, roomID)
		}
		return Room{}, fmt.Errorf("index direct message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Room{}, fmt.Errorf("commit direct message create: %w", err)
	}
	return room, nil
}

func (s *Service) GetRoom(ctx context.Context, id string) (Room, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Room{}, errors.New("room id is required")
	}
	var room Room
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT room.id, room.kind, room.name, room.avatar_image, room.created_by, room.membership_revision, room.created_at,
			COALESCE(runtime.id, '')
		FROM rooms room
		LEFT JOIN room_runtimes runtime ON runtime.room_id = room.id
		WHERE room.id = ?`, id,
	).Scan(&room.ID, &room.Kind, &room.Name, &room.AvatarImage, &room.CreatedBy, &room.MembershipRevision, &createdAt, &room.RuntimeID)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("%w: room %q", ErrNotFound, id)
	}
	if err != nil {
		return Room{}, fmt.Errorf("get room: %w", err)
	}
	room.CreatedAt = fromMillis(createdAt)
	room.AgentID = room.RuntimeID
	room.AvatarKey = room.ID
	rows, err := s.db.QueryContext(ctx, `
		SELECT member.member_type, member.member_id, member.joined_at
		FROM room_members member
		WHERE member.room_id = ?
		ORDER BY member.joined_at, member.member_type, member.member_id`, id)
	if err != nil {
		return Room{}, fmt.Errorf("list room members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member RoomMember
		var joinedAt int64
		if err := rows.Scan(&member.MemberType, &member.MemberID, &joinedAt); err != nil {
			return Room{}, fmt.Errorf("scan room member: %w", err)
		}
		member.RoomID = id
		member.JoinedAt = fromMillis(joinedAt)
		room.Members = append(room.Members, member)
	}
	if err := rows.Err(); err != nil {
		return Room{}, fmt.Errorf("list room members: %w", err)
	}
	return room, nil
}

func (s *Service) UpdateRoomAvatar(ctx context.Context, id, avatarImage string) (Room, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Room{}, errors.New("room id is required")
	}
	normalized, err := normalizeAvatarImage(avatarImage, "room avatar")
	if err != nil {
		return Room{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE rooms SET avatar_image = ? WHERE id = ?`, normalized, id)
	if err != nil {
		return Room{}, fmt.Errorf("update room avatar: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Room{}, ErrNotFound
	}
	return s.GetRoom(ctx, id)
}

func (s *Service) ListRooms(ctx context.Context) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM rooms ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan room: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close room list: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	rooms := make([]Room, 0, len(ids))
	for _, id := range ids {
		room, err := s.GetRoom(ctx, id)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, nil
}

func (s *Service) UpdateRoom(ctx context.Context, params UpdateRoomParams) (Room, error) {
	id := strings.TrimSpace(params.RoomID)
	if id == "" {
		return Room{}, errors.New("room id is required")
	}
	if params.Name == nil && params.Members == nil {
		return Room{}, errors.New("room update requires a name or members")
	}
	var name string
	if params.Name != nil {
		name = strings.TrimSpace(*params.Name)
		if name == "" {
			return Room{}, errors.New("room name is required")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, fmt.Errorf("begin room update: %w", err)
	}
	defer tx.Rollback()

	var kind RoomKind
	var createdBy string
	membershipChanged := false
	if err := tx.QueryRowContext(ctx, `SELECT kind, created_by FROM rooms WHERE id = ?`, id).Scan(&kind, &createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Room{}, fmt.Errorf("%w: room %q", ErrNotFound, id)
		}
		return Room{}, fmt.Errorf("read room for update: %w", err)
	}
	if kind == RoomDM && params.Members != nil {
		return Room{}, errors.New("direct message members cannot be changed")
	}
	if params.Name != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE rooms SET name = ? WHERE id = ?`, name, id); err != nil {
			return Room{}, fmt.Errorf("update room name: %w", err)
		}
	}
	if params.Members != nil {
		members, err := normalizeMembers(*params.Members, createdBy)
		if err != nil {
			return Room{}, err
		}
		if kind == RoomDM && len(members) != 2 {
			return Room{}, errors.New("dm rooms require exactly two members")
		}
		desiredAgents := make(map[string]struct{}, len(members))
		var addedNames, removedNames []string
		for _, member := range members {
			if member.MemberType != MemberAgent {
				continue
			}
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM named_agents WHERE id = ? AND kind = 'named'`, member.MemberID).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Room{}, fmt.Errorf("%w: named agent %q", ErrNotFound, member.MemberID)
				}
				return Room{}, fmt.Errorf("validate room agent member: %w", err)
			}
			desiredAgents[member.MemberID] = struct{}{}
		}

		rows, err := tx.QueryContext(ctx, `
			SELECT member.member_id
			FROM room_members member
			JOIN named_agents agent ON agent.id = member.member_id
			WHERE member.room_id = ? AND member.member_type = 'agent' AND agent.kind = 'named'`, id)
		if err != nil {
			return Room{}, fmt.Errorf("list room agent members for update: %w", err)
		}
		existingAgents := make(map[string]struct{})
		for rows.Next() {
			var agentID string
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return Room{}, fmt.Errorf("scan room agent member for update: %w", err)
			}
			existingAgents[agentID] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return Room{}, fmt.Errorf("close room agent members for update: %w", err)
		}
		if err := rows.Err(); err != nil {
			return Room{}, fmt.Errorf("list room agent members for update: %w", err)
		}
		if len(desiredAgents) > MaxRoomAgents {
			for agentID := range desiredAgents {
				if _, exists := existingAgents[agentID]; !exists {
					return Room{}, fmt.Errorf("room agent limit is %d", MaxRoomAgents)
				}
			}
		}
		for agentID := range existingAgents {
			if _, keep := desiredAgents[agentID]; keep {
				continue
			}
			var ownedTasks int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_messages WHERE room_id = ? AND task_owner = ? AND task_state != 'done'`, id, agentID).Scan(&ownedTasks); err != nil {
				return Room{}, fmt.Errorf("count removed room agent task ownership: %w", err)
			}
			if ownedTasks > 0 {
				return Room{}, fmt.Errorf("%w: agent %q still owns %d active task(s) in room", ErrConflict, agentID, ownedTasks)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`, id, agentID); err != nil {
				return Room{}, fmt.Errorf("remove room agent member: %w", err)
			}
			membershipChanged = true
			if name, err := namedAgentNameTx(ctx, tx, agentID); err == nil {
				removedNames = append(removedNames, name)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE drafts SET state = 'dropped', updated_at = ? WHERE room_id = ? AND agent_id = ? AND state = 'held'`, toMillis(s.now()), id, agentID); err != nil {
				return Room{}, fmt.Errorf("drop removed room agent drafts: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at = ? WHERE room_id = ? AND (to_agent_id = ? OR from_id = ?) AND consumed_at IS NULL AND invalidated_at IS NULL`, toMillis(s.now()), id, agentID, agentID); err != nil {
				return Room{}, fmt.Errorf("invalidate removed room agent deliveries: %w", err)
			}
		}
		joinedAt := toMillis(s.now())
		for agentID := range desiredAgents {
			if _, exists := existingAgents[agentID]; exists {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO room_members (room_id, member_type, member_id, joined_at) VALUES (?, 'agent', ?, ?)`, id, agentID, joinedAt); err != nil {
				return Room{}, fmt.Errorf("add room agent member: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO room_cursors (room_id, member_type, member_id, last_read_seq) VALUES (?, 'agent', ?, 0)`, id, agentID); err != nil {
				return Room{}, fmt.Errorf("initialize room agent cursor: %w", err)
			}
			membershipChanged = true
			if name, err := namedAgentNameTx(ctx, tx, agentID); err == nil {
				addedNames = append(addedNames, name)
			}
		}
		if membershipChanged {
			if err := recordMembershipChangeTx(ctx, tx, id, createdBy, addedNames, removedNames, toMillis(s.now())); err != nil {
				return Room{}, err
			}
		}
	}
	var wakeRuntimeID string
	var shouldWake bool
	if membershipChanged {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM room_runtimes WHERE room_id = ?`, id).Scan(&wakeRuntimeID); err == nil {
			shouldWake, err = requestWakeTx(ctx, tx, wakeRuntimeID, toMillis(s.now()))
			if err != nil {
				return Room{}, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Room{}, fmt.Errorf("resolve changed room runtime: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Room{}, fmt.Errorf("commit room update: %w", err)
	}
	if shouldWake && s.wake != nil {
		s.wake.Deliver(wakeRuntimeID)
	}
	return s.GetRoom(ctx, id)
}

func namedAgentNameTx(ctx context.Context, tx *sql.Tx, agentID string) (string, error) {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM named_agents WHERE id = ? AND kind = 'named'`, agentID).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func recordMembershipChangeTx(ctx context.Context, tx *sql.Tx, roomID, authorID string, addedNames, removedNames []string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET membership_revision = membership_revision + 1 WHERE id = ?`, roomID); err != nil {
		return fmt.Errorf("advance room membership revision: %w", err)
	}
	sort.Strings(addedNames)
	sort.Strings(removedNames)
	parts := make([]string, 0, 2)
	if len(addedNames) > 0 {
		parts = append(parts, strings.Join(addedNames, "、")+" 加入了群聊")
	}
	if len(removedNames) > 0 {
		parts = append(parts, strings.Join(removedNames, "、")+" 离开了群聊")
	}
	if len(parts) == 0 {
		return nil
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, roomID).Scan(&seq); err != nil {
		return fmt.Errorf("allocate membership event sequence: %w", err)
	}
	messageID, err := randomID("msg", 12)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (id, room_id, seq, author_type, author_id, kind, body, mentions_json, created_at)
		VALUES (?, ?, ?, 'human', ?, 'system', ?, '[]', ?)`, messageID, roomID, seq, authorID, strings.Join(parts, "；"), now); err != nil {
		return fmt.Errorf("insert room membership event: %w", err)
	}
	return nil
}

func (s *Service) EnsureBootstrap(ctx context.Context, humanID string) (BootstrapResult, error) {
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()
	var completed string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM channel_metadata WHERE key = 'bootstrap_completed'`).Scan(&completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BootstrapResult{}, fmt.Errorf("read channel bootstrap state: %w", err)
	}
	if err := s.ensureRoomRuntimes(ctx); err != nil {
		return BootstrapResult{}, err
	}
	agents, err := s.ListNamedAgents(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	rooms, err := s.ListRooms(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	if completed == "1" {
		return BootstrapResult{Agents: agents, Rooms: rooms}, nil
	}
	if len(agents) == 0 && len(rooms) == 0 {
		credential, err := s.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Andy", Autostart: true})
		if err != nil {
			return BootstrapResult{}, err
		}
		room, err := s.CreateRoom(ctx, CreateRoomParams{
			Kind: RoomChannel, Name: "General", CreatedBy: humanID,
			Members: []RoomMember{{MemberType: MemberAgent, MemberID: credential.Agent.ID}},
		})
		if err != nil {
			_ = s.DeleteNamedAgent(ctx, credential.Agent.ID)
			return BootstrapResult{}, err
		}
		agents = []NamedAgent{credential.Agent}
		rooms = []Room{room}
	} else if len(agents) == 1 && agents[0].Name == "Andy" && len(rooms) == 0 {
		room, err := s.CreateRoom(ctx, CreateRoomParams{
			Kind: RoomChannel, Name: "General", CreatedBy: humanID,
			Members: []RoomMember{{MemberType: MemberAgent, MemberID: agents[0].ID}},
		})
		if err != nil {
			return BootstrapResult{}, err
		}
		rooms = []Room{room}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO channel_metadata (key, value) VALUES ('bootstrap_completed', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		return BootstrapResult{}, fmt.Errorf("persist channel bootstrap state: %w", err)
	}
	return BootstrapResult{Agents: agents, Rooms: rooms}, nil
}

func (s *Service) DeleteRoom(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("room id is required")
	}
	var runtimeID, memoryDir string
	err := s.db.QueryRowContext(ctx, `SELECT id, memory_dir FROM room_runtimes WHERE room_id = ?`, id).Scan(&runtimeID, &memoryDir)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find room runtime for delete: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete room: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	if runtimeID != "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM collaboration_principals WHERE id = ?`, runtimeID); err != nil {
			return fmt.Errorf("delete room runtime principal: %w", err)
		}
		if err := os.RemoveAll(filepath.Dir(memoryDir)); err != nil {
			return fmt.Errorf("delete room runtime state: %w", err)
		}
	}
	return nil
}

func (s *Service) WakeState(ctx context.Context, agentID string) (WakeState, error) {
	var state WakeState
	var outstanding, pending int
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT agent_id, outstanding, pending, updated_at FROM agent_wake_state WHERE agent_id = ?`,
		strings.TrimSpace(agentID),
	).Scan(&state.AgentID, &outstanding, &pending, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WakeState{}, fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
	}
	if err != nil {
		return WakeState{}, fmt.Errorf("get wake state: %w", err)
	}
	state.Outstanding = outstanding != 0
	state.Pending = pending != 0
	state.UpdatedAt = fromMillis(updatedAt)
	return state, nil
}

func sqliteDSN(path string) string {
	// Normalize Windows paths for file URI: C:\Users\... → /C:/Users/...
	// On Unix filepath.ToSlash is a no-op and paths already start with /.
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	query := u.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	return u.String()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNamedAgent(row scanner) (NamedAgent, error) {
	var agent NamedAgent
	var autostart int
	var createdAt int64
	if err := row.Scan(&agent.ID, &agent.Name, &agent.Role, &agent.MemoryDir, &agent.AvatarKey, &agent.AvatarImage, &agent.EngineOverride, &agent.ProviderOverride, &agent.ModelOverride, &agent.EffortOverride, &autostart, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NamedAgent{}, ErrNotFound
		}
		return NamedAgent{}, fmt.Errorf("scan named agent: %w", err)
	}
	agent.Autostart = autostart != 0
	agent.CreatedAt = fromMillis(createdAt)
	return agent, nil
}

func normalizeMembers(input []RoomMember, createdBy string) ([]RoomMember, error) {
	members := make([]RoomMember, 0, len(input)+1)
	seen := make(map[string]struct{}, len(input)+1)
	add := func(memberType MemberType, memberID string) error {
		memberID = strings.TrimSpace(memberID)
		if memberType != MemberHuman && memberType != MemberAgent {
			return fmt.Errorf("invalid room member type %q", memberType)
		}
		if memberID == "" {
			return errors.New("room member id is required")
		}
		key := string(memberType) + "\x00" + memberID
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		members = append(members, RoomMember{MemberType: memberType, MemberID: memberID})
		return nil
	}
	if err := add(MemberHuman, createdBy); err != nil {
		return nil, err
	}
	for _, member := range input {
		if err := add(member.MemberType, member.MemberID); err != nil {
			return nil, err
		}
	}
	if len(members) > MaxRoomMembers {
		return nil, fmt.Errorf("room member limit is %d", MaxRoomMembers)
	}
	return members, nil
}

func roomAgentCount(members []RoomMember) int {
	count := 0
	for _, member := range members {
		if member.MemberType == MemberAgent {
			count++
		}
	}
	return count
}

func randomID(prefix string, byteCount int) (string, error) {
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(buffer), nil
}

func randomNamedAgentAvatarKey() (string, error) {
	var value [1]byte
	for {
		if _, err := rand.Read(value[:]); err != nil {
			return "", fmt.Errorf("generate named agent avatar: %w", err)
		}
		if value[0] < 252 {
			return fmt.Sprintf("abstract-%d", int(value[0])%namedAgentAvatarCount+1), nil
		}
	}
}

func normalizeNamedAgentAvatarKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for index := 1; index <= namedAgentAvatarCount; index++ {
		if value == fmt.Sprintf("abstract-%d", index) {
			return value, nil
		}
	}
	parts := strings.Split(value, ":")
	if len(parts) == 4 && parts[0] == "mascot-v1" && validNamedAgentAvatarShape(parts[1]) && validNamedAgentAvatarAccessory(parts[2]) {
		hue, err := strconv.Atoi(parts[3])
		if err == nil && hue >= 0 && hue <= 359 && parts[3] == strconv.Itoa(hue) {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid named agent avatar %q", value)
}

func validNamedAgentAvatarShape(value string) bool {
	switch value {
	case "round", "organic", "boxy", "nub", "cloud", "sun":
		return true
	default:
		return false
	}
}

func validNamedAgentAvatarAccessory(value string) bool {
	switch value {
	case "none", "cap", "beanie", "top-hat", "sprout", "crown", "headphones", "scarf", "beret", "party-hat", "wizard-hat", "chef-hat", "flower", "halo", "bow-tie", "graduation-cap", "cowboy-hat", "propeller-cap", "mushroom-cap", "bunny-ears", "cat-ears", "ribbon", "necktie":
		return true
	default:
		return false
	}
}

func normalizeNamedAgentAvatarImage(value string) (string, error) {
	return normalizeAvatarImage(value, "named agent avatar")
}

func normalizeAvatarImage(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/webp;base64") {
		return "", fmt.Errorf("%s image must be PNG, JPEG, or WebP", label)
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxAgentAvatarBytes) {
		return "", fmt.Errorf("%s image exceeds %d bytes", label, maxAgentAvatarBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%s image is not valid base64 data", label)
	}
	if len(decoded) == 0 || len(decoded) > maxAgentAvatarBytes {
		return "", fmt.Errorf("%s image must be between 1 and %d bytes", label, maxAgentAvatarBytes)
	}
	validImage := (header == "data:image/png;base64" && bytes.HasPrefix(decoded, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})) ||
		(header == "data:image/jpeg;base64" && bytes.HasPrefix(decoded, []byte{0xff, 0xd8, 0xff})) ||
		(header == "data:image/webp;base64" && len(decoded) >= 12 && string(decoded[:4]) == "RIFF" && string(decoded[8:12]) == "WEBP")
	if !validImage {
		return "", fmt.Errorf("%s image data does not match its media type", label)
	}
	return value, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func toMillis(value time.Time) int64 {
	return value.UTC().UnixMilli()
}

func fromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func encodeMentions(mentions []string) (string, error) {
	data, err := json.Marshal(mentions)
	if err != nil {
		return "", fmt.Errorf("encode message mentions: %w", err)
	}
	return string(data), nil
}
