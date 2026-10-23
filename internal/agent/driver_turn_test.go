package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

// These checks exercise a driver which decides its own control flow. A model
// response must never cause tool execution without a separate driver request.
func TestDriverTurnOperationsSeparateModelAndToolExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("measured value: 42"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := &driverEvidenceTools{path: path}
	call := providers.ToolCall{ID: "read-1", Name: "read_evidence", Arguments: "{}"}
	client := &mockStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventToolUseStart, ToolCall: &call},
		{Type: providers.EventToolUseEnd, ToolCall: &call},
		{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls},
	}}
	runner := &StreamRunner{Client: client, Model: "controlled-model", Tools: tools, StreamingToolExecution: true}
	gateway := newStreamTurnGateway(context.Background(), runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "Read the measured value"}}}, nil)
	defer gateway.close()
	request, err := gateway.ReadTurn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	round, err := gateway.CallModel(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response := round.Messages[len(round.Messages)-1]
	if tools.calls != 0 || len(response.ToolCalls) != 1 {
		t.Fatalf("model executed tools or lost calls: effects=%d response=%+v", tools.calls, response)
	}
	history := append(request.Messages, response)
	messages, err := gateway.CallTools(context.Background(), loopdriver.ToolBatch{ID: "collect-evidence", Messages: history, Calls: response.ToolCalls})
	if err != nil {
		t.Fatal(err)
	}
	if tools.calls != 1 || len(messages) != 1 || messages[0].Content != "measured value: 42" {
		t.Fatalf("tool effects=%d messages=%+v", tools.calls, messages)
	}
	if _, err := gateway.CallTools(context.Background(), loopdriver.ToolBatch{ID: "collect-evidence", Messages: history, Calls: response.ToolCalls}); err == nil || tools.calls != 1 {
		t.Fatalf("duplicate operation repeated an effect: calls=%d err=%v", tools.calls, err)
	}
	receipt, err := gateway.CompleteTurn(context.Background(), loopdriver.TurnResult{Content: messages[0].Content})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := gateway.result(receipt.ID)
	if !ok || result.Content != "measured value: 42" || len(result.NewMessages) != 2 {
		t.Fatalf("completion lost history: %+v found=%v", result, ok)
	}
	if _, err := gateway.CallModel(context.Background(), request); err == nil || len(client.requests) != 1 {
		t.Fatalf("completed turn accepted another model call: calls=%d err=%v", len(client.requests), err)
	}
}

