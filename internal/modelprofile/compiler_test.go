package modelprofile

import (
	"slices"
	"sort"
	"testing"

	"github.com/blueberrycongee/wuu/internal/capability"
)

func TestResolveProfileKey(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     ProfileKey
	}{
		{provider: "openai", model: "gpt-5-codex", want: ProfileOpenAICodex},
		{provider: "openai", model: "gpt-5.5", want: ProfileOpenAIGPT},
		{provider: "openai", model: "gpt-4.1-mini", want: ProfileOpenAIGPT},
		{provider: "openai", model: "gpt-oss-120b", want: ProfileOpenAIGPT},
		{provider: "anthropic", model: "claude-sonnet-4-5", want: ProfileAnthropicClaude},
		{provider: "anthropic", model: "claude-opus-4-7", want: ProfileAnthropicClaude},
		{provider: "google", model: "gemini-2.5-pro", want: ProfileGeneric},
		{provider: "moonshot", model: "kimi-k2", want: ProfileGeneric},
		{provider: "deepseek", model: "deepseek-v3.2", want: ProfileGeneric},
		{provider: "dashscope", model: "qwen3-coder-plus", want: ProfileGeneric},
		{provider: "ollama", model: "llama-coder", want: ProfileGeneric},
		{provider: "custom", model: "local-model", want: ProfileGeneric},
	}
	for _, tt := range cases {
		got := ResolveProfileKey(Resolve(tt.provider, tt.model))
		if got != tt.want {
			t.Fatalf("ResolveProfileKey(%s, %s) = %s, want %s", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestNamedAgentSurfaceAddsChatToolsToCompleteMainSurface(t *testing.T) {
	c := DefaultCompiler{}
	for _, tt := range []struct {
		provider string
		model    string
	}{
		{provider: "openai", model: "gpt-5-codex"},
		{provider: "openai", model: "gpt-5.5"},
		{provider: "anthropic", model: "claude-sonnet-4-5"},
		{provider: "ollama", model: "llama-coder"},
	} {
		profile := Resolve(tt.provider, tt.model)
		main := c.Compile(profile, SurfaceMain)
		named := c.Compile(profile, SurfaceNamedAgent)
		chatTools := []string{"chat_check", "chat_memory", "chat_read", "chat_roster", "chat_send", "chat_task", "chat_verify", "chat_wake", "chat_work", "session", "work_get"}
		for name, capabilityName := range main.Tools {
			if named.Tools[name] != capabilityName {
				t.Errorf("%s/%s named-agent surface lost main tool %s", tt.provider, tt.model, name)
			}
		}
		for _, name := range chatTools {
			if named.Tools[name] != capability.CapabilityChat {
				t.Errorf("%s/%s named-agent surface must expose %s as chat capability", tt.provider, tt.model, name)
			}
		}
		wantTools := append(sortedKeys(main.Tools), chatTools...)
		slices.Sort(wantTools)
		if got := sortedKeys(named.Tools); !slices.Equal(got, wantTools) {
			t.Errorf("%s/%s named-agent tools = %v, want main + chat %v", tt.provider, tt.model, got, wantTools)
		}
		if !slices.Equal(sortedKeys(named.DeferredTools), sortedKeys(main.DeferredTools)) ||
			!slices.Equal(sortedKeys(named.HiddenTools), sortedKeys(main.HiddenTools)) {
			t.Errorf("%s/%s named-agent surface did not retain the main deferred/hidden tools", tt.provider, tt.model)
		}
	}
}

func TestOpenAICodexSurface(t *testing.T) {
	s := DefaultCompiler{}.Compile(Resolve("openai", "gpt-5-codex"), SurfaceMain)

	// Editing primitive is apply_patch. edit_file and write_file
	// must not be visible on this surface.
	if tool, ok := s.ToolForCapability(capability.CapabilityFileEdit); !ok || tool != "apply_patch" {
		t.Fatalf("Codex file.edit must map to apply_patch, got tool=%q ok=%v", tool, ok)
	}
	for _, hidden := range []string{"edit_file", "write_file"} {
		if _, visible := s.Tools[hidden]; visible {
			t.Fatalf("Codex surface must not advertise %s", hidden)
		}
	}

	// Bash-first: bash is visible.
	if _, ok := s.Tools["bash"]; !ok {
		t.Fatalf("Codex surface must include bash as a visible tool")
	}
	if !hasCapability(s.Capabilities, capability.CapabilityCommandBackground) {
		t.Fatalf("Codex surface must advertise command.background through bash, got caps=%v", s.Capabilities)
	}
	if !hasCapability(s.DeferredCapabilities, capability.CapabilityMCP) {
		t.Fatalf("Codex surface must defer mcp capability for tool_search-gated extensions, got caps=%v", s.DeferredCapabilities)
	}

	// The direct request prefix stays small: core file/search/edit/command,
	// planning, skill loading, and tool discovery.
	mustVisible := []string{
		"read_file", "list_files",
		"grep", "glob",
		"web_search", "web_fetch",
		"bash", "apply_patch",
		"load_skill", "tool_search",
		"wuu_browser",
	}
	for _, name := range mustVisible {
		if _, ok := s.Tools[name]; !ok {
			t.Fatalf("Codex surface must include %s, got tools=%v", name, sortedKeys(s.Tools))
		}
	}
	mustDeferred := []string{
		"thread_get",
	}
	for _, name := range mustDeferred {
		if _, ok := s.Tools[name]; ok {
			t.Fatalf("Codex surface must not directly advertise deferred tool %s, got tools=%v", name, sortedKeys(s.Tools))
		}
		if _, ok := s.DeferredTools[name]; !ok {
			t.Fatalf("Codex surface must defer %s, got deferred=%v", name, sortedKeys(s.DeferredTools))
		}
	}
}

func TestOpenAIGPTSurfaceUsesApplyPatchForAllOpenAIModels(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-4.1-mini", "openai/gpt-oss-120b"} {
		s := DefaultCompiler{}.Compile(Resolve("openai", model), SurfaceMain)
		tool, ok := s.ToolForCapability(capability.CapabilityFileEdit)
		if !ok {
			t.Fatalf("%s: expected file.edit capability to be visible", model)
		}
		if tool != "apply_patch" {
			t.Fatalf("%s: OpenAI GPT surface must use apply_patch, got %q", model, tool)
		}
		if _, hasEdit := s.Tools["edit_file"]; hasEdit {
			t.Fatalf("%s: OpenAI GPT surface must not advertise edit_file", model)
		}
		if _, hasWrite := s.Tools["write_file"]; hasWrite {
			t.Fatalf("%s: OpenAI GPT surface must not advertise write_file", model)
		}
	}
}

func TestAnthropicClaudeSurface(t *testing.T) {
	s := DefaultCompiler{}.Compile(Resolve("anthropic", "claude-sonnet-4-5"), SurfaceMain)

	// Editing primitive is edit_file (+ write_file as whole-file fallback).
	// apply_patch is hidden, never visible.
	tool, ok := s.ToolForCapability(capability.CapabilityFileEdit)
	if !ok || tool != "edit_file" {
		t.Fatalf("Claude file.edit must map to edit_file, got tool=%q ok=%v", tool, ok)
	}
	if _, has := s.Tools["write_file"]; !has {
		t.Fatalf("Claude surface must include write_file")
	}
	if _, has := s.Tools["apply_patch"]; has {
		t.Fatalf("Claude surface must not advertise apply_patch")
	}

	// Bash-first: bash is visible.
	if _, ok := s.Tools["bash"]; !ok {
		t.Fatalf("Claude surface must include bash")
	}
	if !hasCapability(s.Capabilities, capability.CapabilityCommandBackground) {
		t.Fatalf("Claude surface must advertise command.background through bash, got caps=%v", s.Capabilities)
	}
	if !hasCapability(s.DeferredCapabilities, capability.CapabilityMCP) {
		t.Fatalf("Claude surface must defer mcp capability for tool_search-gated extensions, got caps=%v", s.DeferredCapabilities)
	}
	// The same direct core capabilities as Codex, minus apply_patch.
	for _, name := range []string{
		"read_file", "list_files", "grep", "glob",
		"web_search", "web_fetch",
		"load_skill", "tool_search",
		"wuu_browser",
	} {
		if _, ok := s.Tools[name]; !ok {
			t.Fatalf("Claude surface must include %s, got tools=%v", name, sortedKeys(s.Tools))
		}
	}
}

func TestGenericSurfaceForOpenAISHapedBYOK(t *testing.T) {
	for _, tt := range []struct {
		provider string
		model    string
	}{
		{provider: "google", model: "gemini-2.5-pro"},
		{provider: "moonshot", model: "kimi-k2"},
		{provider: "deepseek", model: "deepseek-v3.2"},
		{provider: "dashscope", model: "qwen3-coder-plus"},
	} {
		s := DefaultCompiler{}.Compile(Resolve(tt.provider, tt.model), SurfaceMain)
		if s.ProfileName != string(ProfileGeneric) {
			t.Fatalf("%s/%s: ProfileName = %s, want generic", tt.provider, tt.model, s.ProfileName)
		}
		tool, ok := s.ToolForCapability(capability.CapabilityFileEdit)
		if !ok || tool != "edit_file" {
			t.Fatalf("%s/%s: generic file.edit must map to edit_file, got tool=%q ok=%v", tt.provider, tt.model, tool, ok)
		}
		if _, has := s.Tools["bash"]; !has {
			t.Fatalf("%s/%s: generic surface must include bash", tt.provider, tt.model)
		}
	}
}

func TestGenericSurfaceDropsBashForLocal(t *testing.T) {
	s := DefaultCompiler{}.Compile(Resolve("ollama", "llama-coder"), SurfaceMain)
	if s.ProfileName != string(ProfileGeneric) {
		t.Fatalf("local profile must compile under generic, got %s", s.ProfileName)
	}
	// The local profile should not expose command.bash as a VISIBLE
	// capability. HasCapability returns true for hidden capabilities
	// too, so we iterate s.Capabilities directly.
	if hasCapability(s.Capabilities, capability.CapabilityCommandBash) {
		t.Fatalf("local profile must not advertise command.bash, got caps=%v", s.Capabilities)
	}
	if _, has := s.Tools["bash"]; has {
		t.Fatalf("local profile must not include bash, got tools=%v", sortedKeys(s.Tools))
	}
	if _, has := s.Tools["edit_file"]; !has {
		t.Fatalf("local generic profile must include edit_file so prompt and write_file guidance remain usable, got tools=%v", sortedKeys(s.Tools))
	}
	if _, has := s.Tools["write_file"]; !has {
		t.Fatalf("local generic profile must include write_file, got tools=%v", sortedKeys(s.Tools))
	}
}

// ── helpers ───────────────────────────────────────────────────────

func hasCapability(caps []capability.Capability, want capability.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]capability.Capability) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
