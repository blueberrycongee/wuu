package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// Model-only rebinding must preserve the selected external batch's capabilities
// and effect authority. Cover a pending selection during a failed old-model call,
// its frozen retry, two tool-bearing steps, early admission, duplicate callbacks,
// stale requests, and durable exactly-once results without a native backend.
func TestSelectedToolBatchProcessModelStepSelection(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming-%t", streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			mode := "normal"
			if streaming {
				mode = "stream"
			}
			host, _ := batchProcessHost(t, mode)
			var engine selectedEngineDescriptor
			if err := callSelectedEngine(ctx, host, "wuu", "descriptor", struct{}{}, &engine); err != nil || !engine.Enabled || !slices.Contains(engine.Capabilities, "step-model-selection") {
				t.Fatalf("fixture must select a capable engine: %+v %v", engine, err)
			}
			if owner, ok := host.ServiceRegistry().ProviderPluginID(toolbatch.ServiceName, 1); !ok || owner != "independent-batch" {
				t.Fatalf("external batch not selected: %q %t", owner, ok)
			}
			var initialized atomic.Int32
			tools := &modelStepBatchTools{selectedToolExecutor: newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				initialized.Add(1)
				return toolservice.Backend{}, errors.New("default backend must remain dormant")
			}), selectedToolIdentity{threadID: "model-batch", cwd: t.TempDir()}), finished: map[string]chan struct{}{"old-tool": make(chan struct{}), "next-tool": make(chan struct{})}, effects: map[string]int{}}
			batch := &modelStepBatchObserver{selectedToolBatch: &selectedToolBatch{host: host}}
			dir := t.TempDir()
			store := testSessionStore(t, dir)
			if _, err := store.Create("model-batch"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetRuntimeSelection("model-batch", session.RuntimeSelection{Provider: "old", Model: "one"}); err != nil {
				t.Fatal(err)
			}
			ledger, err := toolledger.New(testSessionDocuments(t, dir), "model-batch")
			if err != nil {
				t.Fatal(err)
			}
			old := &modelStepBatchModel{id: "old-tool", wait: tools.finished["old-tool"], streaming: streaming, failFirst: true}
			next := &modelStepBatchModel{id: "next-tool", wait: tools.finished["next-tool"], streaming: streaming}
			old.selectNext = func() error {
				metadata, found, err := store.Find("model-batch")
				if err != nil || !found {
					return fmt.Errorf("load model intent: found=%t err=%v", found, err)
				}
				_, err = store.SetNextStepModelSelection("model-batch", session.NextStepModelSelection{
					ExpectedRevision: metadata.ModelSelectionRevision, ExpectedEngineID: metadata.EngineID,
					Selection: session.RuntimeSelection{Provider: "next", Model: "two"},
				})
				return err
			}
			driver := &modelStepBatchDriver{}
			runner := &agent.StreamRunner{Client: old, ProviderName: "old", Model: "one", Tools: tools, ToolLedger: ledger, SelectedToolBatch: batch, StreamingToolExecution: true, LoopDriver: driver,
				ModelInputReceiptStore: sessionModelInputReceiptStore{sessions: store, sessionID: "model-batch"}}
			runner.ResolveModelStep = func(context.Context, uint64) (*agent.ModelStepBinding, error) {
				metadata, found, err := store.Find("model-batch")
				if err != nil || !found {
					return nil, fmt.Errorf("resolve durable model intent: found=%t err=%v", found, err)
				}
				client := providers.StreamClient(old)
				if metadata.Provider == "next" {
					client = next
				}
				return &agent.ModelStepBinding{Revision: metadata.ModelSelectionRevision, Client: client, Provider: metadata.Provider, Model: metadata.Model}, nil
			}
			driver.run = func(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.TurnReceipt, error) {
				turn := gateway.(loopdriver.TurnGateway)
				first, err := turn.ReadTurn(ctx)
				if err != nil {
					return loopdriver.TurnReceipt{}, err
				}
				if _, err := turn.CallModel(ctx, first); err == nil {
					return loopdriver.TurnReceipt{}, errors.New("synthetic old-model failure was hidden")
				}
				for step := 1; step <= 2; step++ {
					if _, err := turn.AdmitInput(ctx); err != nil {
						return loopdriver.TurnReceipt{}, err
					}
					request, err := turn.ReadTurn(ctx)
					if err != nil {
						return loopdriver.TurnReceipt{}, err
					}
					wantProvider, wantModel := "old", "one"
					if step == 2 {
						wantProvider, wantModel = "next", "two"
						if _, err := turn.CallModel(ctx, first); err == nil {
							return loopdriver.TurnReceipt{}, errors.New("stale old-model request accepted")
						}
					}
					if request.Provider != wantProvider || request.Model != wantModel || request.ModelSelectionRevision != uint64(step) {
						return loopdriver.TurnReceipt{}, fmt.Errorf("step %d lost frozen model binding: %+v", step, request)
					}
					settings, err := turn.ReadSettings(ctx)
					if err != nil || settings.StreamingTools != streaming {
						return loopdriver.TurnReceipt{}, fmt.Errorf("model snapshot replaced selected batch capabilities: streaming=%t want=%t err=%v", settings.StreamingTools, streaming, err)
					}
					id := fmt.Sprintf("selected-step-%d", step)
					var round loopdriver.ModelRound
					if streaming {
						round, err = turn.CallModelWithTools(ctx, request, id)
					} else {
						round, err = turn.CallModel(ctx, request)
					}
					if err != nil || round.ToolWorkStarted != streaming || len(round.Messages) != 1 || len(round.Messages[0].ToolCalls) != 1 {
						return loopdriver.TurnReceipt{}, fmt.Errorf("step %d lost admitted tool: %+v %v", step, round, err)
					}
					requestBatch := loopdriver.ToolBatch{ID: id, Calls: round.Messages[0].ToolCalls}
					messages, err := turn.CallTools(ctx, requestBatch)
					if err != nil || len(messages) != 1 || messages[0].ToolResult.IsError || !strings.Contains(messages[0].Content, "external-batch:"+mode+":") {
						return loopdriver.TurnReceipt{}, fmt.Errorf("step %d bypassed external tool provider: %+v %v", step, messages, err)
					}
					if _, err := turn.CallTools(ctx, requestBatch); err == nil {
						return loopdriver.TurnReceipt{}, errors.New("settled batch was replayed")
					}
				}
				return turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: "two selected model steps"})
			}
			result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "model-batch", ExecutionID: "model-batch-turn"}), []providers.ChatMessage{{Role: "user", Content: "switch model, retain tools"}}, nil)
			if err != nil || result.Content != "two selected model steps" {
				t.Fatalf("combined model/batch run: %+v %v", result, err)
			}
			if len(old.requests) != 2 || len(next.requests) != 1 || old.requests[0].ModelSelectionRevision != 1 || old.requests[1].ModelSelectionRevision != 1 || next.requests[0].ModelSelectionRevision != 2 {
				t.Fatalf("model retry or boundary drift: old=%+v next=%+v", old.requests, next.requests)
			}
			wantAdmissions := 0
			if streaming {
				wantAdmissions = 2
			}
			if batch.executions.Load() != 2 || batch.admissions.Load() != int32(wantAdmissions) || initialized.Load() != 0 || tools.effects["old-tool"] != 1 || tools.effects["next-tool"] != 1 {
				t.Fatalf("selected effects: batches=%d admissions=%d defaults=%d tools=%v", batch.executions.Load(), batch.admissions.Load(), initialized.Load(), tools.effects)
			}
			outcomes, err := ledger.PendingProjection(ctx)
			if err != nil || len(outcomes) != 2 || outcomes[0].ID == outcomes[1].ID {
				t.Fatalf("durable tool receipts: %+v %v", outcomes, err)
			}
			restored, found, err := testSessionStore(t, dir).Find("model-batch")
			if err != nil || !found || restored.ModelSelectionRevision != 2 || restored.ModelSelectionApplyAt != "next_step" || restored.Provider != "next" || restored.Model != "two" {
				t.Fatalf("atomic intent did not survive a fresh store client: %+v found=%t err=%v", restored, found, err)
			}
			receipts, err := store.LoadModelInputReceipts("model-batch")
			if err != nil || len(receipts) != 3 {
				t.Fatalf("model input receipts: %+v %v", receipts, err)
			}
			for index, record := range receipts {
				var receipt agent.ModelInputReceipt
				if err := json.Unmarshal(record.Payload, &receipt); err != nil {
					t.Fatal(err)
				}
				wantProvider, wantRevision := "old", uint64(1)
				if index == 2 {
					wantProvider, wantRevision = "next", 2
				}
				if receipt.Provider != wantProvider || receipt.ModelSelectionRevision != wantRevision {
					t.Fatalf("receipt %d lost frozen selection: %+v", index, receipt)
				}
			}
			t.Log("old-model retry stayed frozen; both steps used external batch/tools, streamed receipts joined once, no native initialization")
		})
	}
}