// A driver explicitly opts into overlap before the model starts. Its receipt
// joins the same effects exactly once, and a provider stays alive until even a
// cancellation-resistant tool has physically left its execution function.
func TestDriverTurnStreamingTools(t *testing.T) {
	for _, abandon := range []bool{false, true} {
		t.Run(map[bool]string{false: "join", true: "close"}[abandon], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tools := &driverStreamingTools{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
			client := &driverStreamingModel{started: tools.started}
			gateway := newStreamTurnGateway(ctx, &StreamRunner{Client: client, Model: "streamed", Tools: tools}, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "Read evidence"}}}, nil)
			defer func() { tools.releaseOnce(); gateway.close() }()
			request, err := gateway.ReadTurn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			round, err := gateway.CallModelWithTools(ctx, request, "measure")
			if err != nil || round.ToolBatchID != "measure" || !round.ToolWorkStarted || tools.calls.Load() != 1 {
				t.Fatalf("streamed effect lost its receipt: round=%+v calls=%d err=%v", round, tools.calls.Load(), err)
			}
			select {
			case <-tools.canceled:
				t.Fatal("returning the model response canceled its tool")
			default:
			}
			if _, err := gateway.CallModel(ctx, request); err == nil {
				t.Fatal("another model operation overtook pending streamed tools")
			}
			if _, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{}); err == nil {
				t.Fatal("completion abandoned streamed tools")
			}
			if _, err := gateway.AdmitInput(ctx); err == nil {
				t.Fatal("input interrupted the pending assistant/tool block")
			}
			if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "unrelated", Calls: []providers.ToolCall{{ID: "another-call", Name: "read_evidence", Arguments: "{}"}}}); err == nil {
				t.Fatal("another tool batch interrupted pending streamed effects")
			}
			if abandon {
				closed := make(chan struct{})
				go func() { gateway.close(); close(closed) }()
				select {
				case <-tools.canceled:
				case <-ctx.Done():
					t.Fatal("close did not cancel the streamed tool")
				}
				if tools.unfrozen.Load() {
					t.Fatal("tool surface was released before its consumer stopped")
				}
				select {
				case <-closed:
					t.Fatal("close returned before physical cleanup")
				default:
				}
				tools.releaseOnce()
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("close did not finish after tool cleanup")
				}
				if !tools.unfrozen.Load() {
					t.Fatal("tool surface was never released")
				}
				return
			}
			calls := round.Messages[0].ToolCalls
			changed := append([]providers.ToolCall(nil), calls...)
			changed[0].Arguments = `{"changed":true}`
			if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: round.ToolBatchID, Calls: changed}); err == nil {
				t.Fatal("stream receipt admitted changed arguments")
			}
			if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "other", Calls: calls}); err == nil {
				t.Fatal("another batch reused a streamed call ID")
			}
			tools.releaseOnce()
			messages, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: round.ToolBatchID, Calls: calls})
			if err != nil || len(messages) != 1 || messages[0].Content != "42" || tools.calls.Load() != 1 {
				t.Fatalf("join lost or repeated effects: messages=%+v calls=%d err=%v", messages, tools.calls.Load(), err)
			}
			if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: round.ToolBatchID, Calls: calls}); err == nil {
				t.Fatal("joined receipt was reused")
			}
			if _, err := gateway.CallModelWithTools(ctx, request, "measure"); err == nil {
				t.Fatal("model reused an accepted batch ID")
			}
			if _, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Content: "42"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type driverStreamingTools struct {
	started, canceled, release chan struct{}
	calls                      atomic.Int32
	unfrozen                   atomic.Bool
	released                   atomic.Bool
}

func (t *driverStreamingTools) releaseOnce() {
	if t.released.CompareAndSwap(false, true) {
		close(t.release)
	}
}
func (*driverStreamingTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_evidence"}}
}
func (*driverStreamingTools) ToolMetadata(providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}
func (*driverStreamingTools) FreezeToolSurface()     {}
func (t *driverStreamingTools) UnfreezeToolSurface() { t.unfrozen.Store(true) }
func (t *driverStreamingTools) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	t.calls.Add(1)
	close(t.started)
	select {
	case <-ctx.Done():
		close(t.canceled)
		<-t.release
		return "", ctx.Err()
	case <-t.release:
		return "42", nil
	}
}

func TestDriverTurnStreamingFailureDoesNotReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools := &driverStreamingTools{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	tools.releaseOnce()
	client := &driverStreamingModel{started: tools.started, fail: true}
	gateway := newStreamTurnGateway(ctx, &StreamRunner{Client: client, Model: "streamed", Tools: tools}, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "Read evidence"}}}, nil)
	defer gateway.close()
	request, err := gateway.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	round, err := gateway.CallModelWithTools(ctx, request, "measure")
	if err == nil || !round.ToolWorkStarted || round.ToolBatchID != "measure" || client.calls.Load() != 1 || tools.calls.Load() != 1 {
		t.Fatalf("failed stream lost effects or replayed: round=%+v models=%d tools=%d err=%v", round, client.calls.Load(), tools.calls.Load(), err)
	}
	if len(round.Messages) != 1 || len(round.Messages[0].ToolCalls) != 1 {
		t.Fatalf("failure concealed partial output: %+v", round)
	}
	current, err := gateway.ReadContext(ctx)
	if err != nil || len(current.Messages) != 2 || current.Messages[1].Content != "visible partial" || len(current.Messages[1].ToolCalls) != 0 || current.Messages[1].ProviderItemID != "" {
		t.Fatalf("incomplete tool declaration became replayable history: context=%+v err=%v", current, err)
	}
	if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "retry", Calls: round.Messages[0].ToolCalls}); err == nil {
		t.Fatal("failed streamed effect was executed again")
	}
}

type driverStreamingModel struct {
	started <-chan struct{}
	fail    bool
	calls   atomic.Int32
}

