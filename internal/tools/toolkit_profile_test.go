package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// containsProfileDef reports whether the given name appears in the
// visible tool definitions returned by Toolkit.Definitions().
func containsProfileDef(defs []providers.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// sortedProfileDefNames returns a stable, sorted slice of the
// visible tool names so failure messages are deterministic.
func sortedProfileDefNames(defs []providers.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

func TestSetActiveProfileCompilesAndExposesBashForAllStandardProfiles(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		provider string
		model    string
	}{
		{provider: "openai", model: "gpt-5-codex"},
		{provider: "openai", model: "gpt-5.5"},
		{provider: "anthropic", model: "claude-sonnet-4-5"},
		{provider: "google", model: "gemini-2.5-pro"},
	} {
		kit, err := New(root)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		kit.SetActiveProfile(modelprofile.Resolve(tt.provider, tt.model), true)
		surface := kit.ActiveSurface()
		if surface.ProfileName == "" {
			t.Fatalf("%s/%s: expected a compiled surface", tt.provider, tt.model)
		}
		defs := kit.Definitions()
		if !containsProfileDef(defs, "bash") {
			t.Fatalf("%s/%s: Definitions must include bash, got %v", tt.provider, tt.model, sortedProfileDefNames(defs))
		}
	}
}

func TestActiveProfileKeepsCoreLowFrequencyToolsDeferred(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	defs := kit.Definitions()
	for _, want := range []string{
		"read_file",
		"grep",
		"glob",
		"web_search",
		"web_fetch",
		"bash",
		"apply_patch",
		"tool_search",
		"load_skill",
	} {
		if !containsProfileDef(defs, want) {
			t.Fatalf("Codex profile should keep core tool %s visible, got %v", want, sortedProfileDefNames(defs))
		}
	}
	for _, name := range []string{
		"thread_get",
	} {
		if containsProfileDef(defs, name) {
			t.Fatalf("low-frequency tool %s should stay deferred, got %v", name, sortedProfileDefNames(defs))
		}
		info, ok := kit.ToolInfo(name)
		if !ok {
			t.Fatalf("ToolInfo(%q) not found", name)
		}
		if info.Exposure != ToolExposureDeferred {
			t.Fatalf("%s exposure = %s, want %s", name, info.Exposure, ToolExposureDeferred)
		}
	}
	// Agent-profile tools are not part of the ordinary main-agent surface, so
	// ToolInfo reports them Hidden, not Deferred.
	for _, name := range []string{
		"list_agent_profiles",
		"create_agent_profile",
	} {
		if containsProfileDef(defs, name) {
			t.Fatalf("agent-profile tool %s must not be visible on a main agent, got %v", name, sortedProfileDefNames(defs))
		}
		info, ok := kit.ToolInfo(name)
		if !ok {
			t.Fatalf("ToolInfo(%q) not found", name)
		}
		if info.Exposure != ToolExposureHidden {
			t.Fatalf("main-agent %s exposure = %s, want %s (named-only)", name, info.Exposure, ToolExposureHidden)
		}
	}
}

func TestActiveProfileToolSearchLoadsThreadGetForSessionIDs(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	if containsProfileDef(kit.Definitions(), "thread_get") {
		t.Fatal("thread_get should be deferred by default")
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "tool_search",
		Arguments: `{"query":"look up a thread id / session id conversation"}`,
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var loaded struct {
		LoadedTools []string `json:"loaded_tools"`
	}
	if err := json.Unmarshal([]byte(resp), &loaded); err != nil {
		t.Fatalf("parse tool_search: %v", err)
	}
	if !containsString(loaded.LoadedTools, "thread_get") {
		t.Fatalf("tool_search should load thread_get for session IDs: %s", resp)
	}
	if !containsProfileDef(kit.Definitions(), "thread_get") {
		t.Fatalf("loaded thread_get should be visible in definitions: %v", sortedProfileDefNames(kit.Definitions()))
	}
}

func TestActiveProfileAllowsDeferredMCPThroughToolSearch(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.registry = NewRegistry(
		NewToolSearchTool(kit),
		&stubTool{
			name:   "mcp_docs_search",
			def:    providers.ToolDefinition{Name: "mcp_docs_search", Description: "Search docs through MCP"},
			result: `{"action":"mcp_docs_search"}`,
		},
	)
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	if containsProfileDef(kit.Definitions(), "mcp_docs_search") {
		t.Fatal("MCP tool should not be visible before tool_search")
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "mcp_docs_search", Arguments: `{}`})
	if err == nil || !strings.Contains(err.Error(), "deferred") {
		t.Fatalf("unloaded MCP tool should ask for tool_search, got %v", err)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "tool_search",
		Arguments: `{"query":"docs search"}`,
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var parsed struct {
		LoadedTools []string `json:"loaded_tools"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse tool_search: %v", err)
	}
	if !containsString(parsed.LoadedTools, "mcp_docs_search") {
		t.Fatalf("tool_search did not load MCP tool: %s", resp)
	}
	defs := kit.Definitions()
	if !containsProfileDef(defs, "mcp_docs_search") {
		t.Fatalf("loaded MCP tool should be appended to active profile definitions: %v", sortedProfileDefNames(defs))
	}
	out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "mcp_docs_search", Arguments: `{}`})
	if err != nil {
		t.Fatalf("loaded MCP tool should execute: %v", err)
	}
	if !strings.Contains(out, "mcp_docs_search") {
		t.Fatalf("unexpected MCP result: %s", out)
	}
}

func TestLocalNoShellProfileFiltersTerminalMCPTools(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.registry = NewRegistry(
		NewToolSearchTool(kit),
		&stubTool{
			name:   "mcp_docs_search",
			def:    providers.ToolDefinition{Name: "mcp_docs_search", Description: "Search project documentation through MCP"},
			result: `{"action":"mcp_docs_search"}`,
			cap:    capability.CapabilitySearchSemantic,
		},
		&stubTool{
			name: "mcp_server_execute_command",
			def: providers.ToolDefinition{
				Name:        "mcp_server_execute_command",
				Description: "Run a command through MCP",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{
							"type":        "string",
							"description": "Command to run",
						},
					},
				},
			},
			result: `{"action":"mcp_server_execute_command"}`,
		},
	)
	kit.SetActiveProfile(modelprofile.Resolve("ollama", "llama-coder"), true)

	if containsProfileDef(kit.Definitions(), "mcp_server_execute_command") {
		t.Fatal("local/no-shell profile must not directly expose terminal MCP tools")
	}
	info, ok := kit.ToolInfo("mcp_server_execute_command")
	if !ok || info.Exposure != ToolExposureHidden {
		t.Fatalf("terminal MCP exposure = %+v, ok=%v; want hidden", info, ok)
	}

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "tool_search",
		Arguments: `{"query":"docs terminal shell npm git search"}`,
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var parsed struct {
		LoadedTools []string `json:"loaded_tools"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse tool_search: %v", err)
	}
	if containsString(parsed.LoadedTools, "mcp_server_execute_command") {
		t.Fatalf("tool_search exposed terminal MCP under no-shell profile: %s", resp)
	}
	if !containsString(parsed.LoadedTools, "mcp_docs_search") {
		t.Fatalf("non-command MCP should remain discoverable under no-shell profile: %s", resp)
	}

	kit.markDeferredToolsLoaded("mcp_server_execute_command")
	if containsProfileDef(kit.Definitions(), "mcp_server_execute_command") {
		t.Fatal("manual loading must not leak terminal MCP tools into no-shell definitions")
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "mcp_server_execute_command", Arguments: `{}`})
	if err == nil || !strings.Contains(err.Error(), "active model surface") {
		t.Fatalf("terminal MCP execution should be blocked by no-shell surface, got %v", err)
	}
}

func TestLocalNoShellProfileRequiresExplicitReadOnlyMCPCapability(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.registry = NewRegistry(
		NewToolSearchTool(kit),
		&stubTool{
			name:   "mcp_docs_search",
			def:    providers.ToolDefinition{Name: "mcp_docs_search", Description: "Search project documentation through MCP"},
			result: `{"action":"mcp_docs_search"}`,
		},
	)
	kit.SetActiveProfile(modelprofile.Resolve("ollama", "llama-coder"), true)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "tool_search",
		Arguments: `{"query":"docs search"}`,
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var parsed struct {
		LoadedTools []string `json:"loaded_tools"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse tool_search: %v", err)
	}
	if containsString(parsed.LoadedTools, "mcp_docs_search") {
		t.Fatalf("no-shell profile should fail closed for MCP without explicit capability: %s", resp)
	}
	kit.markDeferredToolsLoaded("mcp_docs_search")
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "mcp_docs_search", Arguments: `{}`})
	if err == nil || !strings.Contains(err.Error(), "active model surface") {
		t.Fatalf("unclassified MCP execution should be blocked by no-shell surface, got %v", err)
	}
}

func TestBashCapableProfileAllowsTerminalMCPTools(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.registry = NewRegistry(
		NewToolSearchTool(kit),
		&stubTool{
			name: "mcp_terminal_exec",
			def: providers.ToolDefinition{
				Name:        "mcp_terminal_exec",
				Description: "Run shell commands in a terminal through MCP",
			},
			result: `{"action":"mcp_terminal_exec"}`,
		},
	)
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "tool_search",
		Arguments: `{"query":"terminal shell"}`,
	})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var parsed struct {
		LoadedTools []string `json:"loaded_tools"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse tool_search: %v", err)
	}
	if !containsString(parsed.LoadedTools, "mcp_terminal_exec") {
		t.Fatalf("bash-capable profile should discover terminal MCP tools: %s", resp)
	}
	out, err := kit.Execute(context.Background(), providers.ToolCall{Name: "mcp_terminal_exec", Arguments: `{}`})
	if err != nil {
		t.Fatalf("loaded terminal MCP should execute under bash-capable profile: %v", err)
	}
	if !strings.Contains(out, "mcp_terminal_exec") {
		t.Fatalf("unexpected MCP result: %s", out)
	}
}

func TestSetActiveProfileLocalProfileDropsBash(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("ollama", "llama-coder"), true)
	defs := kit.Definitions()
	if containsProfileDef(defs, "bash") {
		t.Fatalf("local profile must not expose bash, got %v", sortedProfileDefNames(defs))
	}
}

