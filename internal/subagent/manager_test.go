package subagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type managerTestJournal struct {
	mu                  sync.Mutex
	workflowCompletions []providers.InferenceWorkflowTerminalRecord
}

func (*managerTestJournal) PrepareOperation(providers.InferenceOperationJournalRecord) error {
	return nil
}
func (*managerTestJournal) PrepareAttempt(providers.InferenceAttemptJournalRecord) error { return nil }
func (*managerTestJournal) UpsertSubmission(context.Context, providers.InferenceSubmissionJournalRecord) error {
	return nil
}
func (*managerTestJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error { return nil }
func (*managerTestJournal) CompleteAttempt(providers.InferenceAttemptTerminalRecord) error {
	return nil
}
func (*managerTestJournal) PrepareRecoveryAttempt(context.Context, providers.InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (*managerTestJournal) CompleteOperation(providers.InferenceOperationTerminalRecord) error {
	return nil
}
func (j *managerTestJournal) CompleteWorkflow(ctx context.Context, record providers.InferenceWorkflowTerminalRecord) error {
	j.mu.Lock()
	j.workflowCompletions = append(j.workflowCompletions, record)
	j.mu.Unlock()
	return nil
}

func TestManagerUpdateDefaultsPreservesInferenceJournal(t *testing.T) {
	journal := &managerTestJournal{}
	manager := NewManagerWithOptions(&fakeClient{}, "model", ManagerOptions{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), InferenceJournal: journal})

	manager.UpdateDefaults(nil, "updated-model", ManagerOptions{})

	if got := manager.defaultsSnapshot().journal; got != journal {
		t.Fatalf("journal after defaults update = %T %p, want original %p", got, got, journal)
	}
}

// fakeClient is a tiny providers.StreamClient stub for tests. It
// returns the canned response on every Chat / StreamChat call and
// stashes the most recent request payload so tests can assert what
// the runner actually sent (used by the fork-with-history test).
type fakeClient struct {
	response providers.ChatResponse
	err      error
	calls    atomic.Int32
	delay    time.Duration
	// providerItemID, when set, is attached to the streamed content delta
	// so the turn produces provider-native state (as a Responses-style
	// gateway would) and tests can assert its persisted provenance.
	providerItemID string
	lastRequest    atomic.Pointer[providers.ChatRequest]
}

type terminalBoundaryClient struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}
	secondSeen   chan providers.ChatRequest
}

func (c *terminalBoundaryClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming request")
}

func (c *terminalBoundaryClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	call := c.calls.Add(1)
	if call == 1 {
		close(c.firstStarted)
		select {
		case <-c.releaseFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else if call == 2 {
		c.secondSeen <- req
	}
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: fmt.Sprintf("turn %d done", call)}
	events <- providers.StreamEvent{Type: providers.EventDone}
	close(events)
	return events, nil
}

func visibleMessagesForTest(msgs []providers.ChatMessage) []providers.ChatMessage {
	out := make([]providers.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Hidden {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (f *fakeClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	f.calls.Add(1)
	cp := req
	f.lastRequest.Store(&cp)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return providers.ChatResponse{}, ctx.Err()
		}
	}
	if f.err != nil {
		return providers.ChatResponse{}, f.err
	}
	return f.response, nil
}

// StreamChat replays the same canned response as a single content
// delta followed by a terminal Done event. Errors and the delay knob
// behave the same way they do for Chat so existing tests don't need
// to grow a stream-specific code path.
func (f *fakeClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	f.calls.Add(1)
	cp := req
	f.lastRequest.Store(&cp)
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan providers.StreamEvent, 4)
	go func() {
		defer close(ch)
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-ctx.Done():
				ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
				return
			}
		}
		if f.response.Content != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: f.response.Content, ProviderItemID: f.providerItemID}
		}
		ch <- providers.StreamEvent{
			Type:       providers.EventDone,
			StopReason: f.response.StopReason,
			Truncated:  f.response.Truncated,
		}
	}()
	return ch, nil
}

// resumeClient drives a sub-agent turn to an abnormal terminal state on
// demand (an API-style error, or a block until the context is cancelled)
// and then, once disarmed with arm(""), runs the resume turn to
// completion. It lets the fail->resume and cancel->resume paths be
// exercised against a single manager and client.
type resumeClient struct {
	mu          sync.Mutex
	mode        string // "fail", "block", or "" (succeed)
	startedOnce bool
	started     chan struct{}
	response    providers.ChatResponse
	lastRequest atomic.Pointer[providers.ChatRequest]
}

func newResumeClient(mode string, resp providers.ChatResponse) *resumeClient {
	return &resumeClient{mode: mode, started: make(chan struct{}), response: resp}
}

func (c *resumeClient) arm(mode string) {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
}

func (c *resumeClient) currentMode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

func (c *resumeClient) signalStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.startedOnce {
		c.startedOnce = true
		close(c.started)
	}
}

func (c *resumeClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return c.response, nil
}

func (c *resumeClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	cp := req
	c.lastRequest.Store(&cp)
	switch c.currentMode() {
	case "fail":
		return nil, errors.New("api terminal error")
	case "block":
		ch := make(chan providers.StreamEvent, 2)
		go func() {
			defer close(ch)
			c.signalStarted()
			<-ctx.Done()
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}()
		return ch, nil
	default:
		ch := make(chan providers.StreamEvent, 2)
		if c.response.Content != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: c.response.Content}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone}
		close(ch)
		return ch, nil
	}
}

// fakeToolkit is a no-op ToolExecutor that satisfies the runner contract.
type fakeToolkit struct{}

func (fakeToolkit) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "agent_report", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}}
}
func (fakeToolkit) Execute(_ context.Context, _ providers.ToolCall) (string, error) {
	return "", nil
}

