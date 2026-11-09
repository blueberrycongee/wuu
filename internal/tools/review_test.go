package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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

func TestReviewerCredentialPathsDoNotMatchOrdinaryText(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, ".wuu")
	t.Setenv("WUU_HOME", home)
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow}}
	kit := &Toolkit{env: &Env{RootDir: root, PermissionMode: "standard"}, boundary: StandardBoundary(), reviewer: reviewer, approveForMe: true}
	for _, command := range []string{"echo auth.json", "echo credentials.json"} {
		args, _ := json.Marshal(map[string]string{"command": command})
		if err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: string(args)}); err != nil {
			t.Fatalf("harmless filename mention denied: %v", err)
		}
	}
	for _, args := range []map[string]string{
		{"command": "cat '" + filepath.Join(home, "auth.json") + "'"},
		{"command": "cat credentials.json", "cwd": home},
		{"command": "cd .wuu && cat remote.json"},
		{"command": "cat \"$WUU_HOME/auth.json\""},
	} {
		encoded, _ := json.Marshal(args)
		err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: string(encoded)})
		if err == nil || !strings.Contains(err.Error(), "credential files") {
			t.Fatalf("credential read allowed: %v", err)
		}
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("credential calls must bypass reviewer, got %d requests", len(reviewer.requests))
	}
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
	hard := kit.checkPermission(context.Background(), ToolInfo{Name: "update_runtime", Risk: ToolRiskHigh}, providers.ToolCall{Name: "update_runtime", Arguments: `{"permission_mode":"unconfined"}`})
	if hard == nil || !strings.Contains(hard.Error(), "cannot raise the session permission mode") {
		t.Fatalf("hard deny = %v", hard)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("hard deny should skip the reviewer, requests = %+v", reviewer.requests)
	}
	if !strings.Contains(hard.Error(), "error_kind=review_forbidden") || !strings.Contains(hard.Error(), "do not retry") {
		t.Fatalf("hard denial must not invite a conversational override: %v", hard)
	}
}

func TestReviewerReportsUncertaintyFailureAndCancellationSeparately(t *testing.T) {
	for _, tc := range []struct {
		outcome, kind string
		err           error
	}{
		{approvefor.OutcomeDeny, "review_denied", nil},
		{approvefor.OutcomeUnsure, "review_needs_context", nil},
		{approvefor.OutcomeFailed, "review_failed", nil},
		{approvefor.OutcomeCancelled, "review_cancelled", nil},
		{"invalid", "review_failed", nil},
		{"", "review_failed", errors.New("unavailable")},
	} {
		t.Run(tc.kind+tc.outcome, func(t *testing.T) {
			kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), approveForMe: true,
				reviewer: &recordingReviewer{decision: approvefor.Decision{Outcome: tc.outcome, Reason: "specific reason"}, err: tc.err}}
			err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash"})
			if err == nil || !strings.Contains(err.Error(), "error_kind="+tc.kind) || !strings.Contains(err.Error(), "action_executed=false") {
				t.Fatalf("unexpected review error: %v", err)
			}
		})
	}
}

func TestReviewerSeesShellContentInsteadOfSubstringDenial(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "review shell effects"}}
	kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), approveForMe: true, reviewer: reviewer}
	err := kit.checkPermission(context.Background(), ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}, providers.ToolCall{Name: "bash", Arguments: `{"command":"echo permission_mode=unconfined"}`})
	if err == nil || len(reviewer.requests) != 1 || !strings.Contains(err.Error(), "review shell effects") {
		t.Fatalf("shell content must still be reviewed, not auto-allowed or hard denied: %v", err)
	}
}

func TestReviewerReadOnlyShellStillRequiresReview(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "requires review"}}
	kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), approveForMe: true, reviewer: reviewer}
	info := ToolInfo{Name: "bash", Kind: ToolKindShell, ReadOnly: true, Risk: ToolRiskLow}
	call := providers.ToolCall{Name: "bash", Arguments: `{"command":"cat README.md"}`}
	if err := kit.checkPermission(context.Background(), info, call); err == nil || !strings.Contains(err.Error(), "review_denied") {
		t.Fatalf("read-only shell skipped review: %v", err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("expected one review, got %d", len(reviewer.requests))
	}
	kit.reviewer = nil
	if err := kit.checkPermission(context.Background(), info, call); err == nil || !strings.Contains(err.Error(), "review_failed") {
		t.Fatalf("missing reviewer must fail closed: %v", err)
	}
}

func TestReviewerCannotOverrideBoundaryAndDoesNotCacheAllow(t *testing.T) {
	reviewer := &recordingReviewer{decision: approvefor.Decision{Outcome: approvefor.OutcomeAllow, Reason: "approved exact action"}}
	kit := &Toolkit{env: &Env{PermissionMode: "standard"}, boundary: StandardBoundary(), approveForMe: true, reviewer: reviewer}
	info := ToolInfo{Name: "bash", Kind: ToolKindShell, Risk: ToolRiskHigh}
	call := providers.ToolCall{Name: "bash", Arguments: `{"command":"cleanup build"}`}
	if err := kit.checkPermission(context.Background(), info, call); err != nil {
		t.Fatal(err)
	}
	reviewer.decision = approvefor.Decision{Outcome: approvefor.OutcomeDeny, Reason: "different target"}
	call.Arguments = `{"command":"cleanup source"}`
	if err := kit.checkPermission(context.Background(), info, call); err == nil {
		t.Fatal("previous approval was reused")
	}
	if len(reviewer.requests) != 2 {
		t.Fatal("each action needs fresh review")
	}
	kit.boundary = ReadOnlyBoundary()
	info.ReadOnly = false
	if err := kit.checkPermission(context.Background(), info, call); err == nil {
		t.Fatal("review overrode read-only boundary")
	}
	if len(reviewer.requests) != 2 {
		t.Fatal("boundary must reject before the reviewer")
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