func TestSetActiveProfileCodexExposesApplyPatchHidesEditAndWrite(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)
	defs := kit.Definitions()
	if !containsProfileDef(defs, "apply_patch") {
		t.Fatalf("Codex surface must include apply_patch, got %v", sortedProfileDefNames(defs))
	}
	for _, hidden := range []string{"edit_file", "write_file", "git"} {
		if containsProfileDef(defs, hidden) {
			t.Fatalf("Codex surface must not advertise %s, got %v", hidden, sortedProfileDefNames(defs))
		}
	}
}

func TestSetActiveProfileAlignsDefinitionWithExecutionState(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	if !containsProfileDef(kit.Definitions(), "apply_patch") {
		t.Fatal("Codex surface should advertise apply_patch")
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "apply_patch", Arguments: `{}`})
	if err == nil {
		t.Fatal("expected apply_patch to reject missing patchText")
	}
	if strings.Contains(err.Error(), "disabled") {
		t.Fatalf("advertised apply_patch must not be disabled: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{Name: "edit_file", Arguments: `{}`})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("hidden edit_file should be disabled under Codex surface, got %v", err)
	}
}

func TestActiveProfileDefinitionsRespectExplicitDisables(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)
	kit.DisableTools("bash")

	defs := kit.Definitions()
	if containsProfileDef(defs, "bash") {
		t.Fatalf("explicitly disabled bash leaked into active surface: %v", sortedProfileDefNames(defs))
	}
	if !containsProfileDef(defs, "apply_patch") {
		t.Fatalf("unrelated surface tool apply_patch should remain visible: %v", sortedProfileDefNames(defs))
	}
}