func TestWorkerRequiresExplicitDriver(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "unexpected"}}
	var hooks atomic.Int32
	manager := NewManagerWithOptions(client, "controlled", ManagerOptions{
		AcquireLoopDriver: nil,
		OnSubagentStart:   func(context.Context, string) error { hooks.Add(1); return nil },
		OnSubagentStop:    func(context.Context, string) error { hooks.Add(1); return nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker, err := manager.Spawn(ctx, SpawnOptions{Prompt: "run", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Wait(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusFailed || snapshot.Error == nil || !strings.Contains(snapshot.Error.Error(), "loop driver") {
		t.Fatalf("missing driver result = %+v", snapshot)
	}
	if client.calls.Load() != 0 || hooks.Load() != 0 {
		t.Fatalf("missing driver dispatched work: model=%d hooks=%d", client.calls.Load(), hooks.Load())
	}
}

func TestSpawn_HappyPath(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "all done"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:         "explorer",
		AgentProfile: "qa workflow",
		Prompt:       "find foo",
		Toolkit:      fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if sa.ID == "" || sa.Type != "explorer" {
		t.Fatalf("unexpected sub-agent: %+v", sa)
	}

	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", snap.Status)
	}
	if snap.Result != "all done" {
		t.Fatalf("got result %q", snap.Result)
	}
	if snap.AgentProfile != "qa workflow" {
		t.Fatalf("AgentProfile = %q, want qa workflow", snap.AgentProfile)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", client.calls.Load())
	}
}

func TestRunTurnDispatchesSubagentLifecycleOnce(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	var mu sync.Mutex
	var events []string
	mgr := NewManagerWithOptions(client, "fake-model", ManagerOptions{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

		OnSubagentStart: func(_ context.Context, id string) error {
			mu.Lock()
			events = append(events, "start:"+id)
			mu.Unlock()
			return nil
		},
		OnSubagentStop: func(_ context.Context, id string) error {
			mu.Lock()
			events = append(events, "stop:"+id)
			mu.Unlock()
			return nil
		},
	})
	sa, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"start:" + sa.ID, "stop:" + sa.ID}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle events = %v, want %v", events, want)
	}
}

func TestSpawnStartsWorkflowIndependentFromParentAgent(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "all done"}}
	journal := &managerTestJournal{}
	mgr := NewManagerWithOptions(client, "fake-model", ManagerOptions{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), InferenceJournal: journal})
	parent := providers.NewInferenceWorkflow(providers.InferenceProfileInteractive)
	ctx := providers.WithInferenceWorkflow(context.Background(), parent)

	sa, err := mgr.Spawn(ctx, SpawnOptions{
		Type:    "worker",
		Prompt:  "do work",
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	req := client.lastRequest.Load()
	if req == nil {
		t.Fatal("expected a provider request")
	}
	if req.Operation.WorkflowID == "" || req.Operation.WorkflowID == parent.ID {
		t.Fatalf("child workflow = %q, parent = %q", req.Operation.WorkflowID, parent.ID)
	}
	if req.Operation.WorkloadProfile != providers.InferenceProfileBackgroundAgent {
		t.Fatalf("child workload profile = %q", req.Operation.WorkloadProfile)
	}
	journal.mu.Lock()
	completions := append([]providers.InferenceWorkflowTerminalRecord(nil), journal.workflowCompletions...)
	journal.mu.Unlock()
	if len(completions) != 1 || completions[0].WorkflowID != req.Operation.WorkflowID || completions[0].Outcome != providers.InferenceOutcomeSucceeded {
		t.Fatalf("child workflow completions = %+v", completions)
	}
}

type receiptWorkerClient struct {
	fakeClient
	scopes chan *providers.ModelReceiptScope
}

func (c *receiptWorkerClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.scopes <- providers.ModelReceiptScopeFromContext(ctx)
	return c.fakeClient.StreamChat(ctx, req)
}

func TestWorkerModelReceiptsSurviveInheritedClosedParent(t *testing.T) {
	for _, restored := range []bool{false, true} {
		t.Run(fmt.Sprintf("restored-%t", restored), func(t *testing.T) {
			deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			ctx, parent := providers.NewModelReceiptScope(deadline)
			parent.Seal()
			ctx = context.WithoutCancel(ctx)
			client := &receiptWorkerClient{fakeClient: fakeClient{response: providers.ChatResponse{Content: "worker done"}}, scopes: make(chan *providers.ModelReceiptScope, 2)}
			var begins, finishes atomic.Int32
			mgr := NewManagerWithOptions(client, "test", ManagerOptions{
				AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
				BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
					begins.Add(1)
					if providers.ModelReceiptScopeFromContext(ctx) != parent {
						return ctx, nil, errors.New("worker admission lost caller context")
					}
					ctx, scope := providers.NewModelReceiptScope(ctx)
					return ctx, func() error { finishes.Add(1); scope.Seal(); return scope.Wait(deadline) }, nil
				},
			})
			// Updating model defaults must not erase generation-owned receipt admission.
			mgr.UpdateDefaults(nil, "updated", ManagerOptions{})
			selected := &WorkerRuntime{Model: "selected", Client: client}
			var sa *SubAgent
			var err error
			if restored {
				sa, err = mgr.Restore(RestoreOptions{Run: PersistedRun{ID: "restored-worker", Model: "test", Status: StatusCompleted, Messages: []providers.ChatMessage{{Role: "user", Content: "earlier task"}}}, Toolkit: fakeToolkit{}, WorkerRuntime: selected})
				if err == nil {
					_, err = mgr.Followup(ctx, sa.ID, "resume")
				}
			} else {
				sa, err = mgr.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}, WorkerRuntime: selected})
			}
			if err != nil {
				t.Fatal(err)
			}
			var previous *providers.ModelReceiptScope
			for turn := int32(1); turn <= 2; turn++ {
				snapshot, err := mgr.Wait(deadline, sa.ID)
				if err != nil || snapshot.Status != StatusCompleted || begins.Load() != turn || finishes.Load() != turn {
					t.Fatalf("worker receipt lifecycle failed: status=%s admissions=%d completions=%d err=%v workerErr=%s", snapshot.Status, begins.Load(), finishes.Load(), err, snapshot.Error)
				}
				select {
				case scope := <-client.scopes:
					if scope == nil || scope == parent || scope == previous {
						t.Fatal("worker model reused inherited or previous receipt scope")
					}
					select {
					case <-scope.Done():
					default:
						t.Fatal("worker finished before receipt completion")
					}
					previous = scope
				default:
					t.Fatal("worker did not call its selected model")
				}
				if turn == 1 {
					if _, err := mgr.Followup(ctx, sa.ID, "continue"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

type lifecyclePromptJournal struct {
	managerTestJournal
	operations []providers.InferenceOperation
}

func (j *lifecyclePromptJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.operations = append(j.operations, record.Operation)
	return nil
}

type lifecyclePromptClient struct {
	mu       sync.Mutex
	scopes   []*providers.ModelReceiptScope
	journals []providers.InferenceJournal
}

func (c *lifecyclePromptClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.Operation.Kind != providers.InferenceOperationHookPrompt {
		return providers.ChatResponse{}, errors.New("lifecycle prompt lost its operation kind")
	}
	c.scopes = append(c.scopes, providers.ModelReceiptScopeFromContext(ctx))
	c.journals = append(c.journals, providers.InferenceJournalFromContext(ctx))
	if len(c.scopes) == 2 {
		return providers.ChatResponse{Content: `{"ok":false,"reason":"stop policy rejected"}`}, nil
	}
	return providers.ChatResponse{Content: `{"ok":true}`}, nil
}

func TestWorkerPromptHooksOwnReceiptsAndJournal(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parentJournal, workerJournal := &lifecyclePromptJournal{}, &lifecyclePromptJournal{}
	ctx, parent := providers.NewModelReceiptScope(providers.WithInferenceJournal(deadline, parentJournal))
	parent.Seal()
	ctx = context.WithoutCancel(ctx)
	promptClient := &lifecyclePromptClient{}
	registry := hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.SubagentStart: {{Type: "prompt", Prompt: "$ARGUMENTS"}},
		hooks.SubagentStop:  {{Type: "prompt", Prompt: "$ARGUMENTS"}},
	})
	registry.SetModelClient(hooks.NewProviderModelClient(promptClient, "hook-model", parentJournal))
	dispatcher := hooks.NewDispatcher(registry)
	cleanupErr := errors.New("stop receipt checkpoint failed")
	stopFinishing, releaseStop := make(chan struct{}), make(chan struct{})
	var scopes []*providers.ModelReceiptScope
	var admissionJournals []providers.InferenceJournal
	manager := NewManagerWithOptions(&fakeClient{response: providers.ChatResponse{Content: "worker output"}}, "worker-model", ManagerOptions{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
		InferenceJournal:  workerJournal,
		BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
			ctx, scope := providers.NewModelReceiptScope(ctx)
			scopes = append(scopes, scope)
			admissionJournals = append(admissionJournals, providers.InferenceJournalFromContext(ctx))
			ordinal := len(scopes)
			return ctx, func() error {
				scope.Seal()
				if ordinal == 3 {
					close(stopFinishing)
					select {
					case <-releaseStop:
					case <-deadline.Done():
						return deadline.Err()
					}
					return errors.Join(scope.CheckpointSubmissions(deadline), cleanupErr)
				}
				return scope.CheckpointSubmissions(deadline)
			}, nil
		},
		OnSubagentStart: func(ctx context.Context, id string) error {
			_, err := dispatcher.Dispatch(ctx, hooks.SubagentStart, &hooks.Input{AgentID: id})
			return err
		},
		OnSubagentStop: func(ctx context.Context, id string) error {
			_, err := dispatcher.Dispatch(ctx, hooks.SubagentStop, &hooks.Input{AgentID: id})
			return err
		},
	})
	sa, err := manager.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		snapshot SubAgentSnapshot
		err      error
	}
	done := make(chan outcome, 1)
	go func() { snapshot, err := manager.Wait(deadline, sa.ID); done <- outcome{snapshot, err} }()
	select {
	case <-stopFinishing:
	case result := <-done:
		promptClient.mu.Lock()
		calls := len(promptClient.scopes)
		promptClient.mu.Unlock()
		t.Fatalf("worker became terminal before stop receipt cleanup: status=%s hook model calls=%d err=%v", result.snapshot.Status, calls, result.err)
	case <-deadline.Done():
		t.Fatal("worker never reached stop receipt cleanup")
	}
	sa.mu.Lock()
	status := sa.Status
	sa.mu.Unlock()
	if status != StatusRunning {
		t.Fatalf("worker published %s before stop cleanup", status)
	}
	close(releaseStop)
	select {
	case result := <-done:
		if result.err != nil || result.snapshot.Status != StatusFailed || !errors.Is(result.snapshot.Error, hooks.ErrBlocked) || !errors.Is(result.snapshot.Error, cleanupErr) || result.snapshot.Result != "worker output" {
			t.Fatalf("stop lost callback/cleanup error or partial result: %+v waitErr=%v", result.snapshot, result.err)
		}
	case <-deadline.Done():
		t.Fatal("worker did not finish after stop cleanup")
	}
	if len(scopes) != 3 || scopes[0] == parent || scopes[1] == parent || scopes[2] == parent || scopes[0] == scopes[1] || scopes[1] == scopes[2] || scopes[0] == scopes[2] {
		t.Fatalf("start, main and stop did not receive independent owners: %v", scopes)
	}
	if admissionJournals[0] != workerJournal || admissionJournals[2] != workerJournal {
		t.Fatal("lifecycle admission inherited the parent journal")
	}
	promptClient.mu.Lock()
	defer promptClient.mu.Unlock()
	if len(promptClient.scopes) != 2 || promptClient.scopes[0] != scopes[0] || promptClient.scopes[1] != scopes[2] || promptClient.journals[0] != workerJournal || promptClient.journals[1] != workerJournal {
		t.Fatalf("real prompt model escaped worker receipt/journal ownership: scopes=%v journals=%v", promptClient.scopes, promptClient.journals)
	}
	parentJournal.mu.Lock()
	defer parentJournal.mu.Unlock()
	workerJournal.mu.Lock()
	defer workerJournal.mu.Unlock()
	if len(parentJournal.operations) != 0 || len(workerJournal.operations) != 3 || workerJournal.operations[0].Kind != providers.InferenceOperationHookPrompt || workerJournal.operations[1].Kind != providers.InferenceOperationAgentRound || workerJournal.operations[2].Kind != providers.InferenceOperationHookPrompt {
		t.Fatalf("lifecycle writes used wrong journal: parent=%+v worker=%+v", parentJournal.operations, workerJournal.operations)
	}
}

