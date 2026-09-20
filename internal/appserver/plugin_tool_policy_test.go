package appserver

import (
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
