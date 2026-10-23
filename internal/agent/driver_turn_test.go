package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
