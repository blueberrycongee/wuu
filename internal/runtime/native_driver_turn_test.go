package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// The bundled algorithm has no dependency on the legacy whole-loop service.
// Its tool effects, input receipts and resumable checkpoint must use the same
// resource services as an independently installed driver.
func TestDefaultDriverUsesPublicTurnResources(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("overflow-%t/streaming-%t", overflow, streaming), func(t *testing.T) {
				testDefaultDriverUsesPublicTurnResources(t, overflow, streaming)
			})
		}
	}
}

func testDefaultDriverUsesPublicTurnResources(t *testing.T, overflow, streaming bool) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(dir, "default-session"); err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(dir, "default-session")
	if err != nil {
		t.Fatal(err)
	}
	registry := pluginhost.NewServiceRegistry()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindRegistry(registry)
	provider := loopdriver.NewService("default-evidence", "driver.evidence", agent.DefaultDriver{}, registry, []pluginhost.ServiceRequirement{
		{Name: pluginprotocol.DriverTurnService, MajorVersion: pluginprotocol.DriverTurnMajor, Required: true},
		{Name: loopdriver.DriverCheckpointService, MajorVersion: 1, Required: true},
	})
	clients := append([]pluginhost.Client{kernel, provider}, runtimeServiceConsumers("evidence")...)
	host := pluginhost.New(clients...)
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	t.Cleanup(func() { _ = host.Close(ctx) })
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	model := &workflowEvidenceModel{continueWithTools: true, overflowFirst: overflow}
	tools := &workflowEvidenceTools{path: path}
	runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, ToolLedger: ledger,
		LoopDriver:            resolveLoopDriver("evidence", host, kernel.driverGateways),
		DriverCheckpointStore: sessionDriverCheckpointStore{sessDir: dir, sessionID: "default-session"},
	}
	runner.StreamingToolExecution = streaming
	runner.CompactionRegistry = agent.NewCompactionRegistry()
	runner.CompactionRegistry.Register(&generationCompactionProvider{key: "unchanged"})
	history := []providers.ChatMessage{{Role: "user", Content: "Earlier task"}, {Role: "assistant", Content: "Earlier result"}, {Role: "user", Content: "Measure evidence.txt", ClientID: "default-input"}}
	attachWorkflowHistory(t, runner, dir, "default-session", history)
	result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "default-session", ExecutionID: "default-execution"}), history, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedRequests := 2
	if overflow {
		expectedRequests++
		if !result.HistoryRewritten {
			t.Fatal("overflow recovery did not commit its replacement context")
		}
	}
	if result.Content != "The measured value is 42." || tools.calls != 1 || len(model.requests) != expectedRequests {
		t.Fatalf("default loop lost its tool workflow: result=%+v effects=%d requests=%d", result, tools.calls, len(model.requests))
	}
	checkpoint, found, err := runner.DriverCheckpointStore.Load(ctx)
	if err != nil || !found || len(checkpoint.State) == 0 {
		t.Fatalf("default loop did not commit a checkpoint: found=%t state=%s err=%v", found, checkpoint.State, err)
	}
	assertDriverModelReceipts(t, dir, "default-session", loopdriver.DefaultDriverID, model.requests)
}

// Native drivers must use the same declared callbacks and execution ownership
// as process drivers. Exercise metadata preservation, canceled requests, context
// commits, explicit tool effects, duplicate rejection, and callback cleanup.
func TestNativeDriverIndependentTurn(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming-%t", streaming), func(t *testing.T) { testNativeDriverIndependentTurn(t, streaming) })
	}
}

