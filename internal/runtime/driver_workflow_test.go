package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// Run through scripts/workflow-driver-e2e.mjs, which installs a packed public
// SDK and the example in an independent directory before invoking this check.
func TestWorkflowDriverProcess(t *testing.T) {
	for _, profile := range []string{"workflow", "default"} {
		t.Run(profile, func(t *testing.T) { testWorkflowDriverProcess(t, profile) })
	}
}

func TestStreamingDriverProcess(t *testing.T) {
	fixture := os.Getenv("WUU_WORKFLOW_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/workflow-driver-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root, dir := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := testRuntimePlugin("streaming-evidence")
	provider.Root, provider.Runtime.Command = fixture, node
	provider.Runtime.Args = []string{"streaming.ts"}
	modules := []string{kernelHostModule, sessionExecutionConsumer, provider.ID}
	host, kernel, err := buildPluginHost([]pluginpkg.Plugin{provider}, root, "streaming", t.TempDir(), t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := activatePluginHost(ctx, host); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(dir, "streaming-session"); err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(dir, "streaming-session")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	tools := &workflowEvidenceTools{path: path, started: started}
	model := &workflowEvidenceModel{waitForTool: started}
	runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, ToolLedger: ledger,
		LoopDriver:            resolveLoopDriver("default", host, kernel.driverGateways),
		DriverCheckpointStore: sessionDriverCheckpointStore{sessDir: dir, sessionID: "streaming-session"}}
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "streaming-session", ExecutionID: "streaming-first"})
	result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Read evidence"}}, nil)
	if err != nil || result.Content != "42" || tools.calls != 1 || len(model.requests) != 1 || len(result.NewMessages) != 2 || result.NewMessages[1].ToolInvocationID == "" {
		t.Fatalf("external streaming failed: result=%+v effects=%d requests=%d err=%v", result, tools.calls, len(model.requests), err)
	}
	for _, status := range host.Statuses() {
		if status.ID == loopdriver.DefaultDriverID {
			t.Fatal("host default driver remained loaded")
		}
	}
	t.Log("independent SDK driver: real file read began before model completion; joined one durable tool receipt; default loop absent")
}