func TestSpawn_UsesManagerDefaultRequestOptions(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "all done"}}
	mgr := NewManagerWithOptions(client, "worker-api-model", ManagerOptions{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

		DefaultEffort: "high",
		DefaultProviderOptions: map[string]any{
			"reasoningEffort": "high",
		},
	})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "worker",
		Prompt:  "do work",
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	req := client.lastRequest.Load()
	if req == nil {
		t.Fatal("expected a provider request")
	}
	if req.Model != "worker-api-model" || req.Effort != "high" {
		t.Fatalf("request did not use manager defaults: %+v", *req)
	}
	if got := req.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("ProviderOptions = %#v", req.ProviderOptions)
	}
}

// A worker turn must send the manager's default provider identity with every
// request and persist provider-native state (Responses item ids) stamped with
// that provider, so a later provider switch can recognize it as foreign.
func TestSpawn_StampsDefaultProviderOnNativeState(t *testing.T) {
	client := &fakeClient{
		response:       providers.ChatResponse{Content: "all done"},
		providerItemID: "resp-alpha-1",
	}
	mgr := NewManagerWithOptions(client, "worker-model", ManagerOptions{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), DefaultProviderName: "alpha"})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "do thing", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	req := client.lastRequest.Load()
	if req == nil {
		t.Fatal("expected a provider request")
	}
	if req.Provider != "alpha" {
		t.Fatalf("request Provider = %q, want %q", req.Provider, "alpha")
	}
	history, ok := mgr.History(sa.ID)
	if !ok {
		t.Fatalf("no history for %s", sa.ID)
	}
	assistant := lastAssistantMessageForTest(t, history)
	if assistant.ProviderItemID != "resp-alpha-1" {
		t.Fatalf("ProviderItemID = %q, want resp-alpha-1", assistant.ProviderItemID)
	}
	if assistant.ProviderItemProvider != "alpha" {
		t.Fatalf("ProviderItemProvider = %q, want %q", assistant.ProviderItemProvider, "alpha")
	}
	if assistant.ProviderItemModel != "worker-model" {
		t.Fatalf("ProviderItemModel = %q, want %q", assistant.ProviderItemModel, "worker-model")
	}
}

// A pinned spawn that carries its own client must stamp the pinned provider,
// not the manager default's.
func TestSpawn_PinnedClientStampsPinnedProvider(t *testing.T) {
	defaultClient := &fakeClient{response: providers.ChatResponse{Content: "unused"}}
	pinned := &fakeClient{
		response:       providers.ChatResponse{Content: "pinned done"},
		providerItemID: "resp-gamma-1",
	}
	mgr := NewManagerWithOptions(defaultClient, "worker-model", ManagerOptions{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), DefaultProviderName: "beta"})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:         "worker",
		Prompt:       "do thing",
		Toolkit:      fakeToolkit{},
		Model:        "pinned-model",
		Client:       pinned,
		ProviderName: "gamma",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	req := pinned.lastRequest.Load()
	if req == nil {
		t.Fatal("expected the pinned client to receive the request")
	}
	if req.Provider != "gamma" {
		t.Fatalf("request Provider = %q, want %q", req.Provider, "gamma")
	}
	history, _ := mgr.History(sa.ID)
	assistant := lastAssistantMessageForTest(t, history)
	if assistant.ProviderItemProvider != "gamma" {
		t.Fatalf("ProviderItemProvider = %q, want %q", assistant.ProviderItemProvider, "gamma")
	}
}

