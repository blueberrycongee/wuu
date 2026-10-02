package tools

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"path/filepath"
	"testing"
)

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

func TestCodeModeControlToolsRemainDirectOnly(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetContextWindowToolsEnabled(true)
	for _, name := range []string{newContextToolName, "set_session_workspace"} {
		if !contains(name, kit.Definitions()) {
			t.Fatalf("control %s is not directly available", name)
		}
		nested, err := kit.CodeModeNestedSurface()
		if err != nil {
			t.Fatal(err)
		}
		if contains(name, codeModeDefsToProviderDefs(nested)) {
			t.Fatalf("control %s is nested", name)
		}
		if _, err := kit.ExecuteResult(toolctx.WithNestedCall(context.Background()), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil {
			t.Fatalf("nested control %s was accepted", name)
		}
	}
}

func TestPTCDirectWorkspaceControlDoesNotRequireDiscovery(t *testing.T) {
	kit := newCodeModeTestToolkit(t)
	kit.SetToolSearchEnabled(true)
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changed := ""
	kit.SetOnSessionWorkspaceChanged(func(root string) error { changed = root; return nil })
	args, _ := json.Marshal(map[string]any{"root": target})
	if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "set_session_workspace", Arguments: string(args)}); err != nil {
		t.Fatal(err)
	}
	if changed != target || kit.RootDir() != target {
		t.Fatalf("workspace control did not execute: %q", changed)
	}
}
