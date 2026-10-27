package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type recordingAuthorizer struct {
	requests []AuthorizationRequest
	decision AuthorizationDecision
	err      error
}

func (a *recordingAuthorizer) Authorize(_ context.Context, request AuthorizationRequest) (AuthorizationDecision, error) {
	a.requests = append(a.requests, request)
	return a.decision, a.err
}

func TestAuthorizerMayFurtherRestrictAllowedToolCall(t *testing.T) {
	authorizer := &recordingAuthorizer{decision: AuthorizationDecision{Outcome: "deny", Reason: "project policy"}}
	kit := &Toolkit{env: &Env{RootDir: "/workspace", SessionID: "thread-1", AgentID: "agent-1", PermissionMode: "standard"}, boundary: StandardBoundary(), authorizer: authorizer}
	info := ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh, Destructive: true}
	err := kit.checkPermission(context.Background(), info, providers.ToolCall{Name: "bash", Arguments: `{"command":"rm -rf build"}`})
	if err == nil || !strings.Contains(err.Error(), "project policy") {
		t.Fatalf("error = %v", err)
	}
	if len(authorizer.requests) != 1 || authorizer.requests[0].PermissionMode != "standard" || authorizer.requests[0].Arguments == "" {
		t.Fatalf("requests = %+v", authorizer.requests)
	}
}

func TestAuthorizerFailureAndInvalidOutcomeFailClosed(t *testing.T) {
	for _, authorizer := range []*recordingAuthorizer{
		{err: errors.New("offline")},
		{decision: AuthorizationDecision{Outcome: "ask"}},
	} {
		kit := &Toolkit{env: &Env{}, boundary: StandardBoundary(), authorizer: authorizer}
		if err := kit.checkPermission(context.Background(), ToolInfo{Name: "read_file", ReadOnly: true}, providers.ToolCall{Name: "read_file"}); err == nil {
			t.Fatal("expected fail-closed denial")
		}
	}
}

func TestAuthorizerCannotOverrideWorkspaceBoundary(t *testing.T) {
	authorizer := &recordingAuthorizer{decision: AuthorizationDecision{Outcome: "allow"}}
	kit := &Toolkit{env: &Env{}, boundary: ReadOnlyBoundary(), authorizer: authorizer}
	err := kit.checkPermission(context.Background(), ToolInfo{Name: "write_file", Kind: ToolKindFile}, providers.ToolCall{Name: "write_file"})
	if err == nil || len(authorizer.requests) != 1 {
		t.Fatalf("error = %v requests = %+v", err, authorizer.requests)
	}
}

func TestAuthorizerDenialPrecedesReviewer(t *testing.T) {
	authorizer := &recordingAuthorizer{decision: AuthorizationDecision{Outcome: "deny", Reason: "selected policy"}}
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow}}
	kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), authorizer: authorizer, reviewer: reviewer, approveForMe: true}
	err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: `{"command":"rm build/output"}`})
	if err == nil || !strings.Contains(err.Error(), "selected policy") || len(reviewer.requests) != 0 {
		t.Fatalf("policy denial reached reviewer: err=%v reviews=%+v", err, reviewer.requests)
	}
}