// A restored run resumed on the manager defaults must run under the defaults'
// provider identity, so the request-preparation machinery drops native state
// minted by a different provider instead of replaying its item ids.
func TestRestore_DefaultProviderDropsForeignItemIDs(t *testing.T) {
	client := &fakeClient{
		response:       providers.ChatResponse{Content: "resumed"},
		providerItemID: "resp-beta-9",
	}
	mgr := NewManagerWithOptions(client, "worker-model", ManagerOptions{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), DefaultProviderName: "beta"})

	sa, err := mgr.Restore(RestoreOptions{
		Run: PersistedRun{
			Version: ResumeSnapshotVersion,
			ID:      "wk-cross",
			Type:    "general-purpose",
			Status:  StatusCompleted,
			Model:   "worker-model",
			CWD:     os.TempDir(),
			Messages: []providers.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "do it"},
				{
					Role:                 "assistant",
					Content:              "earlier answer",
					ProviderItemID:       "resp-alpha-1",
					ProviderItemProvider: "alpha",
					ProviderItemModel:    "worker-model",
				},
			},
		},
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := mgr.Followup(context.Background(), sa.ID, "carry on"); err != nil {
		t.Fatalf("Followup: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	req := client.lastRequest.Load()
	if req == nil {
		t.Fatal("expected a provider request")
	}
	if req.Provider != "beta" {
		t.Fatalf("request Provider = %q, want defaults provider %q", req.Provider, "beta")
	}
	// Run the same request preparation a provider adapter applies: with the
	// provider identity stamped, the foreign item id must be dropped.
	prepared, err := providers.PrepareMessagesForProviderRequest(req.Provider, req.Model, req.Messages)
	if err != nil {
		t.Fatalf("PrepareMessagesForProviderRequest: %v", err)
	}
	for _, msg := range prepared {
		if msg.Role == "assistant" && msg.Content == "earlier answer" && msg.ProviderItemID != "" {
			t.Fatalf("foreign ProviderItemID %q survived request preparation", msg.ProviderItemID)
		}
	}
	// Contrapositive: with the provider identity missing (the pre-fix
	// state), the same preparation would have replayed the foreign id.
	unstamped, err := providers.PrepareMessagesForProviderRequest("", req.Model, req.Messages)
	if err != nil {
		t.Fatalf("PrepareMessagesForProviderRequest (unstamped): %v", err)
	}
	replayed := false
	for _, msg := range unstamped {
		if msg.Role == "assistant" && msg.ProviderItemID == "resp-alpha-1" {
			replayed = true
		}
	}
	if !replayed {
		t.Fatal("expected the unstamped preparation to replay the foreign item id (guards the test's premise)")
	}
	// The resume turn's own native state carries the defaults' provider.
	history, _ := mgr.History(sa.ID)
	assistant := lastAssistantMessageForTest(t, history)
	if assistant.ProviderItemID != "resp-beta-9" || assistant.ProviderItemProvider != "beta" {
		t.Fatalf("resumed native state = (%q, %q), want (resp-beta-9, beta)", assistant.ProviderItemID, assistant.ProviderItemProvider)
	}
}

func lastAssistantMessageForTest(t *testing.T, history []providers.ChatMessage) providers.ChatMessage {
	t.Helper()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return history[i]
		}
	}
	t.Fatal("no assistant message in history")
	return providers.ChatMessage{}
}

// scriptedStreamClient returns a queued sequence of responses, one per model
// round-trip, so a test can distinguish the main turn from a re-entry nudge
// turn. Once the queue is exhausted it returns an empty end_turn completion.
type scriptedStreamClient struct {
	mu        sync.Mutex
	responses []providers.ChatResponse
	calls     int
}

func (c *scriptedStreamClient) next() providers.ChatResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	var resp providers.ChatResponse
	if c.calls < len(c.responses) {
		resp = c.responses[c.calls]
	} else {
		resp = providers.ChatResponse{StopReason: "end_turn"}
	}
	c.calls++
	if strings.TrimSpace(resp.StopReason) == "" {
		resp.StopReason = "end_turn"
	}
	return resp
}

func (c *scriptedStreamClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *scriptedStreamClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return c.next(), nil
}

func (c *scriptedStreamClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	resp := c.next()
	ch := make(chan providers.StreamEvent, 4)
	go func() {
		defer close(ch)
		if resp.Content != "" {
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: resp.Content}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone, StopReason: resp.StopReason, Truncated: resp.Truncated}
	}()
	return ch, nil
}

func TestLastAssistantText_WalksBackToMostRecentText(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "the real answer"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{Name: "read_file"}}},
		{Role: "tool", Content: "file contents"},
		{Role: "assistant", Content: "   "},
	}
	if got := lastAssistantText(history); got != "the real answer" {
		t.Fatalf("tail-fallback walk = %q, want %q", got, "the real answer")
	}
	if got := lastAssistantText(nil); got != "" {
		t.Fatalf("empty history should yield empty text, got %q", got)
	}
}

func TestRunTurn_EmptyOutputNudgeRecoversFinalSummary(t *testing.T) {
	// Main turn ends with no text; the single re-entry nudge then produces the
	// summary, which becomes the delivered result.
	client := &scriptedStreamClient{responses: []providers.ChatResponse{
		{Content: "", StopReason: "end_turn"},
		{Content: "final summary after nudge", StopReason: "end_turn"},
	}}
	var admissions, completions atomic.Int32
	mgr := NewManagerWithOptions(client, "fake-model", ManagerOptions{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
		BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
			admissions.Add(1)
			return ctx, func() error { completions.Add(1); return nil }, nil
		},
	})
	sa, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "do thing", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", snap.Status)
	}
	if snap.Result != "final summary after nudge" {
		t.Fatalf("nudge result = %q, want the nudged summary", snap.Result)
	}
	if client.callCount() != 2 {
		t.Fatalf("expected exactly one main turn plus one nudge turn, got %d model calls", client.callCount())
	}
	if admissions.Load() != 2 || completions.Load() != 2 {
		t.Fatalf("nudge reused prior receipt lifecycle: admissions=%d completions=%d", admissions.Load(), completions.Load())
	}
}

func TestRunTurnNudgePreservesReceiptFailure(t *testing.T) {
	for _, admission := range []bool{false, true} {
		t.Run(fmt.Sprintf("admission-%t", admission), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := &scriptedStreamClient{responses: []providers.ChatResponse{{StopReason: "end_turn"}, {Content: "accepted summary", StopReason: "end_turn"}}}
			journal := &managerTestJournal{}
			receiptErr := errors.New("nudge receipt required write failed")
			var admissions atomic.Int32
			manager := NewManagerWithOptions(client, "test", ManagerOptions{
				AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
				InferenceJournal:  journal,
				BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
					ordinal := admissions.Add(1)
					if admission && ordinal == 2 {
						return ctx, nil, receiptErr
					}
					return ctx, func() error {
						if ordinal == 2 {
							return receiptErr
						}
						return nil
					}, nil
				},
			})
			historyPath := filepath.Join(t.TempDir(), "worker.json")
			sa, err := manager.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}, HistoryPath: historyPath})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := manager.Wait(ctx, sa.ID)
			if err != nil || snapshot.Status != StatusFailed || !errors.Is(snapshot.Error, receiptErr) || !errors.Is(snapshot.Error, agent.ErrModelReceiptLifecycle) {
				t.Fatalf("nudge swallowed required receipt failure: status=%s workerErr=%v waitErr=%v", snapshot.Status, snapshot.Error, err)
			}
			wantCalls, wantResult := 2, "accepted summary"
			if admission {
				wantCalls, wantResult = 1, ""
			}
			if admissions.Load() != 2 || client.callCount() != wantCalls || snapshot.Result != wantResult || lastAssistantText(sa.HistorySnapshot()) != wantResult {
				t.Fatalf("nudge lost accepted output: admissions=%d calls=%d result=%q history=%+v", admissions.Load(), client.callCount(), snapshot.Result, sa.HistorySnapshot())
			}
			persisted, err := LoadPersistedRun(historyPath)
			if err != nil || persisted.Status != StatusFailed || lastAssistantText(persisted.Messages) != wantResult {
				t.Fatalf("nudge failed to persist accepted history: status=%s history=%+v err=%v", persisted.Status, persisted.Messages, err)
			}
			journal.mu.Lock()
			defer journal.mu.Unlock()
			if len(journal.workflowCompletions) != 1 || journal.workflowCompletions[0].Outcome != providers.InferenceOutcomeFailed {
				t.Fatalf("nudge falsely completed worker workflow: %+v", journal.workflowCompletions)
			}
		})
	}
}

type failedNudgeClient struct{ scriptedStreamClient }

func (c *failedNudgeClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if c.callCount() == 1 {
		c.next()
		return nil, &providers.HTTPError{StatusCode: 400, Body: "summary generation rejected"}
	}
	return c.scriptedStreamClient.StreamChat(ctx, req)
}

