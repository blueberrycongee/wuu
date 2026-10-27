package runtime

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Engines must be able to round-trip durable history and recovery receipts
// without depending on Go field names or private model structs.
func TestEngineWirePortableReceipts(t *testing.T) {
	raw := []byte(`{"version":1,"content":"done","new_messages":[{"role":"assistant","content":"done","client_id":"client","seq":8,"provider_item_id":"item","provider_item_provider":"openai","provider_item_model":"model","reasoning_blocks":[{"type":"thinking","thinking":"trace","signature":"sig"}],"provider_items":[{"type":"opaque","data":"{\"x\":1}","provider":"openai","scope":"session"}],"images":[{"media_type":"image/png","data":"YWJj","provider_item_id":"image"}],"tool_calls":[{"id":"call","name":"test","arguments":"{}","provider_item_id":"call-item"}]}],"durable_new_messages":[{"role":"tool","tool_call_id":"call","tool_invocation_id":"invocation","tool_result":{"model_text":"","structured_content":{"ok":true},"meta":{"native":1},"content":[{"type":"text","text":"hidden projection","artifact":{"size_bytes":0}}]}}],"durable_messages_tracked":true,"history_archive_head_seq":7,"history_rewritten":true,"retained_request_context":{"durable_len":1,"durable_hash":"hash","messages":[{"after_durable":1,"message":{"role":"system","content":"retained","hidden":true}}]},"input_tokens":10,"output_tokens":20,"cache_creation_tokens":3,"cache_read_tokens":4,"context_tokens":37,"finish_reason":"stop","stop_reason":"end_turn","receipt_diagnostic":"accounting pending","driver_id":"candidate","driver_version":"2","driver_contract_version":1,"driver_status":"completed","driver_checkpoint":{"receipt":"immutable"}}`)
	var wire pluginprotocol.EngineResultV1
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	result, err := engineResultFromWire(wire)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(engineResultToWire(result))
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	// Compare the canonical transport representation, not implementation fields.
	canonical, _ := json.Marshal(before)
	roundtrip, _ := json.Marshal(after)
	if string(canonical) != string(roundtrip) {
		t.Fatalf("receipt changed:\n%s\n%s", canonical, roundtrip)
	}
	if result.DurableNewMessages[0].ToolResult.ModelText == nil || *result.DurableNewMessages[0].ToolResult.ModelText != "" {
		t.Fatal("explicit empty projection lost")
	}
}

func TestEngineWireVersionAndPartialFailure(t *testing.T) {
	if _, err := engineResultFromWire(pluginprotocol.EngineResultV1{Version: 2}); err == nil {
		t.Fatal("unknown result version accepted")
	}
	if _, err := engineEventFromWire(pluginprotocol.EngineEventV1{Version: 2}); err == nil {
		t.Fatal("unknown event version accepted")
	}
	event := providers.StreamEvent{Type: providers.EventError, Content: "partial", Error: errors.New("interrupted")}
	restored, err := engineEventFromWire(engineEventToWire(event))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Content != "partial" || restored.Error == nil {
		t.Fatal("partial failure lost")
	}
	result, err := engineResultFromWire(engineResultToWire(agent.LoopResult{Content: "partial"}))
	if err != nil || result.Content != "partial" {
		t.Fatal(result, err)
	}
}

