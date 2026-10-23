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
	host.RetirePlugin(context.Background(), provider.ID, errors.New("workflow provider disabled"))
	if _, err := runner.RunWithCallback(resumeCtx, history, nil); err == nil || tools.calls != 1 || len(resumedModel.requests) != 1 {
		t.Fatalf("disabled workflow fell back to another loop: effects=%d requests=%d err=%v", tools.calls, len(resumedModel.requests), err)
	}
	t.Logf("external driver=%s; file evidence=42; tool effects=%d; first requests=%d; resume requests=%d; output=%q", result.DriverID, tools.calls, len(model.requests), len(resumedModel.requests), result.Content)
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