func TestRunTurnNudgeModelFailureRemainsBestEffort(t *testing.T) {
	client := &failedNudgeClient{}
	manager := NewManagerWithOptions(client, "test", ManagerOptions{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
		BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
			return ctx, func() error { return nil }, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sa, err := manager.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Wait(ctx, sa.ID)
	if err != nil || snapshot.Status != StatusCompleted || snapshot.Error != nil || snapshot.Result != emptyWorkerResultPlaceholder || client.callCount() != 2 {
		t.Fatalf("ordinary nudge failure stopped being best-effort: %+v calls=%d waitErr=%v", snapshot, client.callCount(), err)
	}
}

func TestFollowupReceiptFailureSurvivesContinuations(t *testing.T) {
	for _, stage := range []string{"nudge", "main", "start", "stop", "ordinary-stop"} {
		for _, admission := range []bool{false, true} {
			if stage == "ordinary-stop" && admission {
				continue
			}
			t.Run(fmt.Sprintf("%s/admission-%t", stage, admission), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				failureReady, releaseFailure := make(chan struct{}), make(chan struct{})
				continuationReady, releaseContinuation := make(chan struct{}), make(chan struct{})
				unblockFailure := sync.OnceFunc(func() { close(releaseFailure) })
				unblockContinuation := sync.OnceFunc(func() { close(releaseContinuation) })
				defer unblockFailure()
				defer unblockContinuation()
				receiptErr := errors.New("required receipt failed before queued continuation")
				failAt, firstCalls := int32(1), 1
				responses := []providers.ChatResponse{{Content: "accepted first output"}}
				if stage == "nudge" {
					failAt, firstCalls = 2, 2
					responses = append([]providers.ChatResponse{{StopReason: "end_turn"}}, responses...)
				}
				if stage == "stop" || stage == "ordinary-stop" {
					failAt = 2
				}
				if stage == "start" || (admission && stage == "main") {
					firstCalls, responses = 0, nil
				} else if admission && stage == "nudge" {
					firstCalls, responses = 1, responses[:1]
				}
				responses = append(responses, providers.ChatResponse{Content: "first continuation done"}, providers.ChatResponse{Content: "second continuation done"}, providers.ChatResponse{Content: "explicit retry done"})
				client := &scriptedStreamClient{responses: responses}
				var admissions atomic.Int32
				var continuationGated atomic.Bool
				fail := func() error {
					close(failureReady)
					select {
					case <-releaseFailure:
						return receiptErr
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				opts := ManagerOptions{
					AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
					BeginModelReceipts: func(runCtx context.Context) (context.Context, func() error, error) {
						ordinal := admissions.Add(1)
						if stage != "ordinary-stop" && ordinal == failAt && admission {
							return runCtx, nil, fail()
						}
						return runCtx, func() error {
							if stage != "ordinary-stop" && ordinal == failAt {
								return fail()
							}
							if client.callCount() == firstCalls+1 && continuationGated.CompareAndSwap(false, true) {
								close(continuationReady)
								select {
								case <-releaseContinuation:
								case <-ctx.Done():
									return ctx.Err()
								}
							}
							return nil
						}, nil
					},
				}
				if stage == "start" {
					opts.OnSubagentStart = func(context.Context, string) error { return nil }
				}
				if stage == "stop" {
					opts.OnSubagentStop = func(context.Context, string) error { return nil }
				}
				if stage == "ordinary-stop" {
					var stopped atomic.Bool
					opts.OnSubagentStop = func(context.Context, string) error {
						if stopped.CompareAndSwap(false, true) {
							return fail()
						}
						return nil
					}
				}
				manager := NewManagerWithOptions(client, "test", opts)
				historyPath := filepath.Join(t.TempDir(), "worker.json")
				sa, err := manager.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}, HistoryPath: historyPath})
				if err != nil {
					t.Fatal(err)
				}
				for i, ready := range []chan struct{}{failureReady, continuationReady} {
					select {
					case <-ready:
					case <-ctx.Done():
						t.Fatalf("turn %d never reached receipt gate: %v", i, ctx.Err())
					}
					snapshot, err := manager.Followup(ctx, sa.ID, fmt.Sprintf("queued instruction %d", i))
					if err != nil || snapshot.Status != StatusRunning || snapshot.PendingMessageCount != 1 {
						t.Fatalf("Followup did not queue at receipt gate: %+v err=%v", snapshot, err)
					}
					if i == 0 {
						unblockFailure()
					} else {
						unblockContinuation()
					}
				}
				snapshot, err := manager.Wait(ctx, sa.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stage == "ordinary-stop" {
					if snapshot.Status != StatusCompleted || snapshot.Error != nil {
						t.Fatalf("ordinary hook error prevented queued recovery: %+v", snapshot)
					}
				} else if snapshot.Status != StatusFailed || !errors.Is(snapshot.Error, receiptErr) || !errors.Is(snapshot.Error, agent.ErrModelReceiptLifecycle) {
					t.Errorf("continuation lost required receipt error: status=%s err=%v", snapshot.Status, snapshot.Error)
				}
				if client.callCount() != firstCalls+2 || snapshot.Result != "second continuation done" || snapshot.PendingMessageCount != 0 {
					t.Fatalf("queued work did not complete: calls=%d snapshot=%+v", client.callCount(), snapshot)
				}
				persisted, err := LoadPersistedRun(historyPath)
				if err != nil || persisted.Status != snapshot.Status || persisted.Result != snapshot.Result {
					t.Fatalf("persisted terminal differs: %+v err=%v", persisted, err)
				}
				if stage != "ordinary-stop" && !strings.Contains(persisted.Error, receiptErr.Error()) {
					t.Errorf("persisted terminal lost required error: %q", persisted.Error)
				}
				wantMessages := []string{"queued instruction 0", "first continuation done", "queued instruction 1", "second continuation done"}
				if firstCalls > 0 && !(admission && stage == "nudge") {
					wantMessages = append([]string{"accepted first output"}, wantMessages...)
				}
				for _, messages := range [][]providers.ChatMessage{sa.HistorySnapshot(), persisted.Messages} {
					remaining := wantMessages
					for _, message := range messages {
						if len(remaining) > 0 && message.Content == remaining[0] {
							remaining = remaining[1:]
						}
					}
					if len(remaining) != 0 {
						t.Fatalf("accepted history or queued input lost: missing=%v messages=%+v", remaining, messages)
					}
				}
				if _, err := manager.Followup(ctx, sa.ID, "explicit retry"); err != nil {
					t.Fatal(err)
				}
				retried, err := manager.Wait(ctx, sa.ID)
				if err != nil || retried.Status != StatusCompleted || retried.Error != nil || retried.Result != "explicit retry done" {
					t.Fatalf("explicit retry retained prior run error: %+v err=%v", retried, err)
				}
			})
		}
	}
}

func TestWorkerReceiptContextFailureRetainsIdentity(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			receiptErr := errors.New("receipt owner cleanup failed")
			manager := NewManagerWithOptions(&fakeClient{response: providers.ChatResponse{Content: "accepted output"}}, "test", ManagerOptions{
				AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),
				BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
					return ctx, func() error { return errors.Join(receiptErr, cause) }, nil
				},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sa, err := manager.Spawn(ctx, SpawnOptions{Type: "worker", Prompt: "work", Toolkit: fakeToolkit{}, MaxLifetime: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := manager.Wait(ctx, sa.ID)
			if err != nil || snapshot.Status == StatusCompleted || !errors.Is(snapshot.Error, receiptErr) || !errors.Is(snapshot.Error, cause) || !errors.Is(snapshot.Error, agent.ErrModelReceiptLifecycle) || snapshot.Result != "accepted output" {
				t.Fatalf("receipt failure identity lost at terminal: %+v waitErr=%v", snapshot, err)
			}
		})
	}
}

