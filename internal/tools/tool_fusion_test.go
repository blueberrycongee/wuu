package tools

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestFusionRejectsUnknownFieldsBeforeAdmission(t *testing.T) {
	called := false
	tool := &FusionDelegateTool{env: &Env{FusionDelegate: func(context.Context, string, FusionDelegateRequest) (any, error) { called = true; return nil, nil }}}
	for _, raw := range []string{`{"message":"brief","blok":false}`, `{"message":"brief"} {}`} {
		if _, err := tool.ExecuteResultCall(context.Background(), providers.ToolCall{Arguments: raw}); err == nil || called {
			t.Fatalf("invalid JSON reached admission: %s", raw)
		}
	}
}

// Invalid control requests must fail before the handler can cancel work or
// admit a message. In particular stop must never silently discard a message.
func TestFusionRequestActionBoundaries(t *testing.T) {
	for _, request := range []FusionDelegateRequest{
		{Action: "stop", Message: "please return your findings", Reason: "takeover"},
		{Action: "update", Message: "new requirement"},
		{Action: "review", TaskID: "task", ReportID: "report", Revision: 1, Verdict: "accept"},
		{Action: "inspect", Message: "ignored"},
		{Action: "wait", TaskID: "task", TimeoutMS: -1},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
	for _, request := range []FusionDelegateRequest{
		{Message: "Complete self-contained brief"},
		{Action: "update", TaskID: "task", Message: "wrap up and return findings"},
		{Action: "stop", TaskID: "task", Reason: "user cancelled"},
		{Action: "review", TaskID: "task", ReportID: "report", Revision: 1, Verdict: "accept", Message: "verified actual diff and checks"},
	} {
		if err := request.Validate(); err != nil {
			t.Fatalf("valid request rejected: %v", err)
		}
	}
}