func testNativeDriverIndependentTurn(t *testing.T, streaming bool) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(dir, "native-session"); err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(dir, "native-session")
	if err != nil {
		t.Fatal(err)
	}
	model := &workflowEvidenceModel{}
	tools := &workflowEvidenceTools{path: path}
	implementation := &nativeTurnFixture{}
	registry := pluginhost.NewServiceRegistry()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindRegistry(registry)
	provider := loopdriver.NewService("native-evidence", "driver.evidence", implementation, registry, []pluginhost.ServiceRequirement{
		{Name: pluginprotocol.DriverTurnService, MajorVersion: pluginprotocol.DriverTurnMajor, Required: true},
		{Name: loopdriver.DriverCheckpointService, MajorVersion: 1, Required: true},
	})
	clients := append([]pluginhost.Client{kernel, provider}, runtimeServiceConsumers("evidence")...)
	host := pluginhost.New(clients...)
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	t.Cleanup(func() { _ = host.Close(ctx) })
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	runner := &agent.StreamRunner{Client: model, Model: "controlled-evidence", Tools: tools, ToolLedger: ledger,
		LoopDriver:            resolveLoopDriver("evidence", host, kernel.driverGateways),
		DriverCheckpointStore: sessionDriverCheckpointStore{sessDir: dir, sessionID: "native-session"},
	}
	history := []providers.ChatMessage{{Role: "user", Content: "Measure evidence.txt", ClientID: "native-input"}}
	attachWorkflowHistory(t, runner, dir, "native-session", history)
	inputCalls, contextCalls, transformCalls := 0, 0, 0
	runner.BeforeStep = func() []providers.ChatMessage {
		inputCalls++
		if inputCalls > 1 {
			return nil
		}
		return []providers.ChatMessage{{Role: "user", Content: "Use the measured value", ClientID: "native-followup", Steered: true}}
	}
	runner.BeforeRequestContext = func() []agent.ContextSegment {
		contextCalls++
		return agent.RequestOnlyContextBlocks([]wuucontext.Block{{Kind: wuucontext.BlockTaskState, Source: "fixture", Content: "Use evidence", TokenBudget: 12}})
	}
	runner.BeforeRequest = func(_ context.Context, request *providers.ChatRequest) error {
		transformCalls++
		request.Temperature = 0.25
		return nil
	}
	implementation.run = func(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.TurnReceipt, error) {
		turn, ok := gateway.(loopdriver.TurnGateway)
		if !ok {
			return loopdriver.TurnReceipt{}, errors.New("native driver lacks public turn operations")
		}
		request, err := turn.ReadTurn(ctx)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if len(request.Messages) != 1 || request.Messages[0].ClientID != "native-input" {
			return loopdriver.TurnReceipt{}, fmt.Errorf("read lost input address: %+v", request.Messages)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := turn.CallModel(canceled, request); err == nil || len(model.requests) != 0 {
			return loopdriver.TurnReceipt{}, errors.New("canceled callback reached model")
		}
		admitted, err := turn.AdmitInput(ctx)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if len(admitted.Messages) != 1 || admitted.Messages[0].ClientID != "native-followup" || admitted.Messages[0].Seq == 0 || !admitted.Messages[0].Steered || admitted.Context.ArchiveHead < 2 {
			return loopdriver.TurnReceipt{}, fmt.Errorf("admission lost durable input addresses: %+v", admitted)
		}
		segments, err := turn.ReadRequestContext(ctx)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if len(segments) != 1 || len(segments[0].Blocks) != 1 || segments[0].Blocks[0].Kind != wuucontext.BlockTaskState || segments[0].Blocks[0].TokenBudget != 12 || segments[0].Durable || segments[0].Lifecycle != loopdriver.ContextSegmentRequestOnly {
			return loopdriver.TurnReceipt{}, fmt.Errorf("request context lost typed projection: %+v", segments)
		}
		request.Messages = append(admitted.Context.Messages, segments[0].Messages...)
		request, err = turn.TransformRequest(ctx, request)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		var planned loopdriver.ModelRound
		if streaming {
			planned, err = turn.CallModelWithTools(ctx, request, "measure")
			if err == nil && planned.ToolBatchID != "measure" {
				return loopdriver.TurnReceipt{}, fmt.Errorf("streaming receipt lost its batch: %+v", planned)
			}
		} else {
			planned, err = turn.CallModel(ctx, request)
		}
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if (!streaming && tools.calls != 0) || len(planned.Messages) != 1 || len(planned.Messages[0].ToolCalls) != 1 {
			return loopdriver.TurnReceipt{}, errors.New("model callback executed tools or lost its declaration")
		}
		if model.requests[0].Messages[0].ClientID != "native-input" {
			return loopdriver.TurnReceipt{}, errors.New("model callback lost input address")
		}
		if err := gateway.WriteCheckpoint(ctx, implementation.Checkpoint()); err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		batch := loopdriver.ToolBatch{ID: "measure", Calls: planned.Messages[0].ToolCalls}
		results, err := turn.CallTools(ctx, batch)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if len(results) != 1 || results[0].Content != "42" || results[0].ToolInvocationID == "" {
			return loopdriver.TurnReceipt{}, fmt.Errorf("tool result lost evidence or address: %+v", results)
		}
		if _, err := turn.CallTools(ctx, batch); err == nil || tools.calls != 1 {
			return loopdriver.TurnReceipt{}, errors.New("duplicate effect was accepted")
		}
		current, err := turn.ReadContext(ctx)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if !current.CanReplace || len(current.Messages) != 4 || current.Messages[3].ToolInvocationID != results[0].ToolInvocationID {
			return loopdriver.TurnReceipt{}, fmt.Errorf("context lost tool receipt: %+v", current)
		}
		committed, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: current.Messages})
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		if committed.Revision <= current.Revision || committed.ArchiveHead < 4 || committed.Messages[3].Seq == 0 {
			return loopdriver.TurnReceipt{}, fmt.Errorf("context commit lost durable addresses: %+v", committed)
		}
		if _, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: current.Messages}); err == nil {
			return loopdriver.TurnReceipt{}, errors.New("stale context replacement was accepted")
		}
		request.Messages, request.Tools = committed.Messages, nil
		answer, err := turn.CallModel(ctx, request)
		if err != nil {
			return loopdriver.TurnReceipt{}, err
		}
		return turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: answer.Messages[len(answer.Messages)-1].Content})
	}
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "native-session", ExecutionID: "native-execution"})
	result, err := runner.RunWithCallback(ctx, history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "The measured value is 42." || tools.calls != 1 || len(model.requests) != 2 {
		t.Fatalf("native control flow failed: result=%+v model=%d tools=%d", result, len(model.requests), tools.calls)
	}
	if inputCalls != 1 || contextCalls != 1 || transformCalls != 1 || model.requests[0].Temperature != 0.25 || len(model.requests[0].Messages) != 3 || model.requests[0].Messages[1].ClientID != "native-followup" {
		t.Fatalf("native model repeated request effects or lost transformed input: input=%d context=%d transform=%d requests=%+v", inputCalls, contextCalls, transformCalls, model.requests)
	}
	assertDriverModelReceipts(t, dir, "native-session", result.DriverID, model.requests)
	runner.BeforeStep, runner.BeforeRequestContext, runner.BeforeRequest = nil, nil, nil
	// A provider-owned loop must receive classified failure evidence and the
	// accepted partial response, then decide whether another round is allowed.
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("model-failure-partial-%t", partial), func(t *testing.T) {
			failureModel := &driverFailureModel{partial: partial}
			runner.Client = failureModel
			implementation.run = func(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.TurnReceipt, error) {
				turn := gateway.(loopdriver.TurnGateway)
				request, err := turn.ReadTurn(ctx)
				if err != nil {
					return loopdriver.TurnReceipt{}, err
				}
				round, err := turn.CallModel(ctx, request)
				var failure *loopdriver.FailureError
				if !errors.As(err, &failure) || failure.Failure.Category != "context_overflow" || !failure.Failure.ContextOverflow {
					return loopdriver.TurnReceipt{}, fmt.Errorf("lost classified model failure: %v", err)
				}
				if partial && (len(round.Messages) != 1 || round.Messages[0].Content != "partial evidence" || round.Usage == nil || round.Usage.OutputTokens != 3) {
					return loopdriver.TurnReceipt{}, fmt.Errorf("lost partial model result: %+v", round)
				}
				if !partial && len(round.Messages) != 0 {
					return loopdriver.TurnReceipt{}, fmt.Errorf("invented partial result: %+v", round)
				}
				return turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: "driver stopped after classified failure"})
			}
			failedCtx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "native-session", ExecutionID: fmt.Sprintf("native-failure-%t", partial)})
			result, err := runner.RunWithCallback(failedCtx, history, nil)
			if err != nil || result.Content != "driver stopped after classified failure" || failureModel.calls != 1 || tools.calls != 1 {
				t.Fatalf("failure changed driver control flow: result=%+v calls=%d tools=%d err=%v", result, failureModel.calls, tools.calls, err)
			}
		})
	}
	if _, ok := kernel.driverGateways.lookup("native-execution"); ok {
		t.Fatal("completed native driver retained its callbacks")
	}
	if _, err := (&driverTurnInvoker{parent: kernel}).InvokeService(context.Background(), pluginhost.ServiceInvokeParams{
		Caller: provider.ID(), Method: "context", Params: []byte(`{"execution_id":"native-execution"}`),
	}); err == nil {
		t.Fatal("late callback reached a completed execution")
	}
}