func testWorkflowDriverProcess(t *testing.T, profile string) {
	fixture := os.Getenv("WUU_WORKFLOW_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/workflow-driver-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "evidence.txt"), []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := testRuntimePlugin("evidence-workflow")
	provider.Root, provider.Runtime.Command = fixture, node
	provider.Runtime.Args = []string{"runtime.ts"}
	provider.Runtime.Env = map[string]string{"WUU_WORKFLOW_PROFILE": profile}
	selectedProfile := profile
	var selection *composition.Config
	if profile == "default" {
		selectedProfile = ""
		modules := []string{kernelHostModule, sessionExecutionConsumer, provider.ID}
		selection = &composition.Config{Version: composition.Version, Modules: &modules}
	}
	host, kernel, err := buildPluginHost([]pluginpkg.Plugin{provider}, root, "workflow", t.TempDir(), t.TempDir(), nil, startPluginClient, nil, nil, selection, selectedProfile)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := activatePluginHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	generation := &PluginGeneration{host: host, driverGateways: kernel.driverGateways}
	generation.retain()
	owner := &Session{DriverProfile: selectedProfile, pluginGeneration: generation}
	defer owner.releasePluginGeneration(generation)
	acquire := owner.driverResolver(generation)
	if owner, ok := host.ServiceRegistry().ProviderPluginID("driver."+profile, 1); !ok || owner != provider.ID {
		t.Fatalf("workflow binding = %q, %v", owner, ok)
	}
	if profile == "default" {
		for _, status := range host.Statuses() {
			if status.ID == loopdriver.DefaultDriverID {
				t.Fatal("default implementation remained loaded in replacement composition")
			}
		}
	}
	sessDir := t.TempDir()
	if _, err := session.Create(sessDir, "workflow-session"); err != nil {
		t.Fatal(err)
	}
	store := sessionDriverCheckpointStore{sessDir: sessDir, sessionID: "workflow-session"}
	ledger, err := toolledger.New(sessDir, "workflow-session")
	if err != nil {
		t.Fatal(err)
	}
	tools := &workflowEvidenceTools{path: filepath.Join(root, "evidence.txt")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &workflowEvidenceModel{cancelSynthesis: cancel}
	runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, ToolLedger: ledger, AcquireLoopDriver: acquire, DriverBindingStore: store, DriverCheckpointStore: store}
	history := []providers.ChatMessage{{Role: "user", Content: "A previous task"}, {Role: "assistant", Content: "Previous result"}, {Role: "user", Content: "Read evidence.txt and report the measured value."}}
	attachWorkflowHistory(t, runner, sessDir, "workflow-session", history)
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "workflow-session", ExecutionID: "workflow-first"})
	partial, err := runner.RunWithCallback(ctx, history, nil)
	if err == nil || tools.calls != 1 {
		t.Fatalf("interruption lost tool effects: result=%+v calls=%d err=%v", partial, tools.calls, err)
	}
	var state struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(partial.DriverCheckpoint, &state); err != nil || state.Phase != "synthesis" {
		t.Fatalf("interruption overwrote the last durable checkpoint: state=%s err=%v", partial.DriverCheckpoint, err)
	}
	if !partial.HistoryRewritten || len(partial.NewMessages) != 3 || partial.NewMessages[2].ToolInvocationID == "" || len(partial.DurableNewMessages) != 0 {
		t.Fatalf("interruption lost the committed focused context: %+v", partial)
	}
	originals, err := session.ReadHistoryPage(context.Background(), sessDir, "workflow-session", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(originals.Records) != 5 || originals.Records[0].Content != "A previous task" {
		t.Fatalf("context replacement removed or duplicated durable originals: %+v", originals)
	}
	// Match the application finalizer: saving the reported checkpoint must not
	// overwrite the newer state already saved by a callback before interruption.
	if err := store.Save(context.Background(), loopdriver.Checkpoint{ContractVersion: partial.DriverContractVersion, DriverID: partial.DriverID, DriverVersion: partial.DriverVersion, State: partial.DriverCheckpoint}); err != nil {
		t.Fatal(err)
	}
	restored, err := session.LoadProviderHistorySnapshot(sessDir, "workflow-session")
	if err != nil {
		t.Fatal(err)
	}
	history = workflowRecordsToMessages(t, restored.Records)
	if len(history) != 3 {
		t.Fatalf("restart did not load the focused context: %+v", history)
	}
	resumedModel := &workflowEvidenceModel{synthesisOnly: true}
	runner.Client = resumedModel
	// A changed application default cannot reroute the saved workflow.
	owner.DriverProfile = "unavailable"
	resumeCtx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "workflow-session", ExecutionID: "workflow-resume"})
	result, err := runner.RunWithCallback(resumeCtx, history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "The measured value is 42." || result.DriverID != "example.evidence-workflow" || tools.calls != 1 {
		t.Fatalf("workflow failed to resume independently: %+v effects=%d", result, tools.calls)
	}
	if len(resumedModel.requests) != 1 || len(resumedModel.requests[0].Tools) != 0 {
		t.Fatal("resume repeated planning or allowed synthesis to run tools")
	}
	if _, ok := kernel.driverGateways.lookup("workflow-first"); ok {
		t.Fatal("canceled execution retained its gateway")
	}
	if _, ok := kernel.driverGateways.lookup("workflow-resume"); ok {
		t.Fatal("completed execution retained its gateway")
	}
	t.Run("driver-decides-overflow-recovery", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := session.Create(dir, "overflow-session"); err != nil {
			t.Fatal(err)
		}
		store := sessionDriverCheckpointStore{sessDir: dir, sessionID: "overflow-session"}
		model := &workflowEvidenceModel{overflowFirst: true}
		tools := &workflowEvidenceTools{path: filepath.Join(root, "evidence.txt")}
		owner.DriverProfile = selectedProfile
		runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, AcquireLoopDriver: acquire, DriverBindingStore: store, DriverCheckpointStore: store}
		history := []providers.ChatMessage{{Role: "user", Content: "Earlier task"}, {Role: "assistant", Content: "Earlier result"}, {Role: "user", Content: "Read evidence.txt and report the measured value."}}
		attachWorkflowHistory(t, runner, dir, "overflow-session", history)
		ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "overflow-session", ExecutionID: "overflow-recovery"})
		result, err := runner.RunWithCallback(ctx, history, nil)
		if err != nil || result.Content != "The measured value is 42." || tools.calls != 1 || len(model.requests) != 3 {
			t.Fatalf("external recovery lost control: result=%+v model=%d tools=%d err=%v", result, len(model.requests), tools.calls, err)
		}
		if len(model.requests[1].Messages) >= len(model.requests[0].Messages) {
			t.Fatal("driver retried the overflowing payload without shortening context")
		}
	})
	t.Run("resume-planned-before-history-flush", func(t *testing.T) {
		plannedDir := t.TempDir()
		if _, err := session.Create(plannedDir, "planned-session"); err != nil {
			t.Fatal(err)
		}
		plannedStore := sessionDriverCheckpointStore{sessDir: plannedDir, sessionID: "planned-session"}
		plannedLedger, err := toolledger.New(plannedDir, "planned-session")
		if err != nil {
			t.Fatal(err)
		}
		planningCtx, stopPlanning := context.WithCancel(context.Background())
		defer stopPlanning()
		plannedTools := &workflowEvidenceTools{path: filepath.Join(root, "evidence.txt")}
		owner.DriverProfile = selectedProfile
		plannedRunner := &agent.StreamRunner{Client: &workflowEvidenceModel{}, Model: "controlled-evidence", Tools: plannedTools, ToolLedger: plannedLedger, AcquireLoopDriver: acquire, DriverBindingStore: plannedStore,
			DriverCheckpointStore: workflowCheckpointBarrier{CheckpointStore: plannedStore, phase: "planned", cancel: stopPlanning}}
		originalHistory := []providers.ChatMessage{{Role: "user", Content: "Read evidence.txt and report the measured value."}}
		attachWorkflowHistory(t, plannedRunner, plannedDir, "planned-session", originalHistory)
		planningCtx = loopdriver.WithExecutionContext(planningCtx, loopdriver.ExecutionContext{SessionID: "planned-session", ExecutionID: "planning"})
		if _, err := plannedRunner.RunWithCallback(planningCtx, originalHistory, nil); err == nil || plannedTools.calls != 0 {
			t.Fatalf("planned checkpoint barrier did not precede effects: calls=%d err=%v", plannedTools.calls, err)
		}
		// Simulate a crash after checkpoint persistence but before history flush.
		// The request stored by the external driver contains its planned calls.
		plannedRunner.DriverCheckpointStore = plannedStore
		plannedModel := &workflowEvidenceModel{synthesisOnly: true}
		plannedRunner.Client = plannedModel
		plannedCtx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "planned-session", ExecutionID: "planned-resume"})
		result, err := plannedRunner.RunWithCallback(plannedCtx, originalHistory, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Content != "The measured value is 42." || plannedTools.calls != 1 || len(plannedModel.requests) != 1 || len(result.NewMessages) != 3 {
			t.Fatalf("planned recovery lost effects or history: result=%+v effects=%d requests=%d", result, plannedTools.calls, len(plannedModel.requests))
		}
		if err := providers.ValidateToolCallHistory(append(originalHistory, result.NewMessages...)); err != nil {
			t.Fatal(err)
		}
		t.Log("planned checkpoint recovered without a model replan; one real file read; complete tool history")
	})
	host.RetirePlugin(context.Background(), provider.ID, errors.New("workflow provider disabled"))
	if _, err := runner.RunWithCallback(resumeCtx, history, nil); err == nil || tools.calls != 1 || len(resumedModel.requests) != 1 {
		t.Fatalf("disabled workflow fell back to another loop: effects=%d requests=%d err=%v", tools.calls, len(resumedModel.requests), err)
	}
	t.Logf("external driver=%s; file evidence=42; tool effects=%d; first requests=%d; resume requests=%d; output=%q", result.DriverID, tools.calls, len(model.requests), len(resumedModel.requests), result.Content)
}