func TestRunTurn_EmptyOutputFallsBackToPlaceholder(t *testing.T) {
	// The worker produces nothing on the main turn and nothing on the nudge:
	// the result settles on the clearly-labelled placeholder, and the run is
	// still completed (empty output is not a runtime failure).
	client := &scriptedStreamClient{responses: []providers.ChatResponse{
		{Content: "", StopReason: "end_turn"},
		{Content: "", StopReason: "end_turn"},
	}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})
	sa, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "do thing", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusCompleted {
		t.Fatalf("empty output must stay completed, got %s", snap.Status)
	}
	if snap.Result != emptyWorkerResultPlaceholder {
		t.Fatalf("empty result = %q, want placeholder %q", snap.Result, emptyWorkerResultPlaceholder)
	}
	if client.callCount() != 2 {
		t.Fatalf("expected exactly one nudge (2 model calls), got %d", client.callCount())
	}
}

func TestSpawn_LLMError(t *testing.T) {
	client := &fakeClient{err: errors.New("boom")}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "worker",
		Prompt:  "do thing",
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := mgr.Wait(context.Background(), sa.ID)
	if snap.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", snap.Status)
	}
	if snap.Error == nil {
		t.Fatal("expected error to be set")
	}
}

func TestSpawn_Cancel(t *testing.T) {
	client := &fakeClient{
		response: providers.ChatResponse{Content: "ok"},
		delay:    2 * time.Second,
	}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "worker",
		Prompt:  "slow",
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Give the goroutine a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	if !mgr.Stop(sa.ID) {
		t.Fatal("Stop returned false")
	}

	snap, _ := mgr.Wait(context.Background(), sa.ID)
	if snap.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %s", snap.Status)
	}
}

func TestStopAll(t *testing.T) {
	client := &fakeClient{
		response: providers.ChatResponse{Content: "ok"},
		delay:    2 * time.Second,
	}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	for i := 0; i < 3; i++ {
		_, err := mgr.Spawn(context.Background(), SpawnOptions{
			Type:    "worker",
			Prompt:  "slow",
			Toolkit: fakeToolkit{},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if mgr.CountRunning() != 3 {
		t.Fatalf("expected 3 running, got %d", mgr.CountRunning())
	}

	mgr.StopAll()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.CountRunning() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mgr.CountRunning() != 0 {
		t.Fatalf("expected 0 running after StopAll, got %d", mgr.CountRunning())
	}
}

func TestStopAllConcurrentCancelReplacement(t *testing.T) {
	sa := &SubAgent{ID: "worker", cancelFunc: func() {}}
	mgr := &Manager{agents: map[string]*SubAgent{sa.ID: sa}}

	for i := 0; i < 100; i++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			mgr.StopAll()
		}()
		go func() {
			defer wg.Done()
			<-start
			sa.mu.Lock()
			sa.cancelFunc = func() {}
			sa.mu.Unlock()
		}()
		close(start)
		wg.Wait()
	}
}

func TestNotifications(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "ok"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	ch := make(chan Notification, 16)
	mgr.Subscribe(ch)

	sa, _ := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "explorer",
		Prompt:  "p",
		Toolkit: fakeToolkit{},
	})
	mgr.Wait(context.Background(), sa.ID)

	// Should have received: running + completed.
	statuses := []Status{}
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case n := <-ch:
			statuses = append(statuses, n.Status)
			if n.Status == StatusCompleted {
				break loop
			}
		case <-timeout:
			t.Fatalf("did not receive completed notification, got %v", statuses)
		}
	}
	if len(statuses) < 2 {
		t.Fatalf("expected at least 2 notifications, got %d: %v", len(statuses), statuses)
	}
}

func TestBroadcastSnapshotPublishesRunningUsage(t *testing.T) {
	mgr := NewManager(&fakeClient{}, "fake-model", agent.DefaultDriver{})
	ch := make(chan Notification, 1)
	mgr.Subscribe(ch)

	sa := &SubAgent{ID: "worker-1", Type: "worker", Status: StatusRunning, InputTokens: 12, OutputTokens: 7}
	mgr.BroadcastSnapshot(sa)

	select {
	case n := <-ch:
		if n.Status != StatusRunning {
			t.Fatalf("expected running status, got %s", n.Status)
		}
		if n.Snapshot.InputTokens != 12 || n.Snapshot.OutputTokens != 7 {
			t.Fatalf("unexpected usage snapshot: in=%d out=%d", n.Snapshot.InputTokens, n.Snapshot.OutputTokens)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected usage snapshot notification")
	}
}

func TestTerminalPrepareRunsBeforeFinalSnapshotPersistence(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "workers", "worker.json")
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	prepareEntered := make(chan Notification, 1)
	releasePrepare := make(chan struct{})
	mgr.SetTerminalPrepareObserver(func(notification Notification) error {
		prepareEntered <- notification
		<-releasePrepare
		return nil
	})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		ID:          "worker-terminal-prepare",
		Type:        "worker",
		Prompt:      "finish",
		Toolkit:     fakeToolkit{},
		HistoryPath: historyPath,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	select {
	case notification := <-prepareEntered:
		if notification.Status != StatusCompleted || notification.Snapshot.Result != "done" {
			t.Fatalf("prepared notification = %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal prepare observer was not called")
	}
	if _, err := os.Stat(historyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final snapshot became visible before terminal intent: %v", err)
	}

	close(releasePrepare)
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	run, err := LoadPersistedRun(historyPath)
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.Status != StatusCompleted || run.Result != "done" {
		t.Fatalf("persisted run = %+v", run)
	}
}

func TestPersistHistoryFailureMarksWorkerFailed(t *testing.T) {
	// An existing directory is a deterministic unwritable file target on every
	// supported platform; unlike chmod-based tests, it also fails when tests run
	// with elevated filesystem permissions.
	historyPath := t.TempDir()
	client := &fakeClient{response: providers.ChatResponse{Content: "completed work"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})
	notifications := make(chan Notification, 4)
	mgr.Subscribe(notifications)

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:        "worker",
		Description: "test persistence failure",
		Prompt:      "do it",
		Toolkit:     fakeToolkit{},
		HistoryPath: historyPath,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", snap.Status)
	}
	if snap.Error == nil || !strings.Contains(snap.Error.Error(), "persist final worker snapshot") {
		t.Fatalf("snapshot error = %v, want persistence failure", snap.Error)
	}
	if snap.Result != "completed work" {
		t.Fatalf("result = %q, want completed worker output preserved", snap.Result)
	}

	for {
		select {
		case notification := <-notifications:
			if notification.Status == StatusCompleted {
				t.Fatalf("persistence failure emitted completed notification: %+v", notification)
			}
			if !IsTerminal(notification.Status) {
				continue
			}
			if notification.Status != StatusFailed || notification.Snapshot.Status != StatusFailed {
				t.Fatalf("terminal notification status mismatch: %+v", notification)
			}
			if notification.Snapshot.Error == nil || !strings.Contains(notification.Snapshot.Error.Error(), "persist final worker snapshot") {
				t.Fatalf("terminal notification missing persistence error: %+v", notification)
			}
			return
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for persistence failure notification")
		}
	}
}

