package config

import (
	"bytes"
	"strings"
	"testing"
)

// captureRetiredToolLoadingWarnings redirects the deprecation notice into buf
// and clears the process-level dedupe set so each test starts clean.
func captureRetiredToolLoadingWarnings(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	previous := retiredToolLoadingWarnWriter
	retiredToolLoadingWarnWriter = buf
	resetRetiredToolLoadingWarnings()
	return func() {
		retiredToolLoadingWarnWriter = previous
		resetRetiredToolLoadingWarnings()
	}
}

func TestRetiredToolLoadingValuesResolveToAuto(t *testing.T) {
	var warnings bytes.Buffer
	restore := captureRetiredToolLoadingWarnings(t, &warnings)
	defer restore()

	for _, raw := range []string{"wuu_tool_search", "tool_search", "WUU_TOOL_SEARCH", " wuu_tool_search "} {
		agent := AgentConfig{ToolLoading: ToolLoadingMode(raw)}
		if got := agent.ToolLoadingPreference(); got != ToolLoadingAuto {
			t.Fatalf("ToolLoadingPreference(%q) = %q, want auto", raw, got)
		}
	}
}

// An existing config naming the removed mode must keep starting. Failing
// validation would turn a retired tuning knob into a hard startup error.
func TestRetiredToolLoadingValueStillValidates(t *testing.T) {
	cfg := Config{
		DefaultProvider: "generic",
		Providers: map[string]ProviderConfig{
			"generic": {Type: "openai-compatible", BaseURL: "https://example.com/v1", APIKey: "abc", Model: "generic-coder"},
		},
		Agent: AgentConfig{ToolLoading: ToolLoadingMode("wuu_tool_search")},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a retired tool_loading value", err)
	}

	cfg.Agent.ToolLoading = ToolLoadingMode("nonsense")
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error for an unknown tool_loading value")
	}
}

func TestRetiredToolLoadingNoticeIsEmittedOncePerSpelling(t *testing.T) {
	var warnings bytes.Buffer
	restore := captureRetiredToolLoadingWarnings(t, &warnings)
	defer restore()

	agent := AgentConfig{ToolLoading: ToolLoadingMode("wuu_tool_search")}
	for range 3 {
		agent.ToolLoadingPreference()
	}
	if got := strings.Count(warnings.String(), "was removed"); got != 1 {
		t.Fatalf("notice count = %d, want 1 (the app-server resolves this on every session build):\n%s", got, warnings.String())
	}
}

func TestSupportedToolLoadingValuesAreSilent(t *testing.T) {
	var warnings bytes.Buffer
	restore := captureRetiredToolLoadingWarnings(t, &warnings)
	defer restore()

	for _, mode := range []ToolLoadingMode{ToolLoadingAuto, ToolLoadingFlat, ToolLoadingNative, ""} {
		agent := AgentConfig{ToolLoading: mode}
		agent.ToolLoadingPreference()
	}
	if warnings.Len() != 0 {
		t.Fatalf("supported values must not warn, got %q", warnings.String())
	}
}

// The legacy boolean no longer selects Wuu progressive loading: true means
// auto (native where the provider supports it, flat elsewhere).
func TestLegacyToolSearchBooleanMapsToAutoAndFlat(t *testing.T) {
	enabled, disabled := true, false
	if got := (AgentConfig{ToolSearch: &enabled}).ToolLoadingPreference(); got != ToolLoadingAuto {
		t.Fatalf("tool_search=true resolved to %q, want auto", got)
	}
	if got := (AgentConfig{ToolSearch: &disabled}).ToolLoadingPreference(); got != ToolLoadingFlat {
		t.Fatalf("tool_search=false resolved to %q, want flat", got)
	}
	// tool_loading wins over the legacy alias when both are present.
	if got := (AgentConfig{ToolLoading: ToolLoadingFlat, ToolSearch: &enabled}).ToolLoadingPreference(); got != ToolLoadingFlat {
		t.Fatalf("explicit tool_loading should win over tool_search, got %q", got)
	}
}
