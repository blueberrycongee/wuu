package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Ordinary CLI state commands must use the selected provider without starting
// inference, preserve data across opens, and fail when that provider is disabled.
func TestSessionCommandsUseSelectedStateComposition(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("WUU_HOME", home)
			t.Setenv("OPENAI_API_KEY", "")
			t.Chdir(root)
			cfg := config.Default()
			cfg.DefaultProvider = "openai"
			cfg.Providers = map[string]config.ProviderConfig{"openai": {Type: "openai", BaseURL: "https://unused.invalid/v1", Model: "unconfigured-model"}}
			modules := []string{"wuu.session-state", persistence.SQLiteModule}
			if backend == "jsonl" {
				modules = []string{"wuu.session-state", persistence.JSONLModule}
			}
			cfg.Composition = &composition.Config{Version: composition.Version, Modules: &modules}
			path, err := statepath.ConfigPath("")
			if err != nil {
				t.Fatal(err)
			}
			save := func() {
				t.Helper()
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			save()
			state, err := openCommandState(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.Sessions.CreateInitialized(session.Session{ID: "cli-selected", CWD: root}, []session.HistoryRecord{{Role: "user", Content: "selected provider content"}}); err != nil {
				t.Fatal(err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			output := captureStdout(t, func() {
				if err := run([]string{"session", "show", "--json", "cli-selected"}); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(output, "selected provider content") {
				t.Fatalf("missing persisted history: %s", output)
			}
			output = captureStdout(t, func() {
				if err := run([]string{"session", "export", "--json", "cli-selected"}); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(output, "selected provider content") {
				t.Fatalf("missing exported history: %s", output)
			}
			captureStdout(t, func() {
				if err := run([]string{"session", "archive", "--json", "cli-selected"}); err != nil {
					t.Fatal(err)
				}
			})
			inspection, _, err := persistence.Inspect(context.Background(), filepath.Join(statepath.SessionsDir(home), "documents"))
			if err != nil {
				t.Fatal(err)
			}
			if inspection.Backend != backend {
				t.Fatalf("selected %s, opened %s", backend, inspection.Backend)
			}
			captureStdout(t, func() {
				if err := run([]string{"runs", "--json", "--all-workspaces"}); err != nil {
					t.Fatal(err)
				}
			})
			captureStdout(t, func() {
				if err := run([]string{"session", "compact", "--json"}); err != nil {
					t.Fatal(err)
				}
			})
			modules = []string{}
			save()
			if err := run([]string{"session", "list", "--json"}); err == nil {
				t.Fatal("disabled state provider fell back to local storage")
			}
		})
	}
}

// A limited search that matches the newest session must stop on its first
// metadata page instead of collecting the entire session catalog up front.
func TestSessionSearchStopsAfterEnoughMatches(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	counter := &countingSessionDocuments{Documents: documents}
	store := session.NewStore(ctx, counter)
	for i := range 300 {
		title := "unrelated"
		if i == 299 {
			title = "requested match"
		}
		if _, err := store.CreateInitialized(session.Session{ID: fmt.Sprintf("session-%03d", i), Title: title}, nil); err != nil {
			t.Fatal(err)
		}
	}
	counter.read = 0
	matches, err := searchSessions(store, "", false, "requested match", 1)
	if err != nil || len(matches) != 1 || matches[0].ThreadID != "session-299" {
		t.Fatalf("search: %+v, %v", matches, err)
	}
	if counter.read > 16 {
		t.Fatalf("limited search read %d documents before returning its first match", counter.read)
	}
}

type countingSessionDocuments struct {
	persistence.Documents
	read int
}

func (c *countingSessionDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	view, err := c.Documents.Read(ctx, query)
	c.read += len(view.Documents)
	return view, err
}
