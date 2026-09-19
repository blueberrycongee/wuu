package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBuiltinHandoffRequestsUserConfiguration(t *testing.T) {
	tool := NewRequestHandoffTool(&Env{SessionID: "source"})
	result, err := tool.ExecuteResultCall(context.Background(), providers.ToolCall{ID: "request", Arguments: `{"intent":"continue the verified fix"}`})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		RequestID string `json:"request_id"`
		Source    string `json:"source_session_id"`
		Awaiting  bool   `json:"awaiting_user_configuration"`
		Intent    string `json:"intent"`
	}
	if err := json.Unmarshal([]byte(result.TextProjection()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RequestID != "request" || payload.Source != "source" || !payload.Awaiting || payload.Intent != "continue the verified fix" {
		t.Fatalf("unexpected handoff=%+v", payload)
	}
	if _, err := tool.Execute(context.Background(), `{"model":"chosen-by-agent"}`); err == nil {
		t.Fatal("agent selected the destination model")
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	display, ok := kit.ToolDisplay(providers.ToolCall{Name: tool.Name()})
	if !ok || display.Kind != "handoff" || display.Capability != "handoff" {
		t.Fatalf("handoff action unavailable: %+v", display)
	}
}