func attachWorkflowHistory(t *testing.T, runner *agent.StreamRunner, dir, id string, initial []providers.ChatMessage) {
	t.Helper()
	toRecords := func(messages []providers.ChatMessage) []session.HistoryRecord {
		raw, err := json.Marshal(modelwire.DriverMessagesToWire(messages))
		if err != nil {
			t.Fatal(err)
		}
		var records []session.HistoryRecord
		if err := json.Unmarshal(raw, &records); err != nil {
			t.Fatal(err)
		}
		return records
	}
	start, _, err := session.AppendHistoryRecordsReturningRange(dir, id, toRecords(initial))
	if err != nil {
		t.Fatal(err)
	}
	for i := range initial {
		initial[i].Seq = start + i
	}
	runner.ArchiveHistory = func(ctx context.Context, messages []providers.ChatMessage) (agent.HistoryArchive, error) {
		start, head, err := session.AppendHistoryRecordsReturningRange(dir, id, toRecords(messages))
		if err != nil {
			return agent.HistoryArchive{}, err
		}
		seqs := make([]int, len(messages))
		for i := range messages {
			seqs[i] = start + i
		}
		if len(messages) == 0 {
			snapshot, err := session.LoadProviderHistorySnapshot(dir, id)
			if err != nil {
				return agent.HistoryArchive{}, err
			}
			head = snapshot.HeadSeq
		}
		return agent.HistoryArchive{Seqs: seqs, HeadSeq: head}, nil
	}
	runner.CommitFreshContext = func(ctx context.Context, messages []providers.ChatMessage, head int, key string, note agent.CompactionNote) ([]providers.ChatMessage, int, error) {
		checkpoint, err := session.StoreContextWindow(dir, id, toRecords(messages), head, session.CompactionNote{ProviderKey: key})
		if err != nil {
			return nil, head, err
		}
		return workflowRecordsToMessages(t, checkpoint.Replacement), checkpoint.ThroughSeq, nil
	}
}