// TestPersistHistoryRecordsResumeFields verifies a persisted snapshot
// carries every field needed to rebuild the runtime for a cross-restart
// resume: identity, thread placement, working directory, and model pin.
func TestPersistHistoryRecordsResumeFields(t *testing.T) {
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "workers", "worker.json")
	client := &fakeClient{response: providers.ChatResponse{Content: "ok"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		ID:            "worker-resume",
		ParticipantID: "prt-1",
		Type:          "worker",
		TaskName:      "resume_task",
		AgentProfile:  "qa workflow",
		AgentPath:     "/root/resume_task",
		ParentID:      "sess-1",
		Description:   "resume me",
		Prompt:        "do the work",
		SystemPrompt:  "you are a worker",
		Toolkit:       fakeToolkit{},
		Model:         "pinned-model",
		ModelPin:      "alt-provider:pinned-model",
		WorkerRoot:    dir,
		HistoryPath:   historyPath,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	run, err := LoadPersistedRun(historyPath)
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.Version != ResumeSnapshotVersion {
		t.Fatalf("version = %d, want %d", run.Version, ResumeSnapshotVersion)
	}
	if run.ParticipantID != "prt-1" || run.Type != "worker" || run.TaskName != "resume_task" ||
		run.AgentPath != "/root/resume_task" || run.ParentID != "sess-1" || run.AgentProfile != "qa workflow" {
		t.Fatalf("missing rebuild metadata: %+v", run)
	}
	if run.CWD != dir || run.Model != "pinned-model" || run.ModelPin != "alt-provider:pinned-model" {
		t.Fatalf("missing runtime fields: %+v", run)
	}
	if run.Status != StatusCompleted || len(run.Messages) == 0 {
		t.Fatalf("expected completed snapshot with history, got %+v", run)
	}
}

// TestLoadPersistedRunToleratesPreVersionSnapshot proves an old snapshot
// written before resume support loads without crashing, reporting
// version 0 and empty new fields (the resume gate lives at resume time,
// not load time).
func TestLoadPersistedRunToleratesPreVersionSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	legacy := `{
		"id": "worker-legacy",
		"type": "worker",
		"task_name": "legacy_task",
		"status": "failed",
		"model": "old-model",
		"prompt": "do it",
		"error": "boom",
		"messages": [{"role":"system","content":"sys"},{"role":"user","content":"do it"}]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := LoadPersistedRun(path)
	if err != nil {
		t.Fatalf("LoadPersistedRun on legacy snapshot: %v", err)
	}
	if run.Version != 0 {
		t.Fatalf("legacy snapshot version = %d, want 0", run.Version)
	}
	if run.ID != "worker-legacy" || run.Status != StatusFailed || len(run.Messages) != 2 {
		t.Fatalf("legacy snapshot did not parse: %+v", run)
	}
	if run.CWD != "" || run.ParticipantID != "" {
		t.Fatalf("legacy snapshot should leave new fields empty: %+v", run)
	}
}

func TestSpawn_RequiresToolkitAndPrompt(t *testing.T) {
	mgr := NewManager(&fakeClient{}, "m", agent.DefaultDriver{})

	_, err := mgr.Spawn(context.Background(), SpawnOptions{Prompt: "x"})
	if err == nil {
		t.Error("expected error for missing toolkit")
	}
	_, err = mgr.Spawn(context.Background(), SpawnOptions{Toolkit: fakeToolkit{}})
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

// TestSpawn_WithInitialHistory_PrefixIsParentHistory verifies the
// fork code path in manager.run: when InitialHistory is non-nil, the
// worker's first request to the LLM should start with that exact
// history (preserving prompt-cache compatibility) and end with the
// caller's Prompt as the final user message.
func TestSpawn_WithInitialHistory_PrefixIsParentHistory(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "fork done"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	parentHistory := []providers.ChatMessage{
		{Role: "system", Content: "you are the parent agent"},
		{Role: "user", Content: "fix the bug"},
		{
			Role:      "assistant",
			Content:   "let me read the file",
			ToolCalls: []providers.ToolCall{{ID: "call_read", Name: "read_file", Arguments: `{}`}},
		},
		{Role: "tool", Name: "read_file", ToolCallID: "call_read", Content: "file contents"},
	}

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:           "fork",
		Prompt:         "<system-reminder>do the thing</system-reminder>",
		Toolkit:        fakeToolkit{},
		InitialHistory: parentHistory,
		// SystemPrompt intentionally left empty — the fork path
		// uses the system message in InitialHistory[0] instead.
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s (err=%v)", snap.Status, snap.Error)
	}

	last := client.lastRequest.Load()
	if last == nil {
		t.Fatal("client never received a request")
	}
	got := visibleMessagesForTest(last.Messages)
	if len(got) != len(parentHistory)+1 {
		t.Fatalf("expected %d messages (parent history + 1 user), got %d",
			len(parentHistory)+1, len(got))
	}
	for i, want := range parentHistory {
		if got[i].Role != want.Role || got[i].Content != want.Content {
			t.Errorf("message %d: got {%s,%s}, want {%s,%s}",
				i, got[i].Role, got[i].Content, want.Role, want.Content)
		}
	}
	tail := got[len(got)-1]
	if tail.Role != "user" {
		t.Errorf("expected final message to be user, got %s", tail.Role)
	}
	if tail.Content != "<system-reminder>do the thing</system-reminder>" {
		t.Errorf("expected fork prompt as final message, got %q", tail.Content)
	}
}

func TestFollowup_CompletedAgentStartsNewTurnWithHistory(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "turn done"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:         "worker",
		Prompt:       "initial task",
		SystemPrompt: "you are a worker",
		Toolkit:      fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if ok := mgr.QueueMessage(sa.ID, "queued mailbox note"); !ok {
		t.Fatal("QueueMessage returned false")
	}

	snap, err := mgr.Followup(context.Background(), sa.ID, "continue task")
	if err != nil {
		t.Fatalf("Followup: %v", err)
	}
	if snap.Status != StatusRunning {
		t.Fatalf("expected follow-up turn running, got %s", snap.Status)
	}
	final, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait follow-up: %v", err)
	}
	if final.Status != StatusCompleted {
		t.Fatalf("expected completed follow-up, got %s", final.Status)
	}
	if client.calls.Load() != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", client.calls.Load())
	}

	last := client.lastRequest.Load()
	if last == nil {
		t.Fatal("client never received follow-up request")
	}
	want := []struct {
		role    string
		content string
	}{
		{"system", "you are a worker"},
		{"user", "initial task"},
		{"assistant", "turn done"},
		{"user", "queued mailbox note"},
		{"user", "continue task"},
	}
	visible := visibleMessagesForTest(last.Messages)
	if len(visible) != len(want) {
		t.Fatalf("expected %d visible messages in follow-up request, got %+v", len(want), last.Messages)
	}
	for i, msg := range want {
		if visible[i].Role != msg.role || visible[i].Content != msg.content {
			t.Fatalf("message %d = {%s,%q}, want {%s,%q}", i, visible[i].Role, visible[i].Content, msg.role, msg.content)
		}
	}
}

// TestFollowup_FailedAgentResumesWithHistory locks the fall-through in
// Manager.Followup: a run that ended in StatusFailed keeps its full
// conversation and resumes in place on a follow-up. Any queued mailbox
// messages must be replayed ahead of the new instruction.
func TestFollowup_FailedAgentResumesWithHistory(t *testing.T) {
	client := newResumeClient("fail", providers.ChatResponse{Content: "resumed done"})
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:         "worker",
		Prompt:       "initial task",
		SystemPrompt: "you are a worker",
		Toolkit:      fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusFailed || snap.Error == nil {
		t.Fatalf("expected failed first turn, got %s (err=%v)", snap.Status, snap.Error)
	}

	// Queue a mailbox note, then resume. The queued message must batch
	// ahead of the new follow-up instruction.
	if ok := mgr.QueueMessage(sa.ID, "queued mailbox note"); !ok {
		t.Fatal("QueueMessage returned false")
	}
	client.arm("")

	resumed, err := mgr.Followup(context.Background(), sa.ID, "continue task")
	if err != nil {
		t.Fatalf("Followup on failed agent: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Fatalf("expected resume turn running, got %s", resumed.Status)
	}
	final, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait resume: %v", err)
	}
	if final.Status != StatusCompleted || final.Result != "resumed done" {
		t.Fatalf("expected completed resume, got %s result=%q", final.Status, final.Result)
	}

	last := client.lastRequest.Load()
	if last == nil {
		t.Fatal("client never recorded a resume request")
	}
	want := []struct {
		role    string
		content string
	}{
		{"system", "you are a worker"},
		{"user", "initial task"},
		{"user", "queued mailbox note"},
		{"user", "continue task"},
	}
	visible := visibleMessagesForTest(last.Messages)
	if len(visible) != len(want) {
		t.Fatalf("expected %d visible resume messages, got %+v", len(want), last.Messages)
	}
	for i, msg := range want {
		if visible[i].Role != msg.role || visible[i].Content != msg.content {
			t.Fatalf("resume message %d = {%s,%q}, want {%s,%q}", i, visible[i].Role, visible[i].Content, msg.role, msg.content)
		}
	}
}

// TestFollowup_CancelledAgentResumesWithHistory proves that a user-stopped
// (cancelled) run is resumable: a follow-up after cancellation is treated
// as an explicit revive request, not a hard rejection.
func TestFollowup_CancelledAgentResumesWithHistory(t *testing.T) {
	client := newResumeClient("block", providers.ChatResponse{Content: "resumed done"})
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:         "worker",
		Prompt:       "initial task",
		SystemPrompt: "you are a worker",
		Toolkit:      fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	<-client.started // wait until the first turn is mid-flight
	if !mgr.Stop(sa.ID) {
		t.Fatal("Stop returned false")
	}
	snap, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snap.Status != StatusCancelled {
		t.Fatalf("expected cancelled, got %s", snap.Status)
	}

	client.arm("")
	resumed, err := mgr.Followup(context.Background(), sa.ID, "resume after stop")
	if err != nil {
		t.Fatalf("Followup on cancelled agent: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Fatalf("expected resume running, got %s", resumed.Status)
	}
	final, err := mgr.Wait(context.Background(), sa.ID)
	if err != nil {
		t.Fatalf("Wait resume: %v", err)
	}
	if final.Status != StatusCompleted || final.Result != "resumed done" {
		t.Fatalf("expected completed resume, got %s result=%q", final.Status, final.Result)
	}

	last := client.lastRequest.Load()
	if last == nil {
		t.Fatal("client never recorded a resume request")
	}
	visible := visibleMessagesForTest(last.Messages)
	if len(visible) != 3 {
		t.Fatalf("expected [system, user, user] resume history, got %+v", last.Messages)
	}
	if visible[0].Role != "system" || visible[1].Content != "initial task" {
		t.Fatalf("resume history lost prior turn: %+v", visible)
	}
	tail := visible[len(visible)-1]
	if tail.Role != "user" || tail.Content != "resume after stop" {
		t.Fatalf("expected resume instruction as final message, got %+v", tail)
	}
}

func TestQueueMessageBroadcastsPendingMessageSnapshot(t *testing.T) {
	client := &fakeClient{delay: time.Hour}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "explorer",
		Prompt:  "initial task",
		Toolkit: fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer mgr.StopAll()

	deadline := time.Now().Add(time.Second)
	for client.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not enter model request")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ch := make(chan Notification, 4)
	mgr.Subscribe(ch)
	defer mgr.Unsubscribe(ch)

	if ok := mgr.QueueMessage(sa.ID, "queued mailbox note"); !ok {
		t.Fatal("QueueMessage returned false")
	}

	select {
	case n := <-ch:
		if n.AgentID != sa.ID || n.Status != StatusRunning || n.Snapshot.PendingMessageCount != 1 {
			t.Fatalf("unexpected queue notification: %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue notification")
	}
}

func TestQueueMessageTrimAndDrainToUserMessages(t *testing.T) {
	sa := &SubAgent{}
	sa.pushPendingMessage("  hello  ")
	sa.pushPendingMessage("\t\n") // ignored
	sa.pushPendingMessage("world")

	msgs := sa.popPendingUserMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 drained messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("unexpected first drained message: %+v", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "world" {
		t.Fatalf("unexpected second drained message: %+v", msgs[1])
	}
	if got := sa.pendingCount(); got != 0 {
		t.Fatalf("expected queue drained, pending=%d", got)
	}
}

func TestFollowupAtTerminalBoundaryContinuesInsteadOfStrandingMessage(t *testing.T) {
	client := &terminalBoundaryClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		secondSeen:   make(chan providers.ChatRequest, 1),
	}
	manager := NewManager(client, "boundary-model", agent.DefaultDriver{})
	agent, err := manager.Spawn(context.Background(), SpawnOptions{
		ID:       "terminal-boundary-worker",
		Type:     "worker",
		TaskName: "terminal_boundary",
		Prompt:   "finish one step",
		Toolkit:  fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-client.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	if _, err := manager.Followup(context.Background(), agent.ID, "late boundary message"); err != nil {
		t.Fatalf("Followup: %v", err)
	}
	close(client.releaseFirst)

	select {
	case req := <-client.secondSeen:
		found := false
		for _, message := range req.Messages {
			if message.Role == "user" && message.Content == "late boundary message" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("continuation request omitted late boundary message: %+v", req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late boundary message did not start a continuation turn")
	}
	snapshot, err := manager.Wait(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if snapshot.Status != StatusCompleted || snapshot.Result != "turn 2 done" {
		t.Fatalf("terminal snapshot = %+v, want completed continuation", snapshot)
	}
	if got := agent.pendingCount(); got != 0 {
		t.Fatalf("terminal worker retained %d pending messages", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestFollowupForcingTool_PinsFirstRequest verifies that a forced-tool
// follow-up pins the closing turn's first request to the named tool and that
// the force is one-shot: a later plain follow-up runs unforced.
func TestFollowupForcingTool_PinsFirstRequest(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "done"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:        "worker",
		Description: "test task",
		Prompt:      "do it",
		Toolkit:     fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	mgr.Wait(context.Background(), sa.ID)

	if _, err := mgr.FollowupForcingTool(context.Background(), sa.ID, "file your report", "agent_report"); err != nil {
		t.Fatalf("FollowupForcingTool: %v", err)
	}
	mgr.Wait(context.Background(), sa.ID)
	req := client.lastRequest.Load()
	if req == nil || req.ForceToolName != "agent_report" {
		t.Fatalf("expected forced tool on the closing turn's request, got %+v", req)
	}

	if _, err := mgr.Followup(context.Background(), sa.ID, "one more thing"); err != nil {
		t.Fatalf("Followup: %v", err)
	}
	mgr.Wait(context.Background(), sa.ID)
	req = client.lastRequest.Load()
	if req == nil || req.ForceToolName != "" {
		t.Fatalf("plain follow-up must run unforced, got %+v", req)
	}
}

// TestFailedRunSalvagesPartialResult locks the partial-result contract: when
// a turn fails after earlier turns produced text, the failed snapshot still
// carries the most recent assistant text so the parent sees how far the
// worker got alongside the error and the resume hint.
func TestFailedRunSalvagesPartialResult(t *testing.T) {
	client := newResumeClient("", providers.ChatResponse{Content: "found the bug in parser.go"})
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:        "worker",
		Description: "test task",
		Prompt:      "find the bug",
		Toolkit:     fakeToolkit{},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	mgr.Wait(context.Background(), sa.ID)

	client.arm("fail")
	if _, err := mgr.Followup(context.Background(), sa.ID, "now fix it"); err != nil {
		t.Fatalf("Followup: %v", err)
	}
	mgr.Wait(context.Background(), sa.ID)

	snap := mgr.Get(sa.ID).Snapshot()
	if snap.Status != StatusFailed {
		t.Fatalf("expected failed run, got %s", snap.Status)
	}
	if !contains(snap.Result, "found the bug in parser.go") {
		t.Fatalf("failed run should salvage the partial result, got %q", snap.Result)
	}
}