func (*driverStreamingModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *driverStreamingModel) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.calls.Add(1)
	stream := make(chan providers.StreamEvent, 4)
	call := providers.ToolCall{ID: "streamed-read", Name: "read_evidence", Arguments: "{}"}
	stream <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
	stream <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
	go func() {
		defer close(stream)
		select {
		case <-m.started:
			if m.fail {
				stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "visible partial", ProviderItemID: "incomplete"}
				stream <- providers.StreamEvent{Type: providers.EventError, Error: providers.NewIncompleteStreamError("temporary drop after effect")}
			} else {
				stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
			}
		case <-ctx.Done():
			stream <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}
	}()
	return stream, nil
}

func TestDriverTurnCancellationAndClosureRejectEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tools := &driverEvidenceTools{}
	gateway := newStreamTurnGateway(ctx, &StreamRunner{Tools: tools}, loopdriver.PersistedInput{}, nil)
	cancel()
	_, err := gateway.CallTools(context.Background(), loopdriver.ToolBatch{ID: "late", Calls: []providers.ToolCall{{ID: "late", Name: "read_evidence", Arguments: "{}"}}})
	if !errors.Is(err, context.Canceled) || tools.calls != 0 {
		t.Fatalf("canceled turn accepted effects: calls=%d err=%v", tools.calls, err)
	}
	gateway.close()
	if _, err := gateway.ReadTurn(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed turn accepted a late callback: %v", err)
	}
}

// A request receipt is a dispatch barrier, including when streaming tools are
// enabled. Failed writes and cancellation must leave the request unsubmitted;
// successful writes identify the exact request and the selected driver.
func TestDriverTurnModelReceiptBarrier(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, cancelWrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("streaming-%t/cancel-%t", streaming, cancelWrite), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "receipt-session", ExecutionID: "receipt-execution"})
				client := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}}}
				writeErr := errors.New("receipt storage unavailable")
				var receipts []ModelInputReceipt
				runner := &StreamRunner{Client: client, ProviderName: "bound", Model: "controlled", Tools: &driverEvidenceTools{},
					ModelInputReceiptStore: driverReceiptStoreFunc(func(_ context.Context, receipt ModelInputReceipt) error {
						if len(client.requests) != 0 {
							t.Fatal("model started before its receipt was committed")
						}
						receipts = append(receipts, receipt)
						if cancelWrite {
							cancel()
							return nil
						}
						return writeErr
					}),
				}
				gateway := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "original", ClientID: "input", Seq: 7}}}, nil)
				gateway.descriptor = loopdriver.Descriptor{ID: "independent-loop", Version: "1.2.0"}
				defer gateway.close()
				request, err := gateway.ReadTurn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				request.Messages = append(request.Messages, providers.ChatMessage{Role: "user", Content: "request-only context"})
				request.Temperature, request.Effort = 0.25, "high"
				request.CacheHint = &providers.CacheHint{PromptCacheKey: "bound-cache"}
				call := func() error {
					var err error
					if streaming {
						_, err = gateway.CallModelWithTools(ctx, request, "receipt-batch")
					} else {
						_, err = gateway.CallModel(ctx, request)
					}
					return err
				}
				err = call()
				if err == nil || len(client.requests) != 0 || len(receipts) != 1 {
					t.Fatalf("dispatch crossed failed receipt barrier: requests=%d receipts=%d err=%v", len(client.requests), len(receipts), err)
				}
				if cancelWrite {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cancellation after durable write: %v", err)
					}
					return
				}
				if !errors.Is(err, writeErr) {
					t.Fatalf("lost receipt error: %v", err)
				}
				writeErr = nil
				if err := call(); err != nil || len(client.requests) != 1 || len(receipts) != 2 {
					t.Fatalf("retry after failed write did not submit once: requests=%d receipts=%d err=%v", len(client.requests), len(receipts), err)
				}
				receipt, sent := receipts[1], client.requests[0]
				if receipt.OperationID == "" || receipt.OperationID != sent.Operation.ID || receipt.ExecutionID != "receipt-execution" || receipt.SessionID != "receipt-session" || receipt.DriverID != "independent-loop" || receipt.DriverVersion != "1.2.0" || receipt.StepIndex != 0 {
					t.Fatalf("receipt lost bound dispatch identity: %+v request=%+v", receipt, sent)
				}
				if !reflect.DeepEqual(receipt.Messages, sent.Messages) || !reflect.DeepEqual(receipt.InputFactSeqs, []int{7}) || receipt.Provider != "bound" || receipt.Temperature != 0.25 || receipt.Effort != "high" || receipt.PromptCacheKey != "bound-cache" || receipt.ToolSurfaceHash == "" || receipt.CreatedAt.IsZero() {
					t.Fatalf("receipt differs from submitted request: %+v request=%+v", receipt, sent)
				}
			})
		}
	}
}

