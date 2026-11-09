package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

func batchProcessHost(t *testing.T, mode string) (*pluginhost.Host, *kernelHostServices) {
	t.Helper()
	fixture := os.Getenv("WUU_BATCH_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/tool-batch-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	plugin := testRuntimePlugin("independent-batch")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool-batch.ts", mode}
	selection := &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true, toolbatch.AdapterModule: false, toolservice.DefaultModule: false}}}, Services: []composition.Binding{{Service: toolbatch.ServiceName, Major: 1, Provider: plugin.ID}, {Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}}}
	host, kernel, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, mode, home, state, nil, startPluginClient, nil, nil, selection, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return host, kernel
}
func TestSelectedToolBatchProcess(t *testing.T) {
	for _, mode := range []string{"normal", "partial", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			host, kernel := batchProcessHost(t, mode)
			var initialized atomic.Int32
			executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				initialized.Add(1)
				return toolservice.Backend{}, errors.New("default toolkit must remain dormant")
			}), selectedToolIdentity{threadID: "batch-thread", cwd: t.TempDir()})
			calls := []providers.ToolCall{{ID: "first", Name: "echo", Arguments: "{}"}, {ID: "malformed", Name: "echo", Arguments: "{"}, {ID: "failed", Name: "fail", Arguments: "{}"}, {ID: "last", Name: "echo", Arguments: "{}"}}
			if mode == "cancel" {
				calls = calls[:2]
				calls[1].Arguments = "{}"
			}
			model := &batchProcessModel{calls: calls}
			ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "batch-thread")
			if err != nil {
				t.Fatal(err)
			}
			runner := &agent.StreamRunner{Client: model, Model: "controlled", Tools: executor, ToolLedger: ledger, LoopDriver: resolveLoopDriver("", host, kernel.driverGateways), SelectedToolBatch: newSelectedToolBatch(host), StreamingToolExecution: true}
			result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "batch-thread", ExecutionID: "batch-" + mode}), []providers.ChatMessage{{Role: "user", Content: "exercise batch"}}, nil)
			if (err != nil) != (mode == "partial") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var messages []providers.ChatMessage
			for _, message := range result.NewMessages {
				if message.Role == "tool" {
					messages = append(messages, message)
				}
			}
			if len(messages) != len(calls) {
				t.Fatalf("lost partial outcomes: %+v err=%v", result, err)
			}
			for i, message := range messages {
				if message.ToolCallID != calls[i].ID || message.ToolInvocationID == "" {
					t.Fatalf("unsettled/reordered result %+v", message)
				}
			}
			if mode == "normal" {
				if !strings.Contains(messages[0].Content, "last,failed,first") || !strings.Contains(messages[3].Content, "external-batch:normal:last") || !messages[1].ToolResult.IsError || !messages[2].ToolResult.IsError {
					t.Fatalf("external reverse backend not used: %+v", messages)
				}
			}
			if mode == "partial" && (!messages[0].ToolResult.IsError || messages[3].ToolResult.IsError) {
				t.Fatalf("lost retained success or failed pending calls: %+v", messages)
			}
			if mode == "cancel" {
				for _, message := range messages {
					if !message.ToolResult.IsError {
						t.Fatalf("canceled call executed: %+v", message)
					}
				}
			}
			pending, loadErr := ledger.PendingProjection(context.Background())
			if loadErr != nil || len(pending) != len(calls) {
				t.Fatalf("durable outcomes=%+v err=%v", pending, loadErr)
			}
			if initialized.Load() != 0 {
				t.Fatal("external batch initialized native toolkit")
			}
			t.Logf("external packed SDK batch %s: %d ordered durable results, no default initialization, forged scope denied", mode, len(messages))
		})
	}
}

type batchProcessModel struct {
	calls    []providers.ToolCall
	requests int
}

func (*batchProcessModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected nonstreaming model")
}
func (m *batchProcessModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.requests++
	request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "fixture", Protocol: "controlled", Transport: "memory", Mode: "stream"})
	events := make(chan providers.StreamEvent, 2*len(m.calls)+2)
	if m.requests == 1 {
		for _, call := range m.calls {
			call := call
			events <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
			events <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
		}
		events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	} else {
		events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "completed"}
		events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(events)
	return events, nil
}

