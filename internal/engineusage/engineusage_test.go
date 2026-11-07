package engineusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func writeFile(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func claudeAssistantLine(t *testing.T, sessionID, model string, input, output, cacheRead, cacheWrite int) string {
	t.Helper()
	line := map[string]any{
		"type":      "assistant",
		"timestamp": "2026-11-06T13:35:10.975Z",
		"sessionId": sessionID,
		"message": map[string]any{
			"role":  "assistant",
			"model": model,
			"usage": map[string]any{
				"input_tokens":                input,
				"output_tokens":               output,
				"cache_read_input_tokens":     cacheRead,
				"cache_creation_input_tokens": cacheWrite,
			},
		},
	}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func codexTokenCountLine(t *testing.T, input, cached, cacheWrite, output int) string {
	t.Helper()
	line := map[string]any{
		"timestamp": "2026-10-30T12:38:00.712Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":             input,
					"cached_input_tokens":      cached,
					"cache_write_input_tokens": cacheWrite,
					"output_tokens":            output,
					"total_tokens":             input + output,
				},
			},
		},
	}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func codexSessionMetaLine(t *testing.T, id string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":    "session_meta",
		"payload": map[string]any{"id": id, "cwd": "/tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func totals(rows []Row) (tokens, sessions int) {
	seen := make(map[string]struct{})
	for _, r := range rows {
		tokens += r.Total()
		seen[r.SessionID] = struct{}{}
	}
	return tokens, len(seen)
}

func TestScanClaude(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".claude", "projects")
	writeFile(t, filepath.Join(projects, "-tmp-a", "sess-a.jsonl"), []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		claudeAssistantLine(t, "sess-a", "claude-sonnet-4-5", 100, 20, 30, 10),
		claudeAssistantLine(t, "sess-a", "claude-sonnet-4-5", 50, 10, 0, 0),
		`not json at all`,
	})
	// A Wuu-bound session file must be excluded entirely.
	writeFile(t, filepath.Join(projects, "-tmp-b", "sess-bound.jsonl"), []string{
		claudeAssistantLine(t, "sess-bound", "claude-sonnet-4-5", 999, 999, 0, 0),
	})
	writeFile(t, filepath.Join(projects, "-tmp-a", "notes.txt"), []string{"ignore me"})

	s := &Scanner{ClaudeDir: projects, CodexDirs: []string{filepath.Join(root, "none")}, OpenCodeDB: filepath.Join(root, "none.db")}
	res := s.Scan(context.Background(), ExcludeRefs{"claude": {"sess-bound": {}}})
	if res.Failed["claude"] != nil {
		t.Fatalf("claude scan failed: %v", res.Failed["claude"])
	}
	tokens, sessions := totals(res.Rows)
	if tokens != 100+20+30+10+50+10 || sessions != 1 {
		t.Fatalf("unexpected totals: tokens=%d sessions=%d rows=%v", tokens, sessions, res.Rows)
	}
	var found Row
	for _, r := range res.Rows {
		if r.SessionID == "sess-a" {
			found = r
			break
		}
	}
	if found.Engine != "claude" || found.Model != "claude-sonnet-4-5" || found.At.IsZero() {
		t.Fatalf("unexpected row: %+v", found)
	}
}

func TestScanCodex(t *testing.T) {
	root := t.TempDir()
	codexSessions := filepath.Join(root, ".codex", "sessions")
	boundID := "01a18cd0-55aa-70e1-88c8-06f292e18d96"
	writeFile(t, filepath.Join(codexSessions, "2026", "10", "30", "rollout-2026-10-30T20-37-06-"+boundID+".jsonl"), []string{
		codexSessionMetaLine(t, boundID),
		codexTokenCountLine(t, 999, 0, 0, 999),
	})
	freeID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeFile(t, filepath.Join(codexSessions, "2026", "11", "01", "rollout-2026-11-01T10-00-00-"+freeID+".jsonl"), []string{
		codexSessionMetaLine(t, freeID),
		codexTokenCountLine(t, 100, 30, 10, 20),
		codexTokenCountLine(t, 50, 0, 0, 5),
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
	})

	s := &Scanner{ClaudeDir: filepath.Join(root, "none"), CodexDirs: []string{codexSessions}, OpenCodeDB: filepath.Join(root, "none.db")}
	res := s.Scan(context.Background(), ExcludeRefs{"codex": {boundID: {}}})
	if res.Failed["codex"] != nil {
		t.Fatalf("codex scan failed: %v", res.Failed["codex"])
	}
	tokens, sessions := totals(res.Rows)
	// (100-30-10)+30+10+20 = 120, then (50-0-0)+0+0+5 = 55
	if tokens != 175 || sessions != 1 {
		t.Fatalf("unexpected totals: tokens=%d sessions=%d rows=%v", tokens, sessions, res.Rows)
	}
	for _, r := range res.Rows {
		if r.SessionID == boundID {
			t.Fatalf("bound session leaked into outside rows: %+v", r)
		}
	}
}