func workflowRecordsToMessages(t *testing.T, records []session.HistoryRecord) []providers.ChatMessage {
	t.Helper()
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	var messages []pluginprotocol.DriverMessageV1
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	return modelwire.DriverMessagesFromWire(messages)
}

type workflowCheckpointBarrier struct {
	loopdriver.CheckpointStore
	phase  string
	cancel context.CancelFunc
}

func (s workflowCheckpointBarrier) Save(ctx context.Context, checkpoint loopdriver.Checkpoint) error {
	if err := s.CheckpointStore.Save(ctx, checkpoint); err != nil {
		return err
	}
	var state struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(checkpoint.State, &state); err != nil {
		return err
	}
	if state.Phase == s.phase {
		s.cancel()
	}
	return nil
}

type workflowEvidenceTools struct {
	path    string
	calls   int
	started chan struct{}
}

func (t *workflowEvidenceTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_evidence", InputSchema: map[string]any{"type": "object"}}}
}
func (*workflowEvidenceTools) ToolMetadata(providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}
func (t *workflowEvidenceTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if call.Name != "read_evidence" {
		return "", fmt.Errorf("unexpected tool %s", call.Name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t.calls++
	if t.started != nil {
		close(t.started)
	}
	data, err := os.ReadFile(t.path)
	return string(data), err
}

type workflowEvidenceModel struct {
	mu              sync.Mutex
	requests        []providers.ChatRequest
	cancelSynthesis context.CancelFunc
	synthesisOnly   bool
	overflowFirst   bool
	waitForTool     <-chan struct{}
}

func (m *workflowEvidenceModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *workflowEvidenceModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	planningRound := 1
	if m.overflowFirst {
		planningRound = 2
		if len(m.requests) == 1 {
			return nil, providers.NewProviderStreamError("context_length_exceeded", "controlled context overflow")
		}
	}
	stream := make(chan providers.StreamEvent, 3)
	if !m.synthesisOnly && len(m.requests) == planningRound {
		if len(req.Tools) != 1 {
			return nil, errors.New("planning did not receive the selected tools")
		}
		call := providers.ToolCall{ID: "evidence-call", Name: "read_evidence", Arguments: "{}"}
		stream <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
		stream <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
		if m.waitForTool != nil {
			go func() {
				defer close(stream)
				select {
				case <-m.waitForTool:
					stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
				case <-ctx.Done():
					stream <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
				}
			}()
			return stream, nil
		}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	} else {
		if len(req.Tools) != 0 {
			return nil, errors.New("synthesis still has tools; driver did not own control flow")
		}
		if m.cancelSynthesis != nil {
			m.cancelSynthesis()
			return nil, context.Canceled
		}
		value := ""
		for _, message := range req.Messages {
			if message.Role == "tool" {
				value = strings.TrimSpace(message.Content)
			}
		}
		if value == "" {
			return nil, errors.New("synthesis lost real evidence")
		}
		stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "The measured value is " + value + "."}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(stream)
	return stream, nil
}