type driverReceiptStoreFunc func(context.Context, ModelInputReceipt) error

func (f driverReceiptStoreFunc) SaveModelInputReceipt(ctx context.Context, receipt ModelInputReceipt) error {
	return f(ctx, receipt)
}

// Input admission, request context, and transforms are separate effects. A
// driver must be able to compact between admission and request preparation,
// then retry a model request without consuming more input or transforming twice.
func TestDriverTurnExplicitRequestStages(t *testing.T) {
	ctx := context.Background()
	client := &mockStreamClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "response"},
		{Type: providers.EventDone, FinishReason: providers.FinishReasonStop},
	}}
	var operations []string
	runner := &StreamRunner{Client: client, Model: "controlled-model",
		BeforeStep: func() []providers.ChatMessage {
			operations = append(operations, "input")
			return []providers.ChatMessage{{Role: "user", ClientID: "accepted", Content: "new direction", Steered: true}}
		},
		BeforeModelStep: func(_ context.Context, step int, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
			operations = append(operations, "before-model")
			if step != 0 || history[len(history)-1].ClientID != "accepted" {
				t.Fatalf("input boundary lost step or accepted history: step=%d history=%+v", step, history)
			}
			return nil, nil
		},
		ArchiveHistory: func(_ context.Context, messages []providers.ChatMessage) (HistoryArchive, error) {
			operations = append(operations, "archive")
			if len(messages) != 1 || messages[0].ClientID != "accepted" {
				t.Fatalf("archive lost accepted input: %+v", messages)
			}
			return HistoryArchive{Seqs: []int{2}, HeadSeq: 2}, nil
		},
		BeforeRequestContext: func() []ContextSegment {
			operations = append(operations, "context")
			return RequestOnlyContextMessages([]providers.ChatMessage{{Role: "user", Content: "dynamic guidance"}})
		},
		BeforeRequest: func(_ context.Context, request *providers.ChatRequest) error {
			operations = append(operations, "transform")
			request.Temperature = 0.25
			return nil
		},
	}
	gateway := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "initial", Seq: 1}}}, nil)
	defer gateway.close()
	request, err := gateway.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := gateway.AdmitInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted.Messages) != 1 || admitted.Messages[0].Seq != 2 || admitted.Context.ArchiveHead != 2 || len(admitted.Context.Messages) != 2 || len(client.requests) != 0 {
		t.Fatalf("admission lost durable input or invoked the model: %+v requests=%d", admitted, len(client.requests))
	}
	segments, err := gateway.ReadRequestContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = assembleModelRequest(admitted.Context.Messages, segments).Messages
	transformed, err := gateway.TransformRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Temperature != 0 || transformed.Temperature != 0.25 {
		t.Fatalf("transform mutated caller request: before=%v after=%v", request.Temperature, transformed.Temperature)
	}
	for range 2 {
		round, err := gateway.CallModel(ctx, transformed)
		if err != nil || len(round.Messages) != 1 || round.Messages[0].Role != "assistant" {
			t.Fatalf("model repeated admission or lost response: round=%+v err=%v", round, err)
		}
	}
	if want := []string{"input", "before-model", "archive", "context", "transform"}; !reflect.DeepEqual(operations, want) {
		t.Fatalf("implicit or repeated request effects: got=%v want=%v", operations, want)
	}
	if len(client.requests) != 2 || len(client.requests[0].Messages) != 3 || client.requests[0].Messages[1].Seq != 2 || client.requests[0].Messages[2].Content != "dynamic guidance" || client.requests[0].Temperature != 0.25 {
		t.Fatalf("model did not receive driver-prepared request: %+v", client.requests)
	}
	current, err := gateway.ReadContext(ctx)
	if err != nil || len(current.Messages) != 4 {
		t.Fatalf("request-only context entered durable history: %+v err=%v", current, err)
	}
}

