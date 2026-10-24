package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/hooks"
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
	modules := []string{"wuu.session-state", persistence.JSONLModule, "kernel", loopdriver.DefaultDriverID, "wuu.session-execution", "wuu.tool-execution", runtime.StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, runtime.ArtifactModule, runtime.DataQueryModule, "wuu.session.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default"}
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

// Receipt completion can fail independently of a successful prompt response.
type turnHookReceiptClient struct {
	started       chan providers.ChatRequest
	release       chan struct{}
	done          chan struct{}
	completionErr error
}

func (c *turnHookReceiptClient) Chat(ctx context.Context, request providers.ChatRequest) (providers.ChatResponse, error) {
	_, finish, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "hook-fixture", Protocol: "controlled", Transport: "memory", Mode: "chat"})
	if err != nil {
		finish(nil)
		return providers.ChatResponse{}, err
	}
	c.started <- request
	go func() {
		defer close(c.done)
		defer finish(c.completionErr)
		<-c.release
		submission.CompleteSuccess(&providers.TokenUsage{InputTokens: 19, OutputTokens: 4})
	}()
	<-c.done
	return providers.ChatResponse{Content: `{"ok":true}`}, nil
}

type turnHookOutput struct {
	lockedBuffer
	terminal chan struct{}
	once     sync.Once
}

func (o *turnHookOutput) Write(data []byte) (int, error) {
	n, err := o.lockedBuffer.Write(data)
	var message struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(data, &message) == nil && (message.Method == NotificationTurnCompleted || message.Method == NotificationTurnError) {
		o.once.Do(func() { close(o.terminal) })
	}
	return n, err
}

