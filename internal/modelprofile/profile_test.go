package modelprofile

import "testing"

func TestResolveClassifiesModelFamilies(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     Family
	}{
		{provider: "anthropic", model: "claude-sonnet-4-5", want: FamilyClaude},
		{provider: "openai", model: "gpt-5-codex", want: FamilyCodex},
		{provider: "openai", model: "gpt-5.5", want: FamilyGPT},
		{provider: "google", model: "gemini-2.5-pro", want: FamilyGemini},
		{provider: "moonshot", model: "kimi-k2", want: FamilyKimi},
		{provider: "deepseek", model: "deepseek-v3.2", want: FamilyDeepSeek},
		{provider: "dashscope", model: "qwen3-coder-plus", want: FamilyQwen},
		{provider: "ollama", model: "llama-coder", want: FamilyLocal},
		{provider: "custom", model: "local-model", want: FamilyPortable},
	}
	for _, tt := range tests {
		if got := Resolve(tt.provider, tt.model).Family; got != tt.want {
			t.Fatalf("Resolve(%q, %q).Family = %s, want %s", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestResolveOpenAIGPTProfilesUsePatchMode(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-4.1-mini", "openai/gpt-oss-120b"} {
		profile := Resolve("openai", model)
		if profile.Execution.DefaultWriteMode != WriteModePatch {
			t.Fatalf("%s DefaultWriteMode = %s, want %s", model, profile.Execution.DefaultWriteMode, WriteModePatch)
		}
	}
}
