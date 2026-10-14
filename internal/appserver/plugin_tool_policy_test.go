package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type legacyPolicyExecutor struct{ agent.ToolExecutor }

func (legacyPolicyExecutor) Execute(context.Context, providers.ToolCall) (string, error) {
	return "partial legacy output", errors.New("legacy policy execution failed")
}

type richPolicyExecutor struct {
	agent.ToolExecutor
	result toolresult.Result
	err    error
}

func (e richPolicyExecutor) ExecuteResult(context.Context, providers.ToolCall) (toolresult.Result, error) {
	return e.result.Clone(), e.err
}

func TestSessionToolPolicyKeepsResultsRecoverable(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(t.TempDir())
	call := providers.ToolCall{ID: "records", Name: "extension_records"}
	raw := toolresult.FromText(strings.Repeat("evidence\n", 4000))
	for _, allowRead := range []bool{false, true} {
		allow := []string{call.Name}
		if allowRead {
			allow = append(allow, "read_file")
		}

		legacy := newSessionToolPolicyExecutor(legacyPolicyExecutor{ToolExecutor: noopToolExecutor{}}, pluginhost.SessionToolPolicy{Allow: allow})
		runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: legacy})
		messages, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{call}, nil)
		if err != nil || len(messages) != 1 || messages[0].ToolResult == nil || !messages[0].ToolResult.IsError {
			t.Fatalf("policy-wrapped legacy failure: %+v, %v", messages, err)
		}
		var diagnostic struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(messages[0].Content), &diagnostic); err != nil || diagnostic.Error != "legacy policy execution failed" {
			t.Fatalf("legacy policy diagnostic lost: %q, %v", messages[0].Content, err)
		}

		for _, view := range []string{"", "approved model view"} {
			for _, executionErr := range []error{nil, errors.New("visible rich failure diagnostic")} {
				hidden := toolresult.FromText("hidden producer payload")
				hidden.ModelText = &view
				base := richPolicyExecutor{ToolExecutor: noopToolExecutor{}, result: hidden, err: executionErr}
				wrapped := newSessionToolPolicyExecutor(base, pluginhost.SessionToolPolicy{Allow: allow})
				dir := t.TempDir()
				ledger, err := toolledger.New(dir, "policy-owner")
				if err != nil {
					t.Fatal(err)
				}
				runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: wrapped, Ledger: ledger, OperationID: "policy-view"})
				messages, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{call}, nil)
				if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
					t.Fatalf("rich policy execution: %+v, %v", messages, err)
				}
				settled := messages[0].ToolResult
				if settled.ModelText == nil || strings.Contains(messages[0].Content, "hidden producer payload") || settled.Content[0].Text != hidden.Content[0].Text || settled.IsError != (executionErr != nil) {
					t.Fatalf("policy restored hidden content or lost result state: %s", settled.JSONProjection())
				}
				if executionErr != nil && view == "" {
					if !strings.Contains(messages[0].Content, executionErr.Error()) {
						t.Fatalf("policy dropped failure diagnostic: %q", messages[0].Content)
					}
				} else if messages[0].Content != view {
					t.Fatalf("policy changed explicit model view: %q", messages[0].Content)
				}
				reopened, err := toolledger.New(dir, "policy-owner")
				if err != nil {
					t.Fatal(err)
				}
				pending, err := reopened.PendingProjection(context.Background())
				if err != nil || len(pending) != 1 || pending[0].Result.JSONProjection() != settled.JSONProjection() {
					t.Fatalf("policy replay changed model view: %+v, %v", pending, err)
				}
			}
		}

		executor := newSessionToolPolicyExecutor(kit, pluginhost.SessionToolPolicy{Allow: allow}).(agent.ToolResultFinalizer)
		// Built-ins may already have settled; extensions arrive with raw results.
		for _, input := range []toolresult.Result{raw, kit.FinalizeToolResult(call, raw)} {
			result := executor.FinalizeToolResult(call, input)
			if allowRead {
				if result.ModelText == nil || result.TextProjection() == raw.TextProjection() {
					t.Fatal("policy decorator bypassed result settlement")
				}
			} else if providers.ProjectToolResult(result).ToolText != raw.TextProjection() {
				t.Fatal("restricted session received evidence it cannot recover")
			}
		}
	}
}
