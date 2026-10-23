package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// Native drivers must use the same declared callbacks and execution ownership
// as process drivers. Exercise metadata preservation, canceled requests, context
// commits, explicit tool effects, duplicate rejection, and callback cleanup.
func TestNativeDriverIndependentTurn(t *testing.T) {
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
		{Name: pluginprotocol.DriverTurnService, MajorVersion: 1, Required: true},
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
	implementation.run = func(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.ModelLoopReceipt, error) {
		turn, ok := gateway.(loopdriver.TurnGateway)
		if !ok {
			return loopdriver.ModelLoopReceipt{}, errors.New("native driver lacks public turn operations")
		}
		if _, err := gateway.ExecuteModelLoop(ctx, loopdriver.PersistedInput{}, loopdriver.LoopPolicy{}); err == nil {
			return loopdriver.ModelLoopReceipt{}, errors.New("undeclared legacy loop dependency was callable")
		}
		request, err := turn.ReadTurn(ctx)
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		if len(request.Messages) != 1 || request.Messages[0].ClientID != "native-input" {
			return loopdriver.ModelLoopReceipt{}, fmt.Errorf("read lost input address: %+v", request.Messages)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := turn.CallModel(canceled, request); err == nil || len(model.requests) != 0 {
			return loopdriver.ModelLoopReceipt{}, errors.New("canceled callback reached model")
		}
		planned, err := turn.CallModel(ctx, request)
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		if tools.calls != 0 || len(planned.Messages) != 1 || len(planned.Messages[0].ToolCalls) != 1 {
			return loopdriver.ModelLoopReceipt{}, errors.New("model callback executed tools or lost its declaration")
		}
		if model.requests[0].Messages[0].ClientID != "native-input" {
			return loopdriver.ModelLoopReceipt{}, errors.New("model callback lost input address")
		}
		if err := gateway.WriteCheckpoint(ctx, implementation.Checkpoint()); err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		batch := loopdriver.ToolBatch{ID: "measure", Calls: planned.Messages[0].ToolCalls}
		results, err := turn.CallTools(ctx, batch)
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		if len(results) != 1 || results[0].Content != "42" || results[0].ToolInvocationID == "" {
			return loopdriver.ModelLoopReceipt{}, fmt.Errorf("tool result lost evidence or address: %+v", results)
		}
		if _, err := turn.CallTools(ctx, batch); err == nil || tools.calls != 1 {
			return loopdriver.ModelLoopReceipt{}, errors.New("duplicate effect was accepted")
		}
		current, err := turn.ReadContext(ctx)
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		if !current.CanReplace || len(current.Messages) != 3 || current.Messages[2].ToolInvocationID != results[0].ToolInvocationID {
			return loopdriver.ModelLoopReceipt{}, fmt.Errorf("context lost tool receipt: %+v", current)
		}
		committed, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: current.Messages})
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
		}
		if committed.Revision <= current.Revision || committed.ArchiveHead < 3 || committed.Messages[2].Seq == 0 {
			return loopdriver.ModelLoopReceipt{}, fmt.Errorf("context commit lost durable addresses: %+v", committed)
		}
		if _, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: current.Messages}); err == nil {
			return loopdriver.ModelLoopReceipt{}, errors.New("stale context replacement was accepted")
		}
		request.Messages, request.Tools = committed.Messages, nil
		answer, err := turn.CallModel(ctx, request)
		if err != nil {
			return loopdriver.ModelLoopReceipt{}, err
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
	if _, ok := kernel.driverGateways.lookup("native-execution"); ok {
		t.Fatal("completed native driver retained its callbacks")
	}
	if _, err := (&driverTurnInvoker{parent: kernel}).InvokeService(context.Background(), pluginhost.ServiceInvokeParams{
		Caller: provider.ID(), Method: "context", Params: []byte(`{"execution_id":"native-execution"}`),
	}); err == nil {
		t.Fatal("late callback reached a completed execution")
	}
}

type nativeTurnFixture struct {
	run func(context.Context, loopdriver.KernelGateway) (loopdriver.ModelLoopReceipt, error)
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