// Replacing the selected implementation must not reroute an already admitted
// batch. The old and new external processes intentionally share a plugin ID.
func TestSelectedToolBatchProcessReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	oldHost, oldKernel := batchProcessHost(t, "normal")
	oldTools := &replacementBatchTools{entered: make(chan struct{}), release: make(chan struct{})}
	calls := []providers.ToolCall{{ID: "first", Name: "echo", Arguments: "{}"}, {ID: "last", Name: "echo", Arguments: "{}"}}
	oldLedger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "old")
	if err != nil {
		t.Fatal(err)
	}
	oldRunner := &agent.StreamRunner{Client: &batchProcessModel{calls: calls}, Model: "controlled", Tools: oldTools, ToolLedger: oldLedger, LoopDriver: resolveLoopDriver("", oldHost, oldKernel.driverGateways), SelectedToolBatch: newSelectedToolBatch(oldHost)}
	done := make(chan error, 1)
	go func() {
		_, err := oldRunner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "old", ExecutionID: "old-turn"}), []providers.ChatMessage{{Role: "user", Content: "old batch"}}, nil)
		done <- err
	}()
	select {
	case <-oldTools.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	newHost, newKernel := batchProcessHost(t, "cancel")
	newTools := &replacementBatchTools{}
	newRunner := &agent.StreamRunner{Client: &batchProcessModel{calls: calls}, Model: "controlled", Tools: newTools, LoopDriver: resolveLoopDriver("", newHost, newKernel.driverGateways), SelectedToolBatch: newSelectedToolBatch(newHost)}
	if _, err := newRunner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "new", ExecutionID: "new-turn"}), []providers.ChatMessage{{Role: "user", Content: "new batch"}}, nil); err != nil {
		t.Fatal(err)
	}
	close(oldTools.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Join(oldTools.calls, ",") != "last,first" || len(newTools.calls) != 0 {
		t.Fatalf("old=%v new=%v", oldTools.calls, newTools.calls)
	}
	for _, tools := range []*replacementBatchTools{oldTools, newTools} {
		if tools.settled["first"] != 1 || tools.settled["last"] != 1 {
			t.Fatalf("settlements=%v", tools.settled)
		}
	}
	pending, err := oldLedger.PendingProjection(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatalf("old durable outcomes=%+v err=%v", pending, err)
	}
	t.Log("same-ID replacement: old external backend drained in reverse order exactly once; new backend canceled independently")
}

type replacementBatchTools struct {
	mu               sync.Mutex
	calls            []string
	settled          map[string]int
	entered, release chan struct{}
}

func (*replacementBatchTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}
}
func (e *replacementBatchTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	e.mu.Lock()
	e.calls = append(e.calls, call.ID)
	first := len(e.calls) == 1
	e.mu.Unlock()
	if first && e.entered != nil {
		close(e.entered)
		select {
		case <-e.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return call.ID, nil
}
func (e *replacementBatchTools) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.settled == nil {
		e.settled = map[string]int{}
	}
	e.settled[call.ID]++
	return result
}

func TestSelectedToolBatchProcessRemovalCancelsInFlight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, kernel := batchProcessHost(t, "normal")
	tools := &replacementBatchTools{entered: make(chan struct{}), release: make(chan struct{})}
	calls := []providers.ToolCall{{ID: "pending", Name: "echo", Arguments: "{}"}, {ID: "running", Name: "echo", Arguments: "{}"}}
	ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "remove")
	if err != nil {
		t.Fatal(err)
	}
	runner := &agent.StreamRunner{Client: &batchProcessModel{calls: calls}, Model: "controlled", Tools: tools, ToolLedger: ledger, LoopDriver: resolveLoopDriver("", host, kernel.driverGateways), SelectedToolBatch: newSelectedToolBatch(host)}
	done := make(chan error, 1)
	go func() {
		_, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remove", ExecutionID: "remove-turn"}), []providers.ChatMessage{{Role: "user", Content: "cancel in-flight backend"}}, nil)
		done <- err
	}()
	select {
	case <-tools.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	host.RetirePlugin(ctx, "independent-batch", errors.New("removed while executing"))
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("removal hid cancellation")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if strings.Join(tools.calls, ",") != "running" || tools.settled["pending"] != 1 || tools.settled["running"] != 1 {
		t.Fatalf("effects=%v settlement=%v", tools.calls, tools.settled)
	}
	pending, err := ledger.PendingProjection(context.Background())
	if err != nil || len(pending) != 2 {
		t.Fatalf("durable cancellation=%+v err=%v", pending, err)
	}
	for _, outcome := range pending {
		if !outcome.Result.IsError {
			t.Fatalf("lost cancellation: %+v", outcome)
		}
	}
	t.Log("provider removal canceled and drained one accepted effect; both calls durably settled once without fallback")
}

// An external backend can opt into the same early-admission contract as the
// native adapter. The model cannot finish until the admitted effect starts.
func TestSelectedToolBatchProcessStreaming(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host, kernel := batchProcessHost(t, "stream")
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := &workflowEvidenceTools{path: path, started: make(chan struct{})}
	model := &workflowEvidenceModel{continueWithTools: true, waitForTool: tools.started}
	ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "external-stream")
	if err != nil {
		t.Fatal(err)
	}
	runner := &agent.StreamRunner{Client: model, Model: "controlled", Tools: tools, ToolLedger: ledger, LoopDriver: resolveLoopDriver("", host, kernel.driverGateways), SelectedToolBatch: newSelectedToolBatch(host), StreamingToolExecution: true}
	result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "external-stream", ExecutionID: "external-stream-turn"}), []providers.ChatMessage{{Role: "user", Content: "stream external"}}, nil)
	if err != nil || tools.calls != 1 {
		t.Fatalf("external admission did not precede model completion or repeated effect: calls=%d result=%+v err=%v", tools.calls, result, err)
	}
	pending, err := ledger.PendingProjection(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("streamed receipt=%+v err=%v", pending, err)
	}
	t.Log("external SDK stream_admit started effect before model completion; final execute and duplicate callbacks joined one durable invocation")
}
