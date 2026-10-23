package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if _, err := gateway.CallModel(context.Background(), request); !errors.Is(err, archiveErr) {
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
