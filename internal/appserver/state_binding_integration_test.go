package appserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestServerResumeUsesSelectedAuthorityInsteadOfCachedTranscript(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	old := rt.Sessions
	const id = "authority-switch-transcript"
	if _, err := old.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := appendChatMessage(old, id, providers.ChatMessage{Role: "user", Content: "original authority"}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"before","method":"thread/resume","params":{"session_id":%q}}`, id))); err != nil {
		t.Fatal(err)
	}
	before := responseByID(t, parseOutput(t, out.String()), "before")
	if before["error"] != nil {
		t.Fatalf("initial resume: %+v", before)
	}
	documents, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = documents.Close() })
	selected := session.NewStore(context.Background(), documents)
	if _, err := selected.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := appendChatMessage(selected, id, providers.ChatMessage{Role: "user", Content: "selected authority"}); err != nil {
		t.Fatal(err)
	}
	// This embedded runtime publishes an explicit replacement; production uses
	// the same State capture through its generation publication lock.
	rt.Documents, rt.Sessions = documents, selected
	if err := srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"after","method":"thread/resume","params":{"session_id":%q}}`, id))); err != nil {
		t.Fatal(err)
	}
	after := responseByID(t, parseOutput(t, out.String()), "after")
	if after["error"] != nil {
		t.Fatalf("replacement resume: %+v", after)
	}
	result := remarshal[ThreadResumeResult](t, after["result"])
	found := false
	for _, turn := range result.Thread.Turns {
		for _, item := range turn.Items {
			if item.Text == "selected authority" {
				found = true
			}
			if item.Text == "original authority" {
				t.Fatalf("served stale cached transcript: %+v", result.Thread)
			}
		}
	}
	if !found {
		t.Fatalf("selected transcript missing: %+v", result.Thread)
	}
	original, err := loadChatMessages(old, id)
	if err != nil || len(original) != 1 || original[0].Content != "original authority" {
		t.Fatalf("original authority changed: %+v, %v", original, err)
	}
}

// A parked Run retains its original authority after publication. A scheduler
// for the replacement must ignore it, while each user interruption entry can settle it.
func TestAttachedRunKeepsCapturedStoreAfterRequestCloseAndReplacement(t *testing.T) {
	for _, method := range []string{MethodRunInterrupt, MethodTurnInterrupt, MethodActivityTakeover, MethodActivityStop} {
		t.Run(method, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.ActivityRegistry = activity.NewRegistry()
			journal, err := rt.Sessions.NewInferenceJournalRuntime("captured-run-test")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			rt.InferenceJournalRuntime = journal
			const id = "captured-run-thread"
			if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			binding, err := rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close()
			store, err := execution.NewStore(binding.Documents)
			if err != nil {
				t.Fatal(err)
			}
			run, err := store.Create(context.Background(), execution.CreateParams{RuntimeID: journal.RuntimeID(), ThreadID: id, Request: execution.Request{Mode: execution.ModeStart}, Runtime: execution.RuntimeManifest{Resolved: execution.Selection{Provider: "test", Model: "test"}, ProtocolVersion: ProtocolVersion}, Workspace: execution.WorkspaceRef{Root: rt.RootDir}})
			if err != nil {
				t.Fatal(err)
			}
			th, err := srv.ensureThreadLoaded(binding, id)
			if err != nil {
				t.Fatal(err)
			}
			srv.registerExecutionRun(binding, store, run, nil)
			if _, _, err := rt.ActivityRegistry.Start(activity.StartOptions{ID: "captured-activity", ThreadID: id, Kind: activity.KindBrowser, Workdir: rt.RootDir}); err != nil {
				t.Fatal(err)
			}
			srv.runMu.Lock()
			srv.runs[run.ID].schemaRetry = &executionSchemaRetry{projectID: "same-project", thread: th, snapshot: turnRuntimeSnapshot{ExecutionRunID: run.ID}, prompt: "correction"}
			srv.runMu.Unlock()
			if err := binding.Close(); err != nil {
				t.Fatal(err)
			}
			replacement, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = replacement.Close() })
			rt.Documents, rt.Sessions = replacement, session.NewStore(context.Background(), replacement)
			current := testRuntimeState(t, rt)
			if pending, full := srv.drainExecutionSchemaRetries(current, "same-project", false); pending || full {
				t.Fatalf("replacement scheduler observed original Run: pending=%v capacity=%v", pending, full)
			}
			view, err := srv.readExecutionRun(current, context.Background(), run.ID)
			if err != nil || view.Run.ID != run.ID || !view.Attached {
				t.Fatalf("attached run switched authority: %+v, %v", view, err)
			}
			params := any(TurnInterruptParams{ThreadID: id})
			want := execution.StatusInterrupted
			if method == MethodActivityTakeover || method == MethodActivityStop {
				params = ActivityActionParams{ThreadID: id, ActivityID: "captured-activity"}
			}
			if method == MethodRunInterrupt {
				params = RunInterruptParams{RunID: run.ID, Reason: "timeout"}
				want = execution.StatusTimedOut
			}
			request := Request{ID: mustJSON("interrupt"), Method: method, Params: mustJSON(params)}
			if err := srv.handleLine(context.Background(), mustJSON(request)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), "interrupt")
			if response["error"] != nil {
				t.Fatalf("interrupt retained Run: %+v", response)
			}
			original, err := store.Get(context.Background(), run.ID)
			if err != nil || original.Status != want {
				t.Fatalf("original run was not settled: %+v, %v", original, err)
			}
			if srv.executionRunAttached(run.ID) {
				t.Fatal("settled run kept its captured state attached")
			}
			replacementStore, err := execution.NewStore(replacement)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replacementStore.Get(context.Background(), run.ID); err == nil {
				t.Fatal("run was copied into replacement authority")
			}
		})
	}
}