func TestSetActiveProfileClaudeExposesEditAndWriteHidesApplyPatch(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("anthropic", "claude-sonnet-4-5"), true)
	defs := kit.Definitions()
	for _, want := range []string{"bash", "edit_file", "write_file", "read_file", "grep", "glob"} {
		if !containsProfileDef(defs, want) {
			t.Fatalf("Claude surface must include %s, got %v", want, sortedProfileDefNames(defs))
		}
	}
	if containsProfileDef(defs, "apply_patch") {
		t.Fatalf("Claude surface must not advertise apply_patch, got %v", sortedProfileDefNames(defs))
	}
	for _, hidden := range []string{"git"} {
		if containsProfileDef(defs, hidden) {
			t.Fatalf("Claude surface must not advertise %s, got %v", hidden, sortedProfileDefNames(defs))
		}
	}
}

func TestSetActiveProfileOpenAIGPTUsesApplyPatch(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-4.1-mini"), true)
	defs := kit.Definitions()
	if !containsProfileDef(defs, "apply_patch") {
		t.Fatalf("OpenAI GPT profile must expose apply_patch, got %v", sortedProfileDefNames(defs))
	}
	for _, hidden := range []string{"edit_file", "write_file"} {
		if containsProfileDef(defs, hidden) {
			t.Fatalf("OpenAI GPT profile must not expose %s, got %v", hidden, sortedProfileDefNames(defs))
		}
	}
}

