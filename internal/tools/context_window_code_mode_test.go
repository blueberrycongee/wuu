package tools

import "testing"

func TestCodeModeExposesContextSwitchAtTopLevel(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetContextWindowToolsEnabled(true)
	if !contains(newContextToolName, kit.Definitions()) {
		t.Fatal("context switch unavailable in code-mode")
	}
	nested, err := kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	if contains(newContextToolName, codeModeDefsToProviderDefs(nested)) {
		t.Fatal("nested reset would acknowledge a signal the agent loop cannot consume")
	}
	kit.SetContextWindowToolsEnabled(false)
	if contains(newContextToolName, kit.Definitions()) {
		t.Fatal("disabled extension still exposes context switching")
	}
}