type driverFailureModel struct {
	partial bool
	calls   int
}

func (m *driverFailureModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}

func (m *driverFailureModel) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls++
	stream := make(chan providers.StreamEvent, 3)
	if m.partial {
		stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "partial evidence"}
		stream <- providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{InputTokens: 17, OutputTokens: 3}}
	}
	stream <- providers.StreamEvent{Type: providers.EventError, Error: providers.NewProviderStreamError("context_length_exceeded", "controlled context overflow")}
	close(stream)
	return stream, nil
}

type nativeTurnFixture struct {
	run func(context.Context, loopdriver.KernelGateway) (loopdriver.TurnReceipt, error)
}

func (d *nativeTurnFixture) Descriptor(context.Context) (loopdriver.Descriptor, error) {
	return loopdriver.Descriptor{ID: "native-evidence", Version: "1.0.0"}, nil
}
func (d *nativeTurnFixture) Create(context.Context, loopdriver.ExecutionContext, loopdriver.PersistedInput) (loopdriver.Instance, error) {
	return d, nil
}
func (d *nativeTurnFixture) Resume(context.Context, loopdriver.ExecutionContext, loopdriver.PersistedInput, loopdriver.Checkpoint) (loopdriver.Instance, error) {
	return d, nil
}
func (d *nativeTurnFixture) Run(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.TerminalOutcome, error) {
	receipt, err := d.run(ctx, gateway)
	status := loopdriver.TerminalSucceeded
	if err != nil {
		status = loopdriver.TerminalFailed
	}
	return loopdriver.TerminalOutcome{Status: status, ReceiptID: receipt.ID, Checkpoint: d.Checkpoint()}, err
}
func (d *nativeTurnFixture) Checkpoint() loopdriver.Checkpoint {
	return loopdriver.Checkpoint{ContractVersion: loopdriver.ContractVersion, DriverID: "native-evidence", DriverVersion: "1.0.0", State: []byte(`{}`)}
}
func (*nativeTurnFixture) Cancel(string) {}
func (*nativeTurnFixture) Shutdown()     {}
