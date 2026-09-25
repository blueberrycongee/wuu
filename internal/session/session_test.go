package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCreateAndList(t *testing.T) {
	dir := t.TempDir()
	s1, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID == s2.ID {
		t.Fatalf("expected unique IDs, got %q twice", s1.ID)
	}

	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestHistoryContentPartsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "thread-content-parts", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := json.RawMessage(`[{"type":"pasted_text","text":"pasted body\n"},{"type":"text","text":"question"}]`)
	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{
		Role:         "user",
		Content:      "pasted body\nquestion",
		ContentParts: want,
	}); err != nil {
		t.Fatal(err)
	}

	records, err := LoadHistoryRecords(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("history record count = %d, want 1", len(records))
	}
	if string(records[0].ContentParts) != string(want) {
		t.Fatalf("ContentParts = %s, want %s", records[0].ContentParts, want)
	}
}

func TestSQLiteDSNNormalizesPaths(t *testing.T) {
	const suffix = "?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29&_txlock=immediate"

	t.Run("unix absolute", func(t *testing.T) {
		got := sqliteDSN("/Users/name/.wuu/sessions/sessions.sqlite3")
		want := "file:///Users/name/.wuu/sessions/sessions.sqlite3" + suffix
		if got != want {
			t.Fatalf("sqliteDSN() = %q, want %q", got, want)
		}
	})

	t.Run("windows drive absolute", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("filepath.ToSlash only normalizes Windows separators on Windows")
		}
		got := sqliteDSN(`C:\Users\name\.wuu\sessions\sessions.sqlite3`)
		want := "file:///C:/Users/name/.wuu/sessions/sessions.sqlite3" + suffix
		if got != want {
			t.Fatalf("sqliteDSN() = %q, want %q", got, want)
		}
	})

	t.Run("url parse round trip", func(t *testing.T) {
		// Whatever platform we are on, url.Parse must succeed and the path
		// component must not be reinterpreted as authority — this is the exact
		// failure that triggered "invalid uri authority" in production.
		dsn := sqliteDSN(`C:\Users\wsmdm\.wuu\sessions\sessions.sqlite3`)
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", dsn, err)
		}
		if parsed.Host != "" {
			t.Fatalf("DSN %q has non-empty host/authority %q", dsn, parsed.Host)
		}
	})
}

func TestSetRuntimeSelectionPersists(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-model", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, "thread-model", RuntimeSelection{
		Provider:       "kimi",
		Model:          "k3",
		Variant:        "high",
		Effort:         "xhigh",
		PermissionMode: "read_only",
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Provider != "kimi" || sessions[0].Model != "k3" ||
		sessions[0].Variant != "high" || sessions[0].Effort != "xhigh" || sessions[0].PermissionMode != "read_only" {
		t.Fatalf("runtime selection was not persisted: %+v", sessions)
	}
}