func TestSetActiveProfileZeroValueRestoresLegacySurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Codex first: the model sees only apply_patch, not edit_file.
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)
	if containsProfileDef(kit.Definitions(), "edit_file") {
		t.Fatal("expected Codex surface to hide edit_file")
	}
	// Clear the profile and verify the legacy direct-tool surface
	// returns with bash as the visible shell entry point.
	kit.SetActiveProfile(modelprofile.Profile{}, true)
	if kit.ActiveSurface().ProfileName != "" {
		t.Fatal("expected zero-value profile to clear the active surface")
	}
	defs := kit.Definitions()
	if !containsProfileDef(defs, "bash") {
		t.Fatalf("legacy surface must include bash, got %v", sortedProfileDefNames(defs))
	}
	if !containsProfileDef(defs, "edit_file") {
		t.Fatalf("legacy surface must include edit_file, got %v", sortedProfileDefNames(defs))
	}
}

func TestCloneForRootPreservesActiveProfileSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	clone, err := kit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("CloneForRoot: %v", err)
	}
	if clone.ActiveSurface().ProfileName != kit.ActiveSurface().ProfileName {
		t.Fatalf("clone surface = %q, want %q", clone.ActiveSurface().ProfileName, kit.ActiveSurface().ProfileName)
	}
	defs := clone.Definitions()
	if !containsProfileDef(defs, "apply_patch") || containsProfileDef(defs, "edit_file") {
		t.Fatalf("clone should keep Codex edit surface, got %v", sortedProfileDefNames(defs))
	}
	_, err = clone.Execute(context.Background(), providers.ToolCall{Name: "apply_patch", Arguments: `{}`})
	if err == nil {
		t.Fatal("expected apply_patch to reject missing patchText")
	}
	if strings.Contains(err.Error(), "disabled") {
		t.Fatalf("clone advertised apply_patch must not be disabled: %v", err)
	}
}

func TestActiveSurfaceReturnsCopy(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)

	surface := kit.ActiveSurface()
	delete(surface.Tools, "apply_patch")
	surface.Capabilities = nil

	if !containsProfileDef(kit.Definitions(), "apply_patch") {
		t.Fatal("mutating ActiveSurface result must not mutate toolkit surface")
	}
	if len(kit.ActiveSurface().Capabilities) == 0 {
		t.Fatal("mutating ActiveSurface capability slice must not mutate toolkit surface")
	}
}

func TestDefinitionsFilterStaysWithinSurfaceAndAllowsDeferredTools(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Runtime-backed tools are advertised only when their dependency is wired.
	kit.SetArtifactPublisher(func(context.Context, ArtifactPublishRequest) (toolresult.ContentPart, error) {
		return toolresult.ContentPart{}, nil
	})
	kit.SetActiveProfile(modelprofile.Resolve("anthropic", "claude-sonnet-4-5"), true)
	loadedDeferred := map[string]struct{}{
		"thread_get": {},
	}
	kit.markDeferredToolsLoaded("thread_get")
	surface := kit.ActiveSurface()
	defs := kit.Definitions()
	visible := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		visible[d.Name] = struct{}{}
	}
	for name := range visible {
		if _, ok := surface.Tools[name]; !ok {
			if _, deferred := surface.DeferredTools[name]; !deferred {
				t.Fatalf("Definitions returned %q but the active surface does not allow it", name)
			}
			if _, loaded := loadedDeferred[name]; !loaded {
				t.Fatalf("Definitions returned unloaded deferred tool %q", name)
			}
		}
	}
	for name := range surface.Tools {
		if _, ok := visible[name]; !ok {
			t.Fatalf("surface.Tools has direct %q but Definitions did not include it", name)
		}
	}
	for name := range surface.DeferredTools {
		_, isVisible := visible[name]
		_, loaded := loadedDeferred[name]
		if loaded && !isVisible {
			t.Fatalf("loaded deferred tool %q did not appear in Definitions", name)
		}
		if !loaded && isVisible {
			t.Fatalf("unloaded deferred tool %q leaked into Definitions", name)
		}
	}
}
