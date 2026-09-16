package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type recordingReviewer struct {
	requests []approvefor.Request
	decision approvefor.Decision
	err      error
}

func (r *recordingReviewer) Review(_ context.Context, request approvefor.Request) (approvefor.Decision, error) {
	r.requests = append(r.requests, request)
	return r.decision, r.err
}

func TestReviewerCanAllowHighRiskCallAfterBoundary(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow, Reason: "local cleanup"}}
	kit := &Toolkit{
		env:          &Env{RootDir: "/workspace", SessionID: "thread-1", PermissionMode: "standard"},
		boundary:     StandardBoundary(),
		reviewer:     reviewer,
		approveForMe: true,
	}
	info := ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh, Destructive: true}
	if err := kit.checkPermission(context.Background(), info, providers.ToolCall{Name: "bash", ID: "call-1", Arguments: `{"command":"rm -rf build"}`}); err != nil {
		t.Fatalf("allow = %v", err)
	}
	if len(reviewer.requests) != 1 || reviewer.requests[0].CallID != "call-1" {
		t.Fatalf("requests = %+v", reviewer.requests)
	}
}

func TestReviewerDenyAndHardDenyFailClosed(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "destructive"}}
	kit := &Toolkit{
		env:          &Env{RootDir: "/workspace", PermissionMode: "standard"},
		boundary:     StandardBoundary(),
		reviewer:     reviewer,
		approveForMe: true,
	}
	err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: `{"command":"rm -rf /"}`})
	if err == nil || !strings.Contains(err.Error(), "error_kind=review_denied") {
		t.Fatalf("deny = %v", err)
	}
	hard := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: `{"command":"export permission_mode=unconfined"}`})
	if hard == nil || !strings.Contains(hard.Error(), "cannot raise the session permission mode") {
		t.Fatalf("hard deny = %v", hard)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("hard deny should skip the reviewer, requests = %+v", reviewer.requests)
	}
}

func TestReviewerDoesNotRunWhenDisabled(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeDeny}}
	kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), reviewer: reviewer}
	if err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash"}); err != nil {
		t.Fatalf("disabled review = %v", err)
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("requests = %+v", reviewer.requests)
	}
}