type modelStepBatchDriver struct{ nativeTurnFixture }

func (d *modelStepBatchDriver) Descriptor(ctx context.Context) (loopdriver.Descriptor, error) {
	descriptor, err := d.nativeTurnFixture.Descriptor(ctx)
	descriptor.Capabilities = []string{"step_model_selection"}
	return descriptor, err
}

type modelStepBatchObserver struct {
	*selectedToolBatch
	executions, admissions atomic.Int32
}

func (b *modelStepBatchObserver) ExecuteBatch(ctx context.Context, request toolbatch.Request, resources toolbatch.Resources) error {
	b.executions.Add(1)
	return b.selectedToolBatch.ExecuteBatch(ctx, request, resources)
}
func (b *modelStepBatchObserver) AdmitStream(ctx context.Context, request toolbatch.StreamRequest) (bool, error) {
	b.admissions.Add(1)
	return b.selectedToolBatch.AdmitStream(ctx, request)
}

type modelStepBatchTools struct {
	*selectedToolExecutor
	mu       sync.Mutex
	finished map[string]chan struct{}
	effects  map[string]int
}

func (t *modelStepBatchTools) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	result, err := t.selectedToolExecutor.ExecuteResult(ctx, call)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.effects[call.ID]++
	if t.effects[call.ID] == 1 {
		close(t.finished[call.ID])
	}
	return result, err
}

type modelStepBatchModel struct {
	id         string
	wait       <-chan struct{}
	streaming  bool
	failFirst  bool
	selectNext func() error
	requests   []providers.ChatRequest
}

func (*modelStepBatchModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected nonstreaming model transport")
}
func (m *modelStepBatchModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.requests = append(m.requests, request)
	request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: request.Provider, Protocol: "fixture", Transport: "memory", Mode: "stream"})
	if m.failFirst && len(m.requests) == 1 {
		if err := m.selectNext(); err != nil {
			return nil, err
		}
		return nil, errors.New("permanent synthetic failure before any tool")
	}
	events := make(chan providers.StreamEvent, 3)
	call := &providers.ToolCall{ID: m.id, Name: "echo", Arguments: "{}"}
	events <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: call}
	events <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
	go func() {
		defer close(events)
		if m.streaming {
			select {
			case <-m.wait:
			case <-ctx.Done():
				return
			}
		}
		events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	}()
	return events, nil
}