func newTurnHookFixture(t *testing.T, event hooks.Event, client providers.Client, generation, journal bool) (*runtime.Session, *Server, *turnHookOutput, string) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("done")})
	registry := hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{event: {{Type: "prompt", Prompt: "$ARGUMENTS"}}})
	registry.SetModelClient(hooks.NewProviderModelClient(client, "hook-fixture", nil))
	rt.HookDispatcher = hooks.NewDispatcher(registry)
	if journal {
		var err error
		rt.InferenceJournalRuntime, err = rt.Sessions.NewInferenceJournalRuntime("captured-turn-hooks")
		if err != nil {
			t.Fatal(err)
		}
		ownedJournal := rt.InferenceJournalRuntime
		t.Cleanup(func() { _ = ownedJournal.Close() })
	}
	if generation {
		rt.PluginHost = pluginhost.New()
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	out := &turnHookOutput{terminal: make(chan struct{})}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	raw, err := json.Marshal(map[string]any{"id": "hook-thread", "method": MethodThreadStart, "params": ThreadStartParams{CWD: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "hook-thread")["result"]).Thread.ID
	return rt, srv, out, id
}

func TestTurnHooksKeepCapturedDispatcherAndJournal(t *testing.T) {
	for _, event := range []hooks.Event{hooks.UserPromptSubmit, hooks.Stop} {
		t.Run(string(event), func(t *testing.T) {
			client := &turnHookReceiptClient{started: make(chan providers.ChatRequest, 1), release: make(chan struct{}), done: make(chan struct{})}
			close(client.release)
			rt, srv, out, id := newTurnHookFixture(t, event, client, true, true)
			original := srv.thread(id).state
			successor := &fakeClient{response: providersResponse(`{"ok":true}`)}
			registry := hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{event: {{Type: "prompt", Prompt: "$ARGUMENTS"}}})
			registry.SetModelClient(hooks.NewProviderModelClient(successor, "successor", nil))
			rt.HookDispatcher = hooks.NewDispatcher(registry)
			replacement := newAppserverTestStore(t, t.TempDir())
			journal, err := replacement.NewInferenceJournalRuntime("successor")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			rt.InferenceJournalRuntime = journal
			raw := fmt.Sprintf(`{"id":"hook-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, id)
			if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-out.terminal:
			case <-time.After(5 * time.Second):
				t.Fatalf("turn did not finish: %s", out.String())
			}
			var request providers.ChatRequest
			select {
			case request = <-client.started:
			default:
				t.Fatal("captured hook was replaced by workspace dispatcher")
			}
			successor.mu.Lock()
			calls := len(successor.requests)
			successor.mu.Unlock()
			if calls != 0 {
				t.Fatalf("successor hook ran %d times", calls)
			}
			var input hooks.Input
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &input); err != nil {
				t.Fatal(err)
			}
			if input.SessionID != id || input.CWD != srv.thread(id).CWD {
				t.Fatalf("hook lost thread identity: %+v", input)
			}
			view, err := original.Documents.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_submissions.v1"})
			if err != nil || len(view.Documents) != 1 {
				t.Fatalf("captured receipts = %+v, %v", view.Documents, err)
			}
			var receipt providers.InferenceSubmissionJournalRecord
			if err := json.Unmarshal(view.Documents[0].Value, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.ID != request.Execution.Snapshot().Submissions[0].ID || receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 19 || receipt.ReportedUsage.OutputTokens != 4 {
				t.Fatalf("captured physical receipt lost: %+v", receipt)
			}
			operations, err := original.Documents.Read(context.Background(), persistence.Query{Namespace: "wuu.inference_operations.v1"})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, doc := range operations.Documents {
				var op struct {
					ID      string
					OwnerID string
				}
				if err := json.Unmarshal(doc.Value, &op); err != nil {
					t.Fatal(err)
				}
				if op.ID == request.Operation.ID {
					found = true
					if op.OwnerID != id {
						t.Errorf("hook journal owner = %q, want %q", op.OwnerID, id)
					}
				}
			}
			if !found {
				t.Fatal("hook operation missing from captured journal")
			}
		})
	}
}

func TestTurnHookReceiptFailuresPreventDelivery(t *testing.T) {
	for _, event := range []hooks.Event{hooks.UserPromptSubmit, hooks.Stop} {
		for _, failure := range []string{"missing-generation", "missing-journal", "completion"} {
			t.Run(string(event)+"/"+failure, func(t *testing.T) {
				client := &turnHookReceiptClient{started: make(chan providers.ChatRequest, 1), release: make(chan struct{}), done: make(chan struct{})}
				close(client.release)
				if failure == "completion" {
					client.completionErr = errors.New("required hook receipt failed")
				}
				_, srv, out, id := newTurnHookFixture(t, event, client, failure != "missing-generation", failure != "missing-journal")
				raw := fmt.Sprintf(`{"id":"hook-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"must not succeed"}}`, id)
				if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
					t.Fatal(err)
				}
				response := responseByID(t, parseOutput(t, out.String()), "hook-turn")
				if event == hooks.UserPromptSubmit {
					if response["error"] == nil {
						t.Fatalf("prompt hook failure admitted a turn: %+v", response)
					}
					messages, err := loadChatMessages(srv.thread(id).state.Sessions, id)
					if err != nil {
						t.Fatal(err)
					}
					for _, message := range messages {
						if message.Role == "user" {
							t.Fatalf("failed admission appended user history: %+v", messages)
						}
					}
				} else {
					select {
					case <-out.terminal:
					case <-time.After(5 * time.Second):
						t.Fatalf("turn did not finish: %s", out.String())
					}
					th := srv.thread(id)
					th.mu.Lock()
					turn := th.Turns[len(th.Turns)-1]
					th.mu.Unlock()
					if turn.Status != TurnStatusFailed || turn.Error == nil {
						t.Fatalf("Stop hook failure was delivered as success: %+v", turn)
					}
				}
				if failure != "completion" {
					select {
					case <-client.started:
						t.Fatal("missing receipt authority admitted a model call")
					default:
					}
				}
			})
		}
	}
}

func TestStopHookReceiptFailureSurvivesAnswerReadyCancellation(t *testing.T) {
	client := &turnHookReceiptClient{started: make(chan providers.ChatRequest, 1), release: make(chan struct{}), done: make(chan struct{}), completionErr: errors.Join(context.Canceled, errors.New("required hook receipt failed"))}
	release := sync.OnceFunc(func() { close(client.release) })
	defer release()
	_, srv, out, id := newTurnHookFixture(t, hooks.Stop, client, true, true)
	raw := fmt.Sprintf(`{"id":"hook-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"finish the answer"}}`, id)
	if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hook did not start")
	}
	th := srv.thread(id)
	th.mu.Lock()
	ready := currentTurnIsAnswerReadyLocked(th)
	th.completeAfterAnswerReadyCancel = true
	th.mu.Unlock()
	if !ready {
		t.Fatal("fixture did not reach answer-ready handoff")
	}
	release()
	select {
	case <-out.terminal:
	case <-time.After(5 * time.Second):
		t.Fatalf("turn did not finish: %s", out.String())
	}
	th.mu.Lock()
	turn := th.Turns[len(th.Turns)-1]
	th.mu.Unlock()
	if turn.Status != TurnStatusFailed || turn.Error == nil || !strings.Contains(turn.Error.Message, agent.ErrModelReceiptLifecycle.Error()) || !strings.Contains(turn.Error.Message, "required hook receipt failed") {
		t.Fatalf("answer-ready cancellation discarded required receipt failure: %+v", turn)
	}
}
