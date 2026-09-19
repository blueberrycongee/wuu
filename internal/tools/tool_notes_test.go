package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
)

func callNotes(t *testing.T, session string, input map[string]any) (map[string]any, error) {
	t.Helper()
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the tool on every call to exercise restart persistence.
	result, err := NewNotesTool(&Env{SessionID: session}).Execute(context.Background(), string(arguments))
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatal(err)
	}
	return output, nil
}

func TestNotesRecoverAcrossToolsAndIsolateSessions(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	listed, err := callNotes(t, "one", map[string]any{"action": "list"})
	if err != nil || listed["total"] != float64(0) || listed["revision"] != "" {
		t.Fatalf("list=%v err=%v", listed, err)
	}
	written, err := callNotes(t, "one", map[string]any{"action": "write", "path": "work/current.md", "content": "目标：修复恢复\nSeq 18", "revision": listed["revision"]})
	if err != nil {
		t.Fatal(err)
	}
	read, err := callNotes(t, "one", map[string]any{"action": "read", "path": "work/current.md", "offset": 3, "limit": 4})
	if err != nil || read["content"] != "修复恢复" || read["next_offset"] != float64(7) {
		t.Fatalf("read=%v err=%v", read, err)
	}
	other, err := callNotes(t, "two", map[string]any{"action": "list"})
	if err != nil || other["total"] != float64(0) {
		t.Fatalf("session leaked: %v %v", other, err)
	}
	if _, err := callNotes(t, "one", map[string]any{"action": "append", "path": "work/current.md", "content": " stale", "revision": ""}); err == nil {
		t.Fatal("stale update accepted")
	}
	updated, err := callNotes(t, "one", map[string]any{"action": "append", "path": "work/current.md", "content": "\nverified", "revision": written["revision"]})
	if err != nil || updated["revision"] == written["revision"] {
		t.Fatalf("append=%v %v", updated, err)
	}
	if _, err := callNotes(t, "one", map[string]any{"action": "read", "path": "work/current.md", "offset": 7, "revision": written["revision"]}); err == nil {
		t.Fatal("stale pagination accepted")
	}
}

func TestNotesSearchAndReadBoundedPages(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	_, err := callNotes(t, "one", map[string]any{"action": "write", "path": "work.md", "content": strings.Repeat("中<&", 30000), "revision": ""})
	if err != nil {
		t.Fatal(err)
	}
	page, err := callNotes(t, "one", map[string]any{"action": "search", "query": "<&", "offset": 29998, "limit": 2})
	if err != nil || page["total"] != float64(30000) {
		t.Fatalf("search=%v %v", page, err)
	}
	items := page["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["offset"] != float64(29998*3+1) {
		t.Fatalf("items=%v", items)
	}
	for _, input := range []map[string]any{
		{"action": "search", "query": "<&", "limit": 100},
		{"action": "read", "path": "work.md", "limit": 16000},
	} {
		page, err = callNotes(t, "one", input)
		encoded, _ := json.Marshal(page)
		if err != nil || len(encoded) > 18000 || page["next_offset"] == nil {
			t.Fatalf("unbounded page: %d %v", len(encoded), err)
		}
	}
}

func TestNotesRejectInvalidAndOversizedWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	for _, input := range []map[string]any{
		{"action": "write", "path": "../x", "content": "x", "revision": ""},
		{"action": "write", "path": "x", "content": "x"},
		{"action": "write", "path": "x", "content": strings.Repeat("x", maxNotesBytes), "revision": ""},
		{"action": "read", "offset": -1},
	} {
		if _, err := callNotes(t, "one", input); err == nil {
			t.Fatal("invalid request accepted")
		}
		if value, err := (workingnotes.Store{Home: home}).Read("one"); err != nil || value != nil {
			t.Fatal("invalid request mutated storage")
		}
	}
}

func TestNotesToolkitCloneKeepsStoreAndIsolatesSessions(t *testing.T) {
	home := t.TempDir()
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetWorkingNotesHome(home)
	kit.SetSessionID("one")
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	if !kit.SupportsTool("notes") || !kit.SupportsTool("request_handoff") {
		t.Fatal("native continuation tools unavailable")
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "notes", Arguments: `{"action":"write","path":"work.md","content":"kept across worktrees","revision":""}`})
	if err != nil || result.IsError {
		t.Fatalf("write=%+v %v", result, err)
	}
	clone, err := kit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err = clone.ExecuteResult(context.Background(), providers.ToolCall{Name: "notes", Arguments: `{"action":"read","path":"work.md"}`})
	if err != nil || result.IsError || !strings.Contains(result.TextProjection(), "kept across worktrees") {
		t.Fatalf("clone lost notes=%+v %v", result, err)
	}
	clone.SetSessionID("two")
	result, err = clone.ExecuteResult(context.Background(), providers.ToolCall{Name: "notes", Arguments: `{"action":"read","path":"work.md"}`})
	if err == nil && !result.IsError {
		t.Fatal("notes leaked into a different session")
	}
}