func TestScanOpenCode(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE part (id TEXT, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert := func(sessionID, partType string, createdMS int64, data string) {
		if _, err := db.Exec(`INSERT INTO part VALUES (?, 'm', ?, ?, ?, ?)`, "p"+sessionID+partType, sessionID, createdMS, createdMS, data); err != nil {
			t.Fatal(err)
		}
	}
	stepFinish := func(input, output, reasoning, cacheRead, cacheWrite int) string {
		data, err := json.Marshal(map[string]any{
			"type":   "step-finish",
			"tokens": map[string]any{"input": input, "output": output, "reasoning": reasoning, "cache": map[string]any{"read": cacheRead, "write": cacheWrite}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	insert("ses_free", "step-finish", 1780382479644, stepFinish(100, 20, 5, 30, 10))
	insert("ses_free", "text", 1780382479645, `{"type":"text"}`)
	insert("ses_bound", "step-finish", 1780382479646, stepFinish(999, 999, 0, 0, 0))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Scanner{ClaudeDir: filepath.Join(root, "none"), CodexDirs: []string{filepath.Join(root, "none")}, OpenCodeDB: dbPath}
	res := s.Scan(context.Background(), ExcludeRefs{"opencode": {"ses_bound": {}}})
	if res.Failed["opencode"] != nil {
		t.Fatalf("opencode scan failed: %v", res.Failed["opencode"])
	}
	tokens, sessions := totals(res.Rows)
	if tokens != 100+20+5+30+10 || sessions != 1 {
		t.Fatalf("unexpected totals: tokens=%d sessions=%d rows=%v", tokens, sessions, res.Rows)
	}
}

func TestScanFileCacheReuse(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".claude", "projects")
	path := filepath.Join(projects, "-tmp", "sess-a.jsonl")
	writeFile(t, path, []string{claudeAssistantLine(t, "sess-a", "m", 1, 2, 3, 4)})
	s := &Scanner{ClaudeDir: projects, CodexDirs: []string{filepath.Join(root, "none")}, OpenCodeDB: filepath.Join(root, "none.db")}
	first := s.Scan(context.Background(), nil)
	if len(first.Rows) != 1 {
		t.Fatalf("first scan rows=%v", first.Rows)
	}
	if len(s.files) != 1 {
		t.Fatalf("file cache not populated: %v", s.files)
	}
	second := s.Scan(context.Background(), nil)
	if len(second.Rows) != 1 || second.Rows[0].InputTokens != 1 {
		t.Fatalf("cached scan rows=%v", second.Rows)
	}
	// A changed file must re-parse rather than reuse stale rows.
	time.Sleep(10 * time.Millisecond)
	writeFile(t, path, []string{claudeAssistantLine(t, "sess-a", "m", 9, 9, 9, 9)})
	third := s.Scan(context.Background(), nil)
	if len(third.Rows) != 1 || third.Rows[0].InputTokens != 9 {
		t.Fatalf("stale cache after rewrite: %v", third.Rows)
	}
}

func TestScanMissingLedgers(t *testing.T) {
	root := t.TempDir()
	s := &Scanner{
		ClaudeDir:  filepath.Join(root, "missing-claude"),
		CodexDirs:  []string{filepath.Join(root, "missing-codex")},
		OpenCodeDB: filepath.Join(root, "missing.db"),
	}
	res := s.Scan(context.Background(), nil)
	if len(res.Rows) != 0 || len(res.Failed) != 0 {
		t.Fatalf("missing ledgers should be empty, not failed: %+v", res)
	}
}
