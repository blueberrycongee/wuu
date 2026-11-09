package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Cancellation must settle the workflow after physical model receipts, not
// merely mark its child operation canceled. Reopen the actual selected store
// after all runtime owners close so recovery cannot hide an orphaned workflow.
func TestNativeTurnInferenceWorkflowSettlement(t *testing.T) {
	for _, scenario := range []string{"completed", "canceled", "canceled-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			reached := sync.OnceFunc(func() { close(entered) })
			observedCancel := sync.OnceFunc(func() { close(canceled) })
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				text := "Fixture title"
				if request.Model != "workflow-title" {
					reached()
					select {
					case <-release:
					case <-r.Context().Done():
						observedCancel()
						return
					}
					text = "Completed workflow evidence"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\ndata: [DONE]\n\n", text)
			}))
			defer upstream.Close()
			defer unblock()
			root, home, wuuHome := t.TempDir(), t.TempDir(), t.TempDir()
			cfg := config.Default()
			cfg.PTC.Enabled = false
			cfg.DefaultProvider = "workflow"
			cfg.Providers = map[string]config.ProviderConfig{"workflow": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", APIKey: "fixture-only", Model: "workflow-model"}}
			cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: "workflow", Model: "workflow-title"}
			path := filepath.Join(root, "config.json")
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			credentials := runtime.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
				return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil }), nil
			}}
			catalog := []pluginpkg.Plugin{}
			rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, WuuHome: wuuHome, Config: cfg, ConfigPath: path, ConfigLoadMode: runtime.ConfigLoadFile, NonInteractive: true, DiscoveredPlugins: &catalog, NativeProviders: []runtime.NativeProviderFactory{credentials}})
			if err != nil {
				t.Fatal(err)
			}
			cleanup := sync.OnceFunc(func() {
				if _, err := rt.Cleanup(); err != nil {
					t.Error(err)
				}
			})
			defer cleanup()
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			client := &rpcClient{server: srv, out: out}
			var thread ThreadStartResult
			client.rpc(t, MethodThreadStart, ThreadStartParams{}, &thread)
			var turn TurnStartResult
			client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: thread.Thread.ID, Prompt: "Hold the real model request until interrupted or released"}, &turn)
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatalf("model request was not admitted: %s", out.String())
			}
			original := srv.thread(thread.Thread.ID).state.Host
			if scenario == "canceled-replaced" {
				candidate, err := rt.PreflightDiscoveredExtensions(cfg, catalog)
				if err != nil {
					t.Fatal(err)
				}
				if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
					t.Fatal(err)
				}
				if rt.PluginHost == original {
					t.Fatal("replacement did not publish a new generation")
				}
			}
			if scenario != "completed" {
				client.rpc(t, MethodTurnInterrupt, TurnInterruptParams{ThreadID: thread.Thread.ID}, nil)
				select {
				case <-canceled:
				case <-time.After(10 * time.Second):
					t.Fatal("model HTTP request did not observe cancellation")
				}
			}
			unblock()
			notification := NotificationTurnCompleted
			want := "succeeded"
			if scenario != "completed" {
				notification = NotificationTurnError
				want = "canceled"
			}
			waitForMethod(t, out, notification)
			if srv.thread(thread.Thread.ID).state.Host != original {
				t.Fatal("accepted turn rebound to a replacement graph")
			}
			dir := filepath.Join(rt.SessionDir, "documents")
			srv.Close()
			cleanup()
			persisted, err := persistence.Open(context.Background(), dir, "sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer persisted.Close()
			// Each test has one model turn. Title work may add independent terminal
			// rows; match the user's owner and then follow its durable operation IDs.
			workflows, err := persisted.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_workflows.v1"})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, document := range workflows.Documents {
				var workflow struct {
					ID, OwnerID, Status string
					TerminalAt          int64
					OperationIDs        []string
				}
				if err := json.Unmarshal(document.Value, &workflow); err != nil {
					t.Fatal(err)
				}
				if workflow.OwnerID != thread.Thread.ID {
					continue
				}

				if len(workflow.OperationIDs) != 1 {
					t.Fatalf("unexpected workflow operations: %+v", workflow)
				}
				operations, err := persisted.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_operations.v1", Keys: workflow.OperationIDs})
				if err != nil {
					t.Fatal(err)
				}
				if len(operations.Documents) != 1 {
					t.Fatalf("missing operation: %+v", operations)
				}
				var operation struct {
					Kind, Status, TerminalOutcome string
					TerminalAt                    int64
					AttemptIDs                    []string
				}
				if err := json.Unmarshal(operations.Documents[0].Value, &operation); err != nil {
					t.Fatal(err)
				}
				if operation.Kind != "agent_round" {
					continue
				}
				found = true
				if workflow.Status != want || workflow.TerminalAt == 0 {
					t.Errorf("workflow not durably settled after runtime close: %s", document.Value)
				}
				if operation.Status != want || operation.TerminalOutcome != want || operation.TerminalAt == 0 {
					t.Errorf("operation terminal changed: %s", operations.Documents[0].Value)
				}
				attempts, err := persisted.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_attempts.v1", Keys: operation.AttemptIDs})
				if err != nil {
					t.Fatal(err)
				}
				if len(attempts.Documents) != 1 {
					t.Fatalf("unexpected attempts: %+v", attempts)
				}
				var attempt struct {
					Phase, TerminalOutcome string
					TerminalAt             int64
					SubmissionIDs          []string
				}
				if err := json.Unmarshal(attempts.Documents[0].Value, &attempt); err != nil {
					t.Fatal(err)
				}
				if attempt.Phase != "terminal" || attempt.TerminalOutcome != want || attempt.TerminalAt == 0 {
					t.Errorf("attempt terminal changed: %s", attempts.Documents[0].Value)
				}
				submissions, err := persisted.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_submissions.v1", Keys: attempt.SubmissionIDs})
				if err != nil {
					t.Fatal(err)
				}
				if len(submissions.Documents) != 1 {
					t.Fatalf("unexpected physical submissions: %+v", submissions)
				}
				var submission struct {
					Outcome, FailureCategory string
					CompletedAt              time.Time
				}
				if err := json.Unmarshal(submissions.Documents[0].Value, &submission); err != nil {
					t.Fatal(err)
				}
				if submission.CompletedAt.IsZero() || strings.Contains(submission.Outcome, "flight") {
					t.Errorf("physical submission not settled: %s", submissions.Documents[0].Value)
				}
				if scenario != "completed" && submission.FailureCategory != "canceled" {
					t.Errorf("physical cancellation changed: %s", submissions.Documents[0].Value)
				}
				t.Logf("reopened owner=%s workflow=%s status=%s operation=%s attempt=%s submission=%s", workflow.OwnerID, workflow.ID, workflow.Status, operation.Status, attempt.TerminalOutcome, submission.Outcome)
			}
			if !found {
				t.Fatal("no durable workflow for admitted turn")
			}
		})
	}
}
