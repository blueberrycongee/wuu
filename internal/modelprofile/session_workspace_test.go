package modelprofile

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/capability"
)

func TestSessionWorkspaceToolIsDeferredOnlyOnMainSurface(t *testing.T) {
	compiler := DefaultCompiler{}
	profile := Resolve("openai", "gpt-5-codex")
	main := compiler.Compile(profile, SurfaceMain)
	if got := main.DeferredTools["set_session_workspace"]; got != capability.CapabilitySessionWorkspace {
		t.Fatalf("main deferred capability = %q, want %q", got, capability.CapabilitySessionWorkspace)
	}
	if _, visible := main.Tools["set_session_workspace"]; visible {
		t.Fatal("set_session_workspace must load through tool_search")
	}

	for label, surface := range map[string]capability.Surface{
		"worker": compiler.Compile(profile, SurfaceWorker),
	} {
		if _, ok := surface.Tools["set_session_workspace"]; ok {
			t.Fatalf("%s surface exposes set_session_workspace", label)
		}
		if _, ok := surface.DeferredTools["set_session_workspace"]; ok {
			t.Fatalf("%s surface defers set_session_workspace", label)
		}
	}
}
