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

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// Run through scripts/workflow-driver-e2e.mjs, which installs a packed public
// SDK and the example in an independent directory before invoking this check.
func TestWorkflowDriverProcess(t *testing.T) {
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
	host, kernel, err := buildPluginHost([]pluginpkg.Plugin{provider}, root, "workflow", t.TempDir(), t.TempDir(), nil, startPluginClient, nil, nil, nil, "workflow")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := activatePluginHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	driver := resolveLoopDriver("workflow", host, kernel.driverGateways)
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
	runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, ToolLedger: ledger, LoopDriver: driver, DriverCheckpointStore: store}
	history := []providers.ChatMessage{{Role: "user", Content: "Read evidence.txt and report the measured value."}}
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
	if len(partial.NewMessages) != 2 || partial.NewMessages[1].ToolInvocationID == "" {
		t.Fatalf("interruption lost the durable tool receipt: %+v", partial.NewMessages)
	}
	// Match the application finalizer: saving the reported checkpoint must not
	// overwrite the newer state already saved by a callback before interruption.
	if err := store.Save(context.Background(), loopdriver.Checkpoint{ContractVersion: partial.DriverContractVersion, DriverID: partial.DriverID, DriverVersion: partial.DriverVersion, State: partial.DriverCheckpoint}); err != nil {
		t.Fatal(err)
	}
	history = append(history, partial.NewMessages...)
	resumedModel := &workflowEvidenceModel{synthesisOnly: true}
	runner.Client = resumedModel
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
		plannedRunner := &agent.StreamRunner{Client: &workflowEvidenceModel{}, Model: "controlled-evidence", Tools: plannedTools, ToolLedger: plannedLedger, LoopDriver: driver,
			DriverCheckpointStore: workflowCheckpointBarrier{CheckpointStore: plannedStore, phase: "planned", cancel: stopPlanning}}
		originalHistory := []providers.ChatMessage{{Role: "user", Content: "Read evidence.txt and report the measured value."}}
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
	path  string
	calls int
}

func (t *workflowEvidenceTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_evidence", InputSchema: map[string]any{"type": "object"}}}
}
func (t *workflowEvidenceTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if call.Name != "read_evidence" {
		return "", fmt.Errorf("unexpected tool %s", call.Name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t.calls++
	data, err := os.ReadFile(t.path)
	return string(data), err
}

type workflowEvidenceModel struct {
	mu              sync.Mutex
	requests        []providers.ChatRequest
	cancelSynthesis context.CancelFunc
	synthesisOnly   bool
}

func (m *workflowEvidenceModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *workflowEvidenceModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	stream := make(chan providers.StreamEvent, 3)
	if !m.synthesisOnly && len(m.requests) == 1 {
		if len(req.Tools) != 1 {
			return nil, errors.New("planning did not receive the selected tools")
		}
		call := providers.ToolCall{ID: "evidence-call", Name: "read_evidence", Arguments: "{}"}
		stream <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
		stream <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
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