func TestEngineWireStreamCheckpointAndRecovery(t *testing.T) {
	// A complete assistant item carries history addresses even though it does not
	// change live projection. Unknown event names remain forward-compatible.
	source := providers.StreamEvent{Type: "assistant_item_done", Message: &providers.ChatMessage{Role: "assistant", Content: "checkpoint", ClientID: "client", Seq: 42, Steered: true, ProviderItemID: "item"},
		Lifecycle:     &providers.StreamLifecycle{Phase: providers.StreamPhaseReconnecting, OperationID: "operation", AttemptID: "attempt", Attempt: 2, RetryIn: 1500000001, ResetPartial: true, Workflow: providers.WorkflowBudgetSnapshot{WorkflowID: "workflow", UnknownBillableSubmissions: 1}},
		TodoUpdate:    &providers.TodoUpdate{Todos: []providers.TodoItem{{Content: "task", Status: "running"}}},
		AgentActivity: &providers.AgentActivity{ID: "child", Engine: "external", State: providers.AgentActivityRunning},
		ProviderState: &providers.ProviderStateSummary{PreviousResponseIDUsed: true, FallbackPinStatus: "pinned", FallbackRetryAfterMS: 3000},
		Usage:         &providers.TokenUsage{InputTokens: 2, CacheReadTokens: 3, CacheCreationUnknown: true},
	}
	wire := engineEventToWire(source)
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var restoredWire pluginprotocol.EngineEventV1
	if err = json.Unmarshal(raw, &restoredWire); err != nil {
		t.Fatal(err)
	}
	restored, err := engineEventFromWire(restoredWire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, restored) {
		t.Fatalf("stream checkpoint changed:\n%#v\n%#v", source, restored)
	}
	failure := pluginprotocol.DriverFailure{Message: "interrupted", Code: "cancelled", Origin: "host", Category: "cancelled", StopReason: "user_interrupt", Recovery: &pluginprotocol.DriverModelRecovery{OperationID: "operation", AttemptCount: 2, StopReason: "cancelled"}}
	restored, err = engineEventFromWire(pluginprotocol.EngineEventV1{Version: 1, Type: "error", Failure: &failure})
	if err != nil {
		t.Fatal(err)
	}
	if actual := engineEventToWire(restored).Failure; !reflect.DeepEqual(&failure, actual) {
		t.Fatalf("recovery receipt lost: %#v", actual)
	}
	restored.Error = errors.Join(restored.Error, errors.New("callback receipt persistence failed"))
	if actual := engineEventToWire(restored).Failure; actual.Message != restored.Error.Error() || actual.Code != failure.Code {
		t.Fatalf("joined failure diagnostic lost: %#v", actual)
	}
}

func TestEngineWireCanonicalPresentation(t *testing.T) {
	source := providers.ChatMessage{Role: "user", Content: "model input", DisplayContent: "authored input", PresentationKind: "selection", RelatedSessionID: "child", EngineTurnID: "native-turn", EngineTurnPending: true,
		ContentParts: []providers.MessageContentPart{{Type: "file_selection", Text: "quote", Source: &providers.FileSelectionSource{Workspace: "workspace", Path: "file.pdf", PDF: &providers.PDFSelectionReference{StartPage: 1, EndPage: 2, ArtifactURI: "artifact://immutable", ArtifactSHA256: "sha"}}, Selection: &providers.ResponseSelection{ID: "quote", Text: "selected", Source: providers.ResponseSelectionSource{ThreadID: "thread", TurnID: "turn", ItemID: "item", StartOffset: 1, EndOffset: 4, RangeText: "raw"}}}},
		ToolCalls:    []providers.ToolCall{{ID: "call", Name: "tool", Display: &providers.ToolCallDisplay{Label: "Inspect", Text: "file.pdf"}}},
	}
	encoded, err := json.Marshal(engineResultToWire(agent.LoopResult{NewMessages: []providers.ChatMessage{source}}))
	if err != nil {
		t.Fatal(err)
	}
	var wire pluginprotocol.EngineResultV1
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	restored, err := engineResultFromWire(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, restored.NewMessages[0]) {
		t.Fatalf("canonical presentation changed:\n%#v\n%#v", source, restored.NewMessages[0])
	}
}

func TestEngineWirePreservesCanonicalWorkingCopiesOnly(t *testing.T) {
	source := providers.ChatMessage{Role: "user", Content: "input", DisplayContent: "", Images: []providers.InputImage{{MediaType: "image/png", Data: "AA==", LocalPath: "/host/image.png"}}, Files: []providers.InputFile{{MediaType: "application/pdf", Data: "AA==", LocalPath: "/host/file.pdf"}}, ContentParts: []providers.MessageContentPart{{Type: "pasted_text", Text: "authored snapshot", LocalPath: "/host/pasted.txt"}}}
	wire := engineResultToWire(agent.LoopResult{NewMessages: []providers.ChatMessage{source}})
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var copied pluginprotocol.EngineResultV1
	if err = json.Unmarshal(encoded, &copied); err != nil {
		t.Fatal(err)
	}
	restored, err := engineResultFromWire(copied)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, restored.NewMessages[0]) {
		t.Fatalf("canonical working-copy metadata lost: %#v", restored.NewMessages[0])
	}
	projected, err := json.Marshal(modelwire.ChatMessageToWire(restored.NewMessages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(projected), "/host/") || strings.Contains(string(projected), "working_copies") {
		t.Fatalf("host-only metadata escaped model projection: %s", projected)
	}
}
