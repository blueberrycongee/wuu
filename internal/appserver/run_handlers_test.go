package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/structuredoutput"
)

func TestRunStartSettlesManifest(t *testing.T) {
	client := &fakeClient{response: providersResponse("run-protocol-ok")}
	rt := newTestRuntime(t, client)
	runtimeJournal, err := session.NewInferenceJournalRuntime(rt.SessionDir, "run-test")
	if err != nil {
		t.Fatalf("NewInferenceJournalRuntime: %v", err)
	}
	rt.InferenceJournalRuntime = runtimeJournal
	defer runtimeJournal.Close()
	rt.StreamRunner = &agent.StreamRunner{
		Client:       providers.AdaptStreamClient(client),
		ProviderName: rt.ProviderName,
		Model:        rt.Model,
		SystemPrompt: "system",
	}
	rt.HookDispatcher = hooks.NewDispatcher(nil)

	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	thread := newThreadState("ephemeral-run-thread", []providers.ChatMessage{{Role: "system", Content: "system"}}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	thread.Ephemeral = true
	srv.threads[thread.ID] = thread
	rt.ActivityRegistry = activity.NewRegistry()
	options := activity.StartOptions{ThreadID: thread.ID, Workdir: rt.RootDir, Kind: activity.KindBrowser, PluginID: "browser"}
	browser, _, err := rt.ActivityRegistry.Acquire(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ActivityRegistry.Takeover(thread.ID, browser.ID); err != nil {
		t.Fatal(err)
	}

	request := Request{ID: json.RawMessage(`"start"`), Method: MethodRunStart, Params: mustJSON(RunStartParams{
		ThreadID: thread.ID,
		Prompt:   "reply with exactly: run-protocol-ok",
		Request:  execution.Request{Mode: execution.ModeStart, Requested: execution.Selection{Provider: "fake-provider", Model: "fake-model"}},
	})}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("run/start: %v", err)
	}
	startResult := remarshal[RunStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	run := startResult.Run
	if _, _, err := rt.ActivityRegistry.Acquire(options); err != nil {
		t.Fatalf("run/start did not resume browser: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stored, getErr := srv.runStore.Get(context.Background(), run.ID)
		if getErr == nil {
			run = stored
			if run.Status.Terminal() {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.ID == "" {
		t.Fatalf("run/start did not return a Run: %s", out.String())
	}
	if run.Status != execution.StatusCompleted {
		t.Fatalf("Run status = %q, output: %s", run.Status, out.String())
	}
	if len(run.Turns) != 1 || run.Turns[0].TurnID == "" {
		t.Fatalf("Run turn refs = %+v", run.Turns)
	}
	if run.Request.HasPrompt != true || run.Request.ImageCount != 0 {
		t.Fatalf("Run request facts = %+v", run.Request)
	}
	if run.Runtime.Resolved.Provider != "fake-provider" || run.Runtime.Resolved.Model != "fake-model" {
		t.Fatalf("Run runtime facts = %+v", run.Runtime)
	}
	if run.Result == nil || run.Result.FinalTurnID != run.Turns[0].TurnID {
		t.Fatalf("Run result = %+v", run.Result)
	}
	if run.Result.ExitCode != execution.ExitOK {
		t.Fatalf("Run exit code = %d, want %d", run.Result.ExitCode, execution.ExitOK)
	}
	if srv.executionRunAttached(run.ID) {
		t.Fatal("terminal Run should not remain attached")
	}
}

func TestExecutionRunSchemaOutcomeRetriesWithinOneRun(t *testing.T) {
	srv := &Server{runs: make(map[string]*runTracker), activeRunByThread: make(map[string]string)}
	validator, err := structuredoutput.New(json.RawMessage(`{"type":"object","required":["ok"]}`))
	if err != nil {
		t.Fatalf("New validator: %v", err)
	}
	srv.registerExecutionRun(execution.Run{ID: "run-schema", ThreadID: "thread-schema"}, validator)

	for attempt := 0; attempt < structuredoutput.MaxRetries; attempt++ {
		prompt, retry, validationErr := srv.executionRunSchemaOutcome("run-schema", `{"wrong":true}`)
		if !retry || validationErr != nil || prompt == "" {
			t.Fatalf("attempt %d outcome = prompt %q, retry %v, error %v", attempt, prompt, retry, validationErr)
		}
	}
	_, retry, validationErr := srv.executionRunSchemaOutcome("run-schema", `{"wrong":true}`)
	if retry || validationErr == nil {
		t.Fatalf("exhausted outcome = retry %v, error %v", retry, validationErr)
	}
	if _, retry, validationErr := srv.executionRunSchemaOutcome("run-schema", `{"ok":true}`); retry || validationErr != nil {
		t.Fatalf("valid outcome = retry %v, error %v", retry, validationErr)
	}
}

func TestFailAndDetachExecutionRunReturnsExistingTerminalRun(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	run := createEphemeralExecutionRun(t, srv, "thread-terminal-race")
	if _, err := srv.runStore.AttachTurn(context.Background(), run.ID, run.ThreadID, "turn-terminal-race", time.Now().UTC()); err != nil {
		t.Fatalf("AttachTurn: %v", err)
	}
	srv.registerExecutionRun(run, nil)
	completed, err := srv.runStore.Complete(context.Background(), run.ID, execution.Result{FinalTurnID: "turn-terminal-race"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := srv.failAndDetachExecutionRun(run.ID, execution.StatusInterrupted, "interrupted", "cancelled", context.Canceled)
	if err != nil {
		t.Fatalf("failAndDetachExecutionRun: %v", err)
	}
	if got.ID != completed.ID || got.Status != execution.StatusCompleted {
		t.Fatalf("race result = %+v, want completed Run %+v", got, completed)
	}
	if srv.executionRunAttached(run.ID) {
		t.Fatal("terminal Run remained attached")
	}
}

func TestRunInterruptRecordsTimeoutBeforeCancellingTurn(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	run := createEphemeralExecutionRun(t, srv, "thread-timeout-order")
	if _, err := srv.runStore.AttachTurn(context.Background(), run.ID, run.ThreadID, "turn-timeout-order", time.Now().UTC()); err != nil {
		t.Fatalf("AttachTurn: %v", err)
	}
	srv.registerExecutionRun(run, nil)
	th := newThreadState(run.ThreadID, []providers.ChatMessage{{Role: "system", Content: "system"}}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	observed := execution.Status("")
	th.cancel = func() { observed = srv.executionRunInterruptStatus(run.ID) }
	th.currentExecutionRunID = run.ID
	srv.threads[run.ThreadID] = th

	req := Request{ID: json.RawMessage(`"interrupt"`), Method: MethodRunInterrupt, Params: mustJSON(RunInterruptParams{RunID: run.ID, Reason: "timeout"})}
	if err := srv.handleRunInterrupt(context.Background(), req); err != nil {
		t.Fatalf("run/interrupt: %v", err)
	}
	if observed != execution.StatusTimedOut {
		t.Fatalf("interrupt status observed by cancel = %q, want %q", observed, execution.StatusTimedOut)
	}
}

func TestRunInterruptDoesNotCancelSuccessorRun(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	const threadID = "thread-successor-run"
	th := newThreadState(threadID, []providers.ChatMessage{{Role: "system", Content: "system"}}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	cancelled := false
	th.cancel = func() { cancelled = true }
	th.currentExecutionRunID = "run-successor"
	srv.threads[threadID] = th

	turnActive, err := srv.interruptThreadExecution(threadID, "run-original", "")
	if !errors.Is(err, errExecutionRunChanged) || turnActive {
		t.Fatalf("interrupt successor = active %v, error %v", turnActive, err)
	}
	if cancelled {
		t.Fatal("stale interrupt cancelled the successor Run")
	}
}

func createEphemeralExecutionRun(t *testing.T, srv *Server, threadID string) execution.Run {
	t.Helper()
	run, err := srv.runStore.Create(context.Background(), execution.CreateParams{
		RuntimeID: "test-runtime", Ephemeral: true, ThreadID: threadID,
		Request: execution.Request{Mode: execution.ModeStart, HasPrompt: true},
		Runtime: execution.RuntimeManifest{
			ProtocolVersion: "wuu-app-server/v0.1",
			Resolved:        execution.Selection{Provider: "fake-provider", Model: "fake-model"},
		},
		Workspace: execution.WorkspaceRef{Root: srv.rt.RootDir},
	})
	if err != nil {
		t.Fatalf("Create execution Run: %v", err)
	}
	return run
}