func TestDriverTurnDoesNotDispatchSteeringWhenArchiveFails(t *testing.T) {
	client := &mockStreamClient{}
	archiveErr := errors.New("archive unavailable")
	runner := &StreamRunner{Client: client, Model: "controlled-model",
		BeforeStep: func() []providers.ChatMessage {
			return []providers.ChatMessage{{Role: "user", ClientID: "accepted", Content: "new direction", Steered: true}}
		},
		ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
			return HistoryArchive{}, archiveErr
		},
	}
	gateway := newStreamTurnGateway(context.Background(), runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "initial"}}}, nil)
	defer gateway.close()
	request, err := gateway.ReadTurn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 1 {
		t.Fatalf("read unexpectedly admitted input: %+v", request.Messages)
	}
	if _, err := gateway.AdmitInput(context.Background()); !errors.Is(err, archiveErr) {
		t.Fatalf("archive failure=%v", err)
	}
	result, _ := gateway.result("")
	if len(client.requests) != 0 || len(result.NewMessages) != 0 || len(result.DurableNewMessages) != 0 {
		t.Fatalf("failed archive dispatched steering: requests=%d result=%+v", len(client.requests), result)
	}
}

func TestDriverTurnRecoversSettledResultsOnce(t *testing.T) {
	ctx := context.Background()
	ledger, err := toolledger.New(t.TempDir(), "driver-recovery")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := &driverEvidenceTools{path: path}
	runner := &StreamRunner{Tools: tools, ToolLedger: ledger}
	call := providers.ToolCall{ID: "read-1", Name: "read_evidence", Arguments: "{}"}
	history := []providers.ChatMessage{{Role: "user", Content: "Read evidence"}, {Role: "assistant", ToolCalls: []providers.ToolCall{call}}}
	first := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: history}, nil)
	if _, err := first.CallTools(ctx, loopdriver.ToolBatch{ID: "read", Messages: history, Calls: []providers.ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	first.close()
	resumed := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: history}, nil)
	defer resumed.close()
	request, err := resumed.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 3 || request.Messages[2].ToolInvocationID == "" {
		t.Fatalf("missing recovered result: %+v", request.Messages)
	}
	receipt, err := resumed.CompleteTurn(ctx, loopdriver.TurnResult{Content: "42"})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := resumed.result(receipt.ID)
	if len(result.NewMessages) != 1 || len(result.DurableNewMessages) != 1 || tools.calls != 1 {
		t.Fatalf("duplicated recovery: %+v effects=%d", result, tools.calls)
	}
}

func TestDriverTurnRecordsDriverChosenTools(t *testing.T) {
	for _, planned := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "planned-checkpoint"}[planned], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "evidence.txt")
			if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
				t.Fatal(err)
			}
			tools := &driverEvidenceTools{path: path}
			call := providers.ToolCall{ID: "chosen", Name: "read_evidence", Arguments: "{}"}
			history := []providers.ChatMessage{{Role: "user", Content: "Read evidence"}}
			gateway := newStreamTurnGateway(ctx, &StreamRunner{Tools: tools}, loopdriver.PersistedInput{Messages: history}, nil)
			defer gateway.close()
			if planned {
				history = append(history, providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ToolCall{call}})
			}
			messages, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "chosen", Messages: history, Calls: []providers.ToolCall{call}})
			if err != nil || len(messages) != 1 || messages[0].Content != "42" {
				t.Fatalf("tool messages=%+v err=%v", messages, err)
			}
			receipt, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Content: "42"})
			if err != nil {
				t.Fatal(err)
			}
			result, _ := gateway.result(receipt.ID)
			if len(result.NewMessages) != 2 || result.NewMessages[0].Role != "assistant" || len(result.NewMessages[0].ToolCalls) != 1 || result.NewMessages[1].ToolCallID != call.ID || tools.calls != 1 {
				t.Fatalf("driver-chosen call lost its declaration: %+v effects=%d", result, tools.calls)
			}
		})
	}
}