func TestServerCloseDetachesRunWhenTerminalPersistenceFails(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	journal, err := rt.Sessions.NewInferenceJournalRuntime("failed-store-test")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	rt.InferenceJournalRuntime = journal
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	binding := testRuntimeState(t, rt)
	store, err := execution.NewStore(binding.Documents)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Create(context.Background(), execution.CreateParams{RuntimeID: journal.RuntimeID(), ThreadID: "close-run", Request: execution.Request{Mode: execution.ModeStart}, Runtime: execution.RuntimeManifest{Resolved: execution.Selection{Provider: "test", Model: "test"}, ProtocolVersion: ProtocolVersion}, Workspace: execution.WorkspaceRef{Root: rt.RootDir}})
	if err != nil {
		t.Fatal(err)
	}
	srv.registerExecutionRun(binding, store, run, nil)
	if err := binding.Documents.(io.Closer).Close(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if srv.executionRunAttached(run.ID) {
		t.Fatal("shutdown retained Run ownership after terminal persistence failed")
	}
}

// A delayed title must persist its transcript metadata and inference operation
// in the same authority, even after another provider has been published.
func TestTitleGenerationKeepsCapturedJournalAfterReplacement(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.TitleClient = &fakeClient{response: providersResponse("Captured title")}
	const id = "captured-title"
	if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	originalJournal, err := rt.Sessions.NewInferenceJournalRuntime("title-test")
	if err != nil {
		t.Fatal(err)
	}
	defer originalJournal.Close()
	rt.InferenceJournalRuntime = originalJournal
	attachTestModelGeneration(t, rt)
	binding := testRuntimeState(t, rt)
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()

	replacement, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	selected := session.NewStore(context.Background(), replacement)
	replacementJournal, err := selected.NewInferenceJournalRuntime("title-test")
	if err != nil {
		t.Fatal(err)
	}
	defer replacementJournal.Close()
	rt.Documents, rt.Sessions, rt.InferenceJournalRuntime = replacement, selected, replacementJournal
	rt.TitleClient = &fakeClient{response: providersResponse("Successor title")}

	result, err := srv.generateThreadTitleCore(binding, id, []providers.ChatMessage{{Role: "user", Content: "Keep the original provider"}}, true, false, true, nil)
	if err != nil || result.PersistedTitle != "Captured title" {
		t.Fatalf("captured title: %+v, %v", result, err)
	}
	for _, authority := range []struct {
		name      string
		documents persistence.Documents
		want      int
	}{{"captured", binding.Documents, 1}, {"replacement", replacement, 0}} {
		view, err := authority.documents.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_operations.v1", Limit: 2})
		if err != nil || len(view.Documents) != authority.want {
			t.Fatalf("%s inference operations = %+v, %v; want %d", authority.name, view.Documents, err, authority.want)
		}
	}
	if _, found, err := selected.Find(id); err != nil || found {
		t.Fatalf("title leaked into replacement: found=%v, err=%v", found, err)
	}
}

// Title overrides must resolve configuration and saved credentials from the
// request's selected services, without constructing a second workspace graph.
func TestTitleOverrideUsesSelectedConfigurationAndCredentials(t *testing.T) {
	ctx := context.Background()
	header := make(chan string, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Selected override title\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	selected := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err := authstorage.NewBound(ctx, selected).Set("selected", authstorage.Credentials{APIKey: "selected-key"}); err != nil {
		t.Fatal(err)
	}
	modules := []string{"wuu.session-state", persistence.JSONLModule, "kernel", loopdriver.DefaultDriverID, "wuu.session-execution", "wuu.tool-execution", runtime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, runtime.ArtifactModule, runtime.DataQueryModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "selected", Providers: map[string]config.ProviderConfig{"selected": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", Model: "base"}}}
	binding, err := runtime.OpenState(ctx, runtime.StateOptions{
		RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg,
		NativeProviders: []runtime.NativeProviderFactory{
			{ID: config.Module, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
				return config.NewMemoryService(config.Module, cfg), nil
			}},
			{ID: credentialstore.FileModule, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
				return credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) { return selected, nil }), nil
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	out := &lockedBuffer{}
	srv := New(newTestRuntime(t, &fakeClient{}), out)
	defer srv.Close()
	request := Request{ID: mustJSON("override")}
	if err := srv.regenerateTitleWithOverride(binding, ctx, request, "selected-title", []providers.ChatMessage{{Role: "user", Content: "Use selected services"}}, ThreadRegenerateTitleParams{ModelOverride: "override", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "override")
	if response["error"] != nil {
		t.Fatalf("selected override: %+v", response)
	}
	result := remarshal[ThreadRegenerateTitleResult](t, response["result"])
	if result.CleanedTitle != "Selected override title" || result.Model != "override" {
		t.Fatalf("selected override result: %+v", result)
	}
	select {
	case auth := <-header:
		if auth != "Bearer selected-key" {
			t.Fatalf("selected credentials = %q", auth)
		}
	default:
		t.Fatal("selected provider was not called")
	}
}

// History loading and managed creation must keep the prompt from the same
// retained authority, even when publication changes the shared runner later.
func TestThreadHistoryUsesCapturedPromptAfterPublication(t *testing.T) {
	for _, path := range []string{"resume", "fork", "managed"} {
		t.Run(path, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.SystemPrompt = "captured authority prompt"
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			const id = "captured-prompt-source"
			if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			if _, err := appendChatMessage(rt.Sessions, id, providers.ChatMessage{Role: "user", Content: "stored conversation"}); err != nil {
				t.Fatal(err)
			}
			binding := testRuntimeState(t, rt)
			rt.StreamRunner.SystemPrompt = "successor authority prompt"
			var history []providers.ChatMessage
			switch path {
			case "resume":
				loaded, err := srv.loadPersistedThreadSnapshot(binding, id)
				if err != nil {
					t.Fatal(err)
				}
				history = loaded.history
			case "fork":
				loaded, err := srv.loadForkSourceThread(binding, id, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				history = loaded.history
			case "managed":
				th, err := srv.createHostSessionThreadWithStore(binding, binding.Sessions, "fixture", "plugin", "captured-prompt-child", hostSessionCreateParams{ContextSource: sessionContextFresh, Instructions: "child instructions"})
				if err != nil {
					t.Fatal(err)
				}
				history = th.History
			}
			if len(history) == 0 || history[0].Role != "system" || !strings.Contains(history[0].Content, "captured authority prompt") || strings.Contains(history[0].Content, "successor authority prompt") {
				t.Fatalf("history followed the successor prompt: %+v", history)
			}
			if path == "managed" && !strings.Contains(history[0].Content, "child instructions") {
				t.Fatalf("lost child instructions: %+v", history)
			}
			persisted, err := loadChatMessages(binding.Sessions, id)
			if err != nil || len(persisted) != 1 || persisted[0].Content != "stored conversation" {
				t.Fatalf("read-only source changed: %+v, %v", persisted, err)
			}
		})
	}
}
