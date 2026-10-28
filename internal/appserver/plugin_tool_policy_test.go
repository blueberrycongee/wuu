package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

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

// Session policies decorate the actual turn executor. They must not reduce a
// selected provider's settled outcome to its text-only compatibility projection.
func TestSessionToolPolicyPreservesFinalizationMetadata(t *testing.T) {
	for _, allowRead := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("read-%t/failure-%t", allowRead, fail), func(t *testing.T) {
				base := &policyFinalizationTools{fail: fail}
				allow := []string{"extension_records", "allowed_discovery"}
				if allowRead {
					allow = append(allow, "read_file")
				}
				executor := newSessionToolPolicyExecutor(base, pluginhost.SessionToolPolicy{Allow: allow})
				run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor})
				defer run.Close()
				messages, err := run.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call", Name: "extension_records", Arguments: "{}"}}, nil)
				if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
					t.Fatalf("tool outcome: messages=%+v err=%v", messages, err)
				}
				if base.finalized != 1 || base.normalizedError != fail {
					t.Fatalf("finalizer was skipped or did not receive normalization: calls=%d error=%t", base.finalized, base.normalizedError)
				}
				result := messages[0].ToolResult
				if result.IsError != fail {
					t.Fatalf("error outcome changed: %+v", result)
				}
				if allowRead {
					if result.ModelText == nil || result.TextProjection() != "archive cursor" {
						t.Fatalf("allowed recovery projection lost: %+v", result)
					}
				} else if result.ModelText != nil || result.TextProjection() != result.Content[0].Text {
					t.Fatalf("restricted session received unrecoverable evidence: %+v", result)
				}
				definitions := messages[0].DiscoveredTools
				if len(definitions) != 1 || definitions[0].Name != "allowed_discovery" || definitions[0].Description != "finalized schema" {
					t.Fatalf("finalized discovery was lost or bypassed policy: %+v", definitions)
				}
				segments := run.TakeRequestContextSegments()
				if len(segments) != 1 || len(segments[0].Blocks) != 1 || segments[0].Blocks[0].Content != "execute context\n\nfinalize context" {
					t.Fatalf("call-owned context was lost or reordered: %+v", segments)
				}
				if len(run.TakeRequestContextSegments()) != 0 {
					t.Fatal("finalization context was consumed twice")
				}
			})
		}
	}
}

type policyFinalizationTools struct {
	fail            bool
	finalized       int
	normalizedError bool
	contextTaken    bool
}

func (*policyFinalizationTools) Definitions() []providers.ToolDefinition { return nil }
func (e *policyFinalizationTools) Execute(context.Context, providers.ToolCall) (string, error) {
	if e.fail {
		return "", errors.New("execution failed")
	}
	return "full evidence", nil
}
func (e *policyFinalizationTools) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	return e.FinalizeToolOutcome(call, result).Result
}
func (e *policyFinalizationTools) FinalizeToolOutcome(_ providers.ToolCall, result toolresult.Result) agent.ToolFinalization {
	e.finalized++
	e.normalizedError = result.IsError
	projection := "archive cursor"
	result.ModelText = &projection
	return agent.ToolFinalization{Result: result, AdditionalContext: "finalize context", DiscoveredTools: []providers.LoadableToolDefinition{
		{Name: "allowed_discovery", Description: "finalized schema"},
		{Name: "denied_discovery", Description: "must remain hidden"},
	}}
}
func (*policyFinalizationTools) DiscoveredTools(providers.ToolCall) []providers.LoadableToolDefinition {
	return []providers.LoadableToolDefinition{{Name: "allowed_discovery", Description: "execute schema"}, {Name: "denied_discovery"}}
}
func (e *policyFinalizationTools) TakeAdditionalContext(providers.ToolCall) string {
	if e.contextTaken {
		return ""
	}
	e.contextTaken = true
	return "execute context"
}