func TestListForCWDFiltersSessions(t *testing.T) {
	dir := t.TempDir()
	cwdA := filepath.Join(t.TempDir(), "project-a")
	cwdB := filepath.Join(t.TempDir(), "project-b")

	if _, err := CreateWithMetadata(dir, "sess-a", cwdA); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWithMetadata(dir, "sess-b", cwdB); err != nil {
		t.Fatal(err)
	}

	sessions, err := ListForCWD(dir, cwdA, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "sess-a" {
		t.Fatalf("unexpected scoped sessions: %+v", sessions)
	}

	recent, err := MostRecentForCWD(dir, cwdB, "")
	if err != nil {
		t.Fatal(err)
	}
	if recent != "sess-b" {
		t.Fatalf("MostRecentForCWD() = %q, want sess-b", recent)
	}
}

func TestListForCWDMatchesByWorkspaceIDAcrossMoves(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(t.TempDir(), "old")
	newPath := filepath.Join(t.TempDir(), "new")

	// A project session recorded at the OLD path, bound to a stable id.
	proj, err := CreateWithMetadata(dir, "sess-proj", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetWorkspaceID(dir, proj.ID, "ws-1"); err != nil {
		t.Fatal(err)
	}

	// Listing by the workspace id + the NEW path (post-move) still finds it,
	// even though its recorded cwd is the old path.
	got, err := ListForCWD(dir, newPath, "ws-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "sess-proj" {
		t.Fatalf("id-keyed session should survive a move; got %+v", got)
	}

	// A session with no workspace id but the workspace's current cwd still
	// matches (graceful transition for sessions predating the id).
	if _, err := CreateWithMetadata(dir, "sess-legacy", newPath); err != nil {
		t.Fatal(err)
	}
	got, err = ListForCWD(dir, newPath, "ws-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected id match + legacy-cwd match, got %d: %+v", len(got), got)
	}

	// A session of a DIFFERENT workspace at the same cwd is excluded.
	other, err := CreateWithMetadata(dir, "sess-other", newPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetWorkspaceID(dir, other.ID, "ws-2"); err != nil {
		t.Fatal(err)
	}
	got, err = ListForCWD(dir, newPath, "ws-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.ID == "sess-other" {
			t.Fatalf("ws-2 session leaked into the ws-1 listing: %+v", got)
		}
	}
}

func TestSetSourcePersistsAndLists(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "automation-thread", "/tmp/project"); err != nil {
		t.Fatalf("CreateWithMetadata: %v", err)
	}
	if _, err := SetSource(dir, "automation-thread", "automation"); err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	found, ok, err := Find(dir, "automation-thread")
	if err != nil || !ok || found.Source != "automation" {
		t.Fatalf("Find() = %+v, %t, %v", found, ok, err)
	}
	sessions, err := List(dir, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Source != "automation" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestHistoryRecordToolResultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-tool-result", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	rich := json.RawMessage(`{"content":[{"type":"image","data":"aW1hZ2U=","mime_type":"image/png","name":"screen.png"}],"structured_content":{"caption":"result"},"meta":{"source":"mcp"}}`)
	if err := AppendHistoryRecord(dir, "thread-tool-result", HistoryRecord{
		Role:       "tool",
		Content:    "[image: screen.png (image/png)]",
		ToolCallID: "call-rich",
		ToolResult: rich,
	}); err != nil {
		t.Fatal(err)
	}
	history, err := LoadHistoryRecords(dir, "thread-tool-result", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || string(history[0].ToolResult) != string(rich) {
		t.Fatalf("loaded tool result = %+v, want %s", history, rich)
	}
}

func TestCreateForkWithMetadataPersistsSource(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	fork, err := CreateForkWithMetadata(dir, "forked", cwd, ForkMetadata{
		ForkedFromID:     "source",
		ForkedFromTurnID: "source-turn-0001",
		ForkedFromItemID: "source-turn-0001-item-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fork.ForkedFromID != "source" || fork.ForkedFromTurnID != "source-turn-0001" || fork.ForkedFromItemID != "source-turn-0001-item-2" {
		t.Fatalf("fork metadata not set on create: %+v", fork)
	}

	found, ok, err := Find(dir, "forked")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected fork in index")
	}
	if found.ForkedFromID != "source" || found.ForkedFromTurnID != "source-turn-0001" || found.ForkedFromItemID != "source-turn-0001-item-2" {
		t.Fatalf("fork metadata not persisted: %+v", found)
	}
}

func TestCreateWithWorktreePersistsBinding(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(t.TempDir(), "project")
	wtPath := filepath.Join(t.TempDir(), "worktree")
	fork, err := CreateWithWorktree(dir, "forked-worktree", wtPath, ForkMetadata{
		ForkedFromID: "source",
	}, WorktreeInfo{
		Path:     wtPath,
		BaseHEAD: "abc123",
		BaseRepo: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fork.CWD != wtPath || fork.WorktreePath != wtPath || fork.WorktreeBaseHEAD != "abc123" || fork.WorktreeBaseRepo != parent {
		t.Fatalf("worktree binding not set on create: %+v", fork)
	}

	found, ok, err := Find(dir, "forked-worktree")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected fork in index")
	}
	info, bound := found.WorktreeInfo()
	if !bound {
		t.Fatalf("expected worktree binding: %+v", found)
	}
	if info.Path != wtPath || info.BaseHEAD != "abc123" || info.BaseRepo != parent {
		t.Fatalf("unexpected worktree info: %+v", info)
	}
}

func TestBindWorktreeUpdatesExistingSession(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(t.TempDir(), "project")
	wtPath := filepath.Join(t.TempDir(), "worktree")
	if _, err := CreateWithMetadata(dir, "thread-1", parent); err != nil {
		t.Fatal(err)
	}
	updated, err := BindWorktree(dir, "thread-1", WorktreeInfo{
		Path:     wtPath,
		BaseHEAD: "def456",
		BaseRepo: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CWD != parent {
		t.Fatalf("BindWorktree should not rewrite an existing cwd, got %+v", updated)
	}

	info, bound, err := WorktreeInfoForSession(dir, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bound || info.Path != wtPath || info.BaseHEAD != "def456" || info.BaseRepo != parent {
		t.Fatalf("unexpected persisted worktree info: bound=%v info=%+v", bound, info)
	}
}

func TestListForCWDIncludesWorktreeBaseRepo(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(t.TempDir(), "project")
	wtPath := filepath.Join(t.TempDir(), "worktree")
	if _, err := CreateWithWorktree(dir, "forked-worktree", wtPath, ForkMetadata{}, WorktreeInfo{
		Path:     wtPath,
		BaseHEAD: "abc123",
		BaseRepo: parent,
	}); err != nil {
		t.Fatal(err)
	}

	sessions, err := ListForCWD(dir, parent, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "forked-worktree" {
		t.Fatalf("expected worktree session under parent repo, got %+v", sessions)
	}
}

func TestUpdateIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	setSessionUpdatedAt(t, dir, s.ID, time.Time{})
	if err := UpdateIndex(dir, s.ID, 42, "hello"); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Entries != 42 || sessions[0].Summary != "hello" {
		t.Fatalf("update not persisted: %+v", sessions)
	}
	if sessions[0].UpdatedAt.IsZero() {
		t.Fatalf("expected updated timestamp: %+v", sessions[0])
	}
}

func TestUpdateGeneratedTitle(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateGeneratedTitle(dir, s.ID, "Short title")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Short title" {
		t.Fatalf("title not returned: %+v", updated)
	}

	if _, err := UpdateGeneratedTitle(dir, s.ID, "Replacement"); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "Short title" {
		t.Fatalf("title should persist once: %+v", sessions)
	}
}

func TestUpdateTitle(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateTitle(dir, s.ID, "  "); err == nil {
		t.Fatal("expected error for empty title")
	}
	updated, err := UpdateTitle(dir, s.ID, "Custom rename")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Custom rename" {
		t.Fatalf("title not persisted: %+v", updated)
	}

	// UpdateTitle unconditionally overwrites — unlike UpdateGeneratedTitle,
	// which only fills an empty title. The right-click Rename menu relies
	// on this to replace both an auto-generated preview and any prior
	// user-edited title.
	if _, err := UpdateTitle(dir, s.ID, "Second rename"); err != nil {
		t.Fatal(err)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "Second rename" {
		t.Fatalf("second rename should overwrite: %+v", sessions)
	}
}

func TestListOrdersPinnedGroupsByActivity(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	older, err := CreateWithMetadata(dir, "older", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := CreateWithMetadata(dir, "newer", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	setSessionUpdatedAt(t, dir, older.ID, base)
	setSessionUpdatedAt(t, dir, newer.ID, base.Add(time.Hour))

	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != newer.ID || sessions[1].ID != older.ID {
		t.Fatalf("expected active sessions by updated_at desc, got %+v", sessions)
	}

	if _, err := UpdatePinned(dir, older.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdatePinned(dir, newer.ID, true); err != nil {
		t.Fatal(err)
	}
	sessions, err = List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != newer.ID || sessions[1].ID != older.ID {
		t.Fatalf("expected pinned sessions by updated_at desc, got %+v", sessions)
	}
}

func TestPinAndArchiveMetadata(t *testing.T) {
	dir := t.TempDir()
	first, err := CreateWithMetadata(dir, "first", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateWithMetadata(dir, "second", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}

	pinned, err := UpdatePinned(dir, first.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.PinnedAt == nil {
		t.Fatalf("expected pinned timestamp: %+v", pinned)
	}
	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != first.ID || sessions[1].ID != second.ID {
		t.Fatalf("expected pinned session first, got %+v", sessions)
	}

	archived, err := UpdateArchived(dir, first.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || archived.PinnedAt != nil {
		t.Fatalf("expected archived session to clear pin: %+v", archived)
	}

	found, ok, err := Find(dir, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.ArchivedAt == nil {
		t.Fatalf("expected archived metadata from Find, got ok=%v session=%+v", ok, found)
	}
}

func TestDeleteRemovesSessionAndHistory(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "thread-1", "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{Role: "user", Content: "delete me"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := Delete(dir, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ID != sess.ID || deleted.CWD != "/tmp/project" {
		t.Fatalf("unexpected deleted metadata: %+v", deleted)
	}
	if _, ok, err := Find(dir, sess.ID); err != nil || ok {
		t.Fatalf("deleted session should not be found, ok=%v err=%v", ok, err)
	}
	if _, err := LoadHistoryRecords(dir, sess.ID, true); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("LoadHistoryRecords() error = %v, want ErrSessionNotFound", err)
	}
	if _, err := Delete(dir, sess.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Delete() error = %v, want ErrSessionNotFound", err)
	}
}

func TestHistoryRecordsPersistInSQLite(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}

	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{
		Role:           "assistant",
		Content:        "done",
		DisplayContent: "visible done",
		Phase:          "final_answer",
		Hidden:         true,
		FinishReason:   "length",
		StopReason:     "context_length_exceeded",
		Truncated:      true,
		ToolCalls:      json.RawMessage(`[{"id":"call_1","name":"read_file","arguments":"{}"}]`),
		DiscoveredTools: json.RawMessage(
			`[{"type":"function","name":"mcp_docs_search","input_schema":{"type":"object"}}]`,
		),
		At: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{
		Role:                "meta",
		Content:             "token_usage",
		InputTokens:         12,
		OutputTokens:        4,
		ContextTokens:       14,
		CacheCreationTokens: 6,
		CacheReadTokens:     2,
	}); err != nil {
		t.Fatal(err)
	}

	visible, err := LoadHistoryRecords(dir, "thread-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].Role != "assistant" || visible[0].Content != "done" || visible[0].DisplayContent != "visible done" || visible[0].Phase != "final_answer" || !visible[0].Hidden || visible[0].FinishReason != "length" || visible[0].StopReason != "context_length_exceeded" || !visible[0].Truncated || string(visible[0].ToolCalls) == "" || string(visible[0].DiscoveredTools) == "" {
		t.Fatalf("unexpected visible history: %+v", visible)
	}

	all, err := LoadHistoryRecords(dir, "thread-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].Role != "meta" || all[1].InputTokens != 12 || all[1].OutputTokens != 4 || all[1].ContextTokens != 14 || all[1].CacheCreationTokens != 6 || all[1].CacheReadTokens != 2 {
		t.Fatalf("unexpected full history: %+v", all)
	}
}

func TestMigrateLegacySessionMessagesRemainsReadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(DBPath(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if err := configureDB(db); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE sessions (
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
			archived_at TEXT,
			worktree_path TEXT NOT NULL DEFAULT '',
			worktree_base_head TEXT NOT NULL DEFAULT '',
			worktree_base_repo TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE session_messages (
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			display_content TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			provider_item_id TEXT NOT NULL DEFAULT '',
			provider_item_model TEXT NOT NULL DEFAULT '',
			client_id TEXT NOT NULL DEFAULT '',
			hidden INTEGER NOT NULL DEFAULT 0,
			steered INTEGER NOT NULL DEFAULT 0,
			reasoning_content TEXT NOT NULL DEFAULT '',
			reasoning_blocks_json TEXT NOT NULL DEFAULT '',
			images_json TEXT NOT NULL DEFAULT '',
			files_json TEXT NOT NULL DEFAULT '',
			tool_calls_json TEXT NOT NULL DEFAULT '',
			discovered_tools_json TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT NOT NULL DEFAULT '',
			tool_result_kind TEXT NOT NULL DEFAULT '',
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
		`INSERT INTO sessions (id, created_at, updated_at, cwd) VALUES ('thread-1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '/tmp/project')`,
		`INSERT INTO session_messages (session_id, seq, role, content) VALUES ('thread-1', 1, 'user', 'hello')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	history, err := LoadHistoryRecords(dir, "thread-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("unexpected migrated history: %+v", history)
	}
}

func TestHistoryRecordOperationsRequireExistingSession(t *testing.T) {
	dir := t.TempDir()
	rec := HistoryRecord{Role: "user", Content: "hello"}

	if err := AppendHistoryRecord(dir, "missing-thread", rec); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AppendHistoryRecord() error = %v, want ErrSessionNotFound", err)
	}
	if err := RewriteHistoryRecords(dir, "missing-thread", []HistoryRecord{rec}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("RewriteHistoryRecords() error = %v, want ErrSessionNotFound", err)
	}
	if err := RewriteHistoryRecordsAtBaseline(dir, "missing-thread", []HistoryRecord{rec}, 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("RewriteHistoryRecordsAtBaseline() error = %v, want ErrSessionNotFound", err)
	}
	if _, err := LoadHistoryRecords(dir, "missing-thread", false); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("LoadHistoryRecords() error = %v, want ErrSessionNotFound", err)
	}
}

func TestRewriteHistoryRecordsReplacesExistingMessages(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "user", Content: "old user"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "assistant", Content: "old assistant"}); err != nil {
		t.Fatal(err)
	}

	if err := RewriteHistoryRecords(dir, "thread-1", []HistoryRecord{{Role: "assistant", Content: "new assistant"}}); err != nil {
		t.Fatal(err)
	}

	history, err := LoadHistoryRecords(dir, "thread-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Role != "assistant" || history[0].Content != "new assistant" {
		t.Fatalf("rewrite should replace old history, got %+v", history)
	}
}

// TestConcurrentCreateAndUpdate exercises concurrent short writes against the
// SQLite store. Creates and metadata updates must not lose sessions.
func TestConcurrentCreateAndUpdate(t *testing.T) {
	dir := t.TempDir()

	// Seed with one session that UpdateIndex will target.
	seed, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}

	const newSessions = 50
	const updates = 50
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < newSessions; i++ {
			if _, err := Create(dir); err != nil {
				t.Errorf("Create: %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < updates; i++ {
			if err := UpdateIndex(dir, seed.ID, i, ""); err != nil {
				t.Errorf("UpdateIndex: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	sessions, err := List(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := newSessions + 1 // seed + creates
	if len(sessions) != expected {
		t.Fatalf("expected %d sessions after concurrent work, got %d", expected, len(sessions))
	}

	// Verify no duplicate IDs (a symptom of torn writes).
	seen := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if seen[s.ID] {
			t.Errorf("duplicate session ID in index: %s", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestConcurrentHistoryAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}

	const appends = 64
	const readers = 4
	start := make(chan struct{})
	done := make(chan struct{})
	errs := make(chan error, appends+readers)

	var readerWG sync.WaitGroup
	readerWG.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer readerWG.Done()
			<-start
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, err := LoadHistoryRecords(dir, "thread-1", false); err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	var writerWG sync.WaitGroup
	writerWG.Add(appends)
	for i := 0; i < appends; i++ {
		i := i
		go func() {
			defer writerWG.Done()
			<-start
			if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{
				Role:    "user",
				Content: "message-" + strconv.Itoa(i),
			}); err != nil {
				errs <- err
			}
		}()
	}

	close(start)
	writerWG.Wait()
	close(done)
	readerWG.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	history, err := LoadHistoryRecords(dir, "thread-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != appends {
		t.Fatalf("expected %d appended records, got %d: %+v", appends, len(history), history)
	}
	seen := make(map[string]bool, appends)
	for _, rec := range history {
		if rec.Role != "user" {
			t.Fatalf("unexpected record role after concurrent append: %+v", rec)
		}
		if seen[rec.Content] {
			t.Fatalf("duplicate history content after concurrent append: %q", rec.Content)
		}
		seen[rec.Content] = true
	}
	for i := 0; i < appends; i++ {
		content := "message-" + strconv.Itoa(i)
		if !seen[content] {
			t.Fatalf("missing history content after concurrent append: %q", content)
		}
	}
}

func TestConcurrentHistoryRewriteAndAppend(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "user", Content: "seed"}); err != nil {
		t.Fatal(err)
	}

	const appends = 32
	start := make(chan struct{})
	errs := make(chan error, appends+1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := RewriteHistoryRecords(dir, "thread-1", []HistoryRecord{{
			Role:    "assistant",
			Content: "rewritten",
		}}); err != nil {
			errs <- err
		}
	}()

	wg.Add(appends)
	for i := 0; i < appends; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{
				Role:    "user",
				Content: "append-" + strconv.Itoa(i),
			}); err != nil {
				errs <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	history, err := LoadHistoryRecords(dir, "thread-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 1 || len(history) > appends+1 {
		t.Fatalf("unexpected history length after concurrent rewrite/append: %d %+v", len(history), history)
	}
	var rewritten int
	seen := make(map[string]bool, len(history))
	for _, rec := range history {
		if rec.Content == "seed" {
			t.Fatalf("seed record should not survive rewrite: %+v", history)
		}
		if rec.Content == "rewritten" {
			rewritten++
			continue
		}
		if seen[rec.Content] {
			t.Fatalf("duplicate appended content after concurrent rewrite/append: %q", rec.Content)
		}
		seen[rec.Content] = true
	}
	if rewritten != 1 {
		t.Fatalf("expected one rewritten record, got %d in %+v", rewritten, history)
	}
}

func TestAppendHistoryProjectsSettledToolInvocationAtomically(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "thread-tool-projection", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := db.Exec(`
INSERT INTO tool_batches (id, owner_id, operation_id, step_index, status, created_at, updated_at, terminal_at)
VALUES ('batch-1', ?, 'operation-1', 1, 'settled', ?, ?, ?)`, sess.ID, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO tool_invocations (
  id, batch_id, provider_call_id, tool_name, arguments_json, replay_policy, state,
  result_json, prepared_at, running_at, settled_at
) VALUES ('invocation-1', 'batch-1', 'call-1', 'read_file', '{}', 'at_most_once', 'succeeded', '{}', ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := AppendHistoryRecord(dir, sess.ID, HistoryRecord{
		Role: "tool", ToolCallID: "call-1", ToolInvocationID: "invocation-1", Content: "done",
	}); err != nil {
		t.Fatal(err)
	}
	db, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var projectedAt int64
	var batchState string
	if err := db.QueryRow(`SELECT projected_at FROM tool_invocations WHERE id = 'invocation-1'`).Scan(&projectedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM tool_batches WHERE id = 'batch-1'`).Scan(&batchState); err != nil {
		t.Fatal(err)
	}
	if projectedAt == 0 || batchState != "projected" {
		t.Fatalf("projection state = %d/%q", projectedAt, batchState)
	}
	records, err := LoadHistoryRecords(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ToolInvocationID != "invocation-1" {
		t.Fatalf("history records = %+v", records)
	}
}

func TestAppendHistoryRollsBackUnknownToolProjection(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "thread-tool-rollback", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = AppendHistoryRecord(dir, sess.ID, HistoryRecord{
		Role: "tool", ToolCallID: "call-missing", ToolInvocationID: "invocation-missing", Content: "missing",
	})
	if err == nil {
		t.Fatal("unknown tool invocation was projected")
	}
	records, loadErr := LoadHistoryRecords(dir, sess.ID, false)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(records) != 0 {
		t.Fatalf("history append was not rolled back: %+v", records)
	}
}

func TestAppendHistoryRecordsRollsBackWholeToolSegment(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "thread-tool-segment-rollback", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = AppendHistoryRecords(dir, sess.ID, []HistoryRecord{
		{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call-missing","name":"read_file","arguments":"{}"}]`)},
		{Role: "tool", ToolCallID: "call-missing", ToolInvocationID: "invocation-missing", Content: "missing"},
	})
	if err == nil {
		t.Fatal("unknown tool invocation was projected")
	}
	records, loadErr := LoadHistoryRecords(dir, sess.ID, false)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(records) != 0 {
		t.Fatalf("partial tool segment was persisted: %+v", records)
	}
}

func setSessionUpdatedAt(t *testing.T, dir, id string, at time.Time) {
	t.Helper()
	if _, err := updateMetadata(dir, id, false, func(s *Session) {
		s.UpdatedAt = at
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSetRuntimeSelectionAllowsEmptyModelForProtocolEngines(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-protocol", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEngine(dir, "thread-protocol", "cursor"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, "thread-protocol", RuntimeSelection{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, "thread-protocol", RuntimeSelection{Provider: "cursor", Model: ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWithMetadata(dir, "thread-wuu", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRuntimeSelection(dir, "thread-wuu", RuntimeSelection{Provider: "kimi"}); err == nil {
		t.Fatal("wuu sessions still require a model")
	}
}