func TestDriverTurnExecutesPendingSubsetsWithoutDuplicateDeclarations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := &driverEvidenceTools{path: path}
	calls := []providers.ToolCall{{ID: "first", Name: "read_evidence", Arguments: "{}"}, {ID: "second", Name: "read_evidence", Arguments: "{}"}}
	history := []providers.ChatMessage{{Role: "user", Content: "Read twice"}, {Role: "assistant", ToolCalls: calls}}
	gateway := newStreamTurnGateway(ctx, &StreamRunner{Tools: tools}, loopdriver.PersistedInput{Messages: history}, nil)
	defer gateway.close()
	changed := calls[0]
	changed.Arguments = `{"different":true}`
	if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "changed", Messages: history, Calls: []providers.ToolCall{changed}}); err == nil || tools.calls != 0 {
		t.Fatalf("changed call reused a declaration: effects=%d err=%v", tools.calls, err)
	}
	for _, call := range calls {
		messages, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: call.ID, Messages: history, Calls: []providers.ToolCall{call}})
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, messages...)
	}
	if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "different-batch-same-effect", Messages: history, Calls: calls[:1]}); err == nil || tools.calls != 2 {
		t.Fatalf("settled call executed again: effects=%d err=%v", tools.calls, err)
	}
	receipt, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Content: "42 twice"})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := gateway.result(receipt.ID)
	if len(result.NewMessages) != 2 || result.NewMessages[0].Role != "tool" || result.NewMessages[1].Role != "tool" {
		t.Fatalf("subset execution duplicated declarations: %+v", result)
	}
}

func TestDriverTurnRecoversPartiallyProjectedBatch(t *testing.T) {
	ctx := context.Background()
	ledger, err := toolledger.New(t.TempDir(), "driver-partial-recovery")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := &driverEvidenceTools{path: path}
	runner := &StreamRunner{Tools: tools, ToolLedger: ledger}
	calls := []providers.ToolCall{{ID: "first", Name: "read_evidence", Arguments: "{}"}, {ID: "second", Name: "read_evidence", Arguments: "{}"}}
	history := []providers.ChatMessage{{Role: "user", Content: "Read twice"}, {Role: "assistant", ToolCalls: calls}}
	first := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: history}, nil)
	messages, err := first.CallTools(ctx, loopdriver.ToolBatch{ID: "read", Messages: history, Calls: calls})
	first.close()
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, messages[0])
	resumed := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: history}, nil)
	defer resumed.close()
	request, err := resumed.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 4 || request.Messages[3].ToolInvocationID != messages[1].ToolInvocationID {
		t.Fatalf("partially projected batch duplicated its declaration: %+v", request.Messages)
	}
	if _, err := resumed.CompleteTurn(ctx, loopdriver.TurnResult{Content: "42"}); err != nil {
		t.Fatal(err)
	}
	if tools.calls != 2 {
		t.Fatalf("recovery repeated effects: %d", tools.calls)
	}
}

func TestDriverTurnRejectsMalformedAuthoredMessages(t *testing.T) {
	for _, message := range []providers.ChatMessage{
		{Role: "invented", Content: "bad role"},
		{Role: "user", ToolCalls: []providers.ToolCall{{ID: "bad", Name: "read_evidence", Arguments: "{}"}}},
		{Role: "assistant", ToolCallID: "bad"},
	} {
		t.Run(message.Role, func(t *testing.T) {
			ctx := context.Background()
			gateway := newStreamTurnGateway(ctx, &StreamRunner{}, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "initial"}}}, nil)
			defer gateway.close()
			if _, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Messages: []providers.ChatMessage{message}}); err == nil {
				t.Fatalf("accepted malformed message: %+v", message)
			}
			if _, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Content: "valid", Messages: []providers.ChatMessage{{Role: "assistant", Content: "valid"}}}); err != nil {
				t.Fatalf("rejection changed completion state: %v", err)
			}
		})
	}
}

type driverEvidenceTools struct {
	path  string
	calls int
}

func (t *driverEvidenceTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_evidence", Description: "Read measured evidence", InputSchema: map[string]any{"type": "object"}}}
}

func (t *driverEvidenceTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	t.calls++
	data, err := os.ReadFile(t.path)
	return string(data), err
}
