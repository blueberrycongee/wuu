package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Failure cases: wrong authority/default fallback, lost metadata or structured
// history, live-reader invisibility, unsafe home, and silent legacy SQL fields.
func TestFixtureSeedsConfiguredAuthorityForProductionReader(t *testing.T) {
	for _, provider := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(provider, func(t *testing.T) {
			home, workdir, cfg := fixtureHome(t, provider)
			t.Setenv("WUU_HOME", home)
			reader, err := runtime.OpenState(context.Background(), runtime.StateOptions{RootDir: workdir, HomeDir: home, ConfigPath: filepath.Join(home, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			})
			stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			metadata := session.Session{ID: "fixture-source", Title: "searchable fixture", CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour), CWD: workdir, WorkspaceID: "project", Provider: "fixture", Model: "fixture-model", EngineID: "wuu", EngineRef: "native-reference", PermissionMode: "standard", PinnedAt: &stamp, ArchivedAt: &stamp}
			history := []session.HistoryRecord{
				{Role: "user", Content: "delivery context", DisplayContent: "peer message", Origin: "host", PresentationKind: "session_message", RelatedSessionID: "peer", ReadOnly: true, Name: "Peer", At: stamp},
				{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call","name":"present_artifact","arguments":"{}"}]`), Phase: "commentary", At: stamp},
				{Role: "tool", Content: "Delivered", ToolCallID: "call", Name: "present_artifact", ToolResult: json.RawMessage(`{"content":[{"type":"text","text":"structured result"}]}`), At: stamp},
				{Role: "assistant", Content: "answer", Phase: "final_answer", ProviderItems: json.RawMessage(`[{"opaque":"native"}]`), ContentParts: json.RawMessage(`[{"type":"text","text":"answer"}]`), At: stamp},
				{Role: "meta", Content: "token_usage", Provider: "historical-provider", Model: "historical-model", InputTokens: 17, OutputTokens: 9, CacheReadTokens: 4, CacheCreationTokens: 3, ContextTokens: 22, At: stamp},
			}
			request := map[string]any{"home": home, "workdir": workdir, "op": "seed", "sessions": []map[string]any{{"session": metadata, "history": history}}}
			var seeded struct {
				IDs []string `json:"ids"`
			}
			fixtureCall(t, request, &seeded)
			if !reflect.DeepEqual(seeded.IDs, []string{metadata.ID}) {
				t.Fatalf("seed result: %+v", seeded)
			}
			found, ok, err := reader.Sessions.Find(metadata.ID)
			metadata.Entries = len(history)
			if err != nil || !ok || !reflect.DeepEqual(found, metadata) {
				t.Fatalf("live production metadata: %+v found=%v err=%v", found, ok, err)
			}
			stored, err := reader.Sessions.LoadHistoryRecords(metadata.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			for i := range history {
				history[i].Seq = i + 1
			}
			if !reflect.DeepEqual(stored, history) {
				t.Fatalf("live production history changed:\ngot=%+v\nwant=%+v", stored, history)
			}
			var read struct {
				Sessions []struct {
					Session session.Session         `json:"session"`
					History []session.HistoryRecord `json:"history"`
				} `json:"sessions"`
			}
			fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "read", "ids": []string{metadata.ID}}, &read)
			if len(read.Sessions) != 1 || !reflect.DeepEqual(read.Sessions[0].Session, found) || !reflect.DeepEqual(read.Sessions[0].History, history) {
				t.Fatalf("read did not return the domain records: %+v", read)
			}
			if _, err := os.Stat(filepath.Join(home, "sessions", "sessions.sqlite3")); !os.IsNotExist(err) {
				t.Fatalf("helper created legacy SQLite: %v", err)
			}
			raw, _ := json.Marshal(request)
			if err := run(context.Background(), bytes.NewReader(raw), &bytes.Buffer{}); err == nil {
				t.Fatal("duplicate seed succeeded")
			}
		})
	}
}

func TestFixtureRefusesUnmarkedAndRealHomesBeforeOpeningState(t *testing.T) {
	home, workdir, _ := fixtureHome(t, persistence.JSONLModule)
	if err := os.Remove(filepath.Join(home, ".wuu-session-fixture")); err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{home, filepath.Join(account.HomeDir, ".wuu"), ""} {
		raw, _ := json.Marshal(map[string]any{"home": target, "workdir": workdir, "op": "seed", "sessions": []any{}})
		if err := run(context.Background(), bytes.NewReader(raw), &bytes.Buffer{}); err == nil {
			t.Fatalf("unsafe home accepted: %q", target)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("unmarked home was modified: %v", err)
	}
}

func TestFixtureRejectsLegacyFieldsAndNoncontiguousHistory(t *testing.T) {
	home, workdir, _ := fixtureHome(t, persistence.JSONLModule)
	for _, records := range []string{`[{"role":"tool","content":"x","tool_result_json":"{}"}]`, `[{"seq":7,"role":"user","content":"x"}]`} {
		raw, _ := json.Marshal(map[string]any{"home": home, "workdir": workdir, "op": "seed", "sessions": []map[string]any{{"session": session.Session{ID: "rejected"}, "history": json.RawMessage(records)}}})
		if err := run(context.Background(), bytes.NewReader(raw), &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid history accepted: %s", records)
		}
	}
}

func fixtureCall(t *testing.T, request any, result any) {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), result); err != nil {
		t.Fatalf("result %q: %v", out.String(), err)
	}
}

func fixtureHome(t *testing.T, provider string) (string, string, config.Config) {
	t.Helper()
	root := t.TempDir()
	home, workdir := filepath.Join(root, "state"), filepath.Join(root, "project")
	for _, path := range []string{home, workdir} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".wuu-session-fixture"), []byte("wuu-session-fixture-v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	modules := []string{"wuu.session-state", session.DefaultModule, provider}
	cfg := config.Config{
		DefaultProvider: "fixture",
		Providers: map[string]config.ProviderConfig{
			"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", Model: "fixture-model"},
		},
		Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: provider}}},
	}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(home, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "WUU_HOME", "CODEX_HOME", "GROK_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, strings.TrimSpace(os.Getenv(key)))
	}
	return home, workdir, cfg
}
