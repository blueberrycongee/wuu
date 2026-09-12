package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func configureNamedAgentCreationProvider(t *testing.T, rt *runtime.Session) {
	t.Helper()
	cfg := config.Default()
	cfg.DefaultProvider = "fake-provider"
	provider := config.ProviderConfig{Type: "openai-compatible", BaseURL: "https://provider.example.test/v1", APIKey: "test-key", Model: "fake-model", Models: map[string]config.ProviderModelConfig{
		"fake-model": {},
		"gpt-reasoner": {SupportedEfforts: []string{"low", "high"}, Variants: map[string]map[string]any{
			"low": {"reasoningEffort": "low"}, "high": {"reasoningEffort": "high"},
		}},
		"fixed-model":    {Variants: map[string]map[string]any{"high": {"reasoningEffort": "high"}}},
		"disabled-model": {Disabled: true},
	}}
	cfg.Providers = map[string]config.ProviderConfig{"fake-provider": provider, "openai": provider}
	withoutKey := provider
	withoutKey.APIKey = ""
	withoutKey.APIKeyEnv = "WUU_AGENT_CREATION_TEST_KEY"
	t.Setenv(withoutKey.APIKeyEnv, "")
	cfg.Providers["missing-credentials"] = withoutKey
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestChannelAgentCreateValidatesExplicitBYOKSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	configureNamedAgentCreationProvider(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	defer server.Close()
	for _, tc := range []struct {
		name, provider, model, effort, engine, wantError string
	}{
		{name: "missing selection", wantError: "choose a configured provider"},
		{name: "missing provider", model: "fake-model", wantError: "choose a configured provider"},
		{name: "unconfigured provider", provider: "absent", model: "fake-model", wantError: "not found"},
		{name: "credentials", provider: "missing-credentials", model: "fake-model", wantError: "needs credentials"},
		{name: "unknown model", provider: "fake-provider", model: "absent", wantError: "not configured or available"},
		{name: "disabled model", provider: "fake-provider", model: "disabled-model", wantError: "disabled"},
		{name: "missing effort", provider: "fake-provider", model: "gpt-reasoner", wantError: "choose a reasoning effort"},
		{name: "unsupported effort", provider: "fake-provider", model: "gpt-reasoner", effort: "ultra", wantError: "does not support"},
		{name: "unavailable engine", provider: "fake-provider", model: "fake-model", engine: "missing-engine", wantError: "unknown agent engine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, err := json.Marshal(ChannelAgentCreateParams{Name: "Analyst", ProviderOverride: tc.provider, ModelOverride: tc.model, EffortOverride: tc.effort, EngineOverride: tc.engine})
			if err != nil {
				t.Fatal(err)
			}
			if err := server.handleChannelAgentCreate(context.Background(), Request{ID: json.RawMessage(`1`), Params: params}); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			var envelope struct {
				Error *ResponseError `json:"error"`
			}
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || !strings.Contains(envelope.Error.Message, tc.wantError) {
				t.Fatalf("response error = %#v, want %q", envelope.Error, tc.wantError)
			}
		})
	}
	agents, err := server.channelService.ListNamedAgents(context.Background())
	if err != nil || len(agents) != 0 {
		t.Fatalf("rejected requests persisted identities: %#v, %v", agents, err)
	}
	var fixed ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{Name: "Fixed", ProviderOverride: "fake-provider", ModelOverride: "fixed-model"}, &fixed)
	if fixed.Agent.EffortOverride != "high" {
		t.Fatalf("fixed effort = %q, want high", fixed.Agent.EffortOverride)
	}
}

func TestChannelAgentCreateRetryPinsModelDefaultWithoutInteractiveEffort(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	rt.StreamRunner.Effort = "high"
	rt.StreamRunner.Variant = "ultra"
	configureNamedAgentCreationProvider(t, rt)
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	defer server.Close()
	params := ChannelAgentCreateParams{RequestID: "first-agent", Name: "Analyst", ProviderOverride: "fake-provider", ModelOverride: "fake-model"}
	var created, replayed ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, params, &created)
	callChannelRPC(t, server, out, MethodChannelAgentCreate, params, &replayed)
	if created.Agent.ID != replayed.Agent.ID || replayed.Agent.EffortOverride != "" {
		t.Fatalf("replayed agent = %#v, want original identity with model default", replayed.Agent)
	}
	selection, err := server.namedAgentPinnedSelection(agentRuntimeFromNamed(created.Agent), session.NewID(), "")
	if err != nil || selection.Provider != "fake-provider" || selection.Model != "fake-model" || selection.Effort != "" || selection.Variant != "" {
		t.Fatalf("initial selection = %#v, %v", selection, err)
	}
	room, err := server.channelService.OpenDirectMessage(context.Background(), localChannelHumanID, created.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	client, err := server.channelService.BindAgent(context.Background(), created.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent := agentRuntimeFromNamed(created.Agent)
	agent.Autostart = false
	if err := server.startNamedAgentConversationLocked(context.Background(), agent, client, room.ID, false); err != nil {
		t.Fatal(err)
	}
	ref := namedAgentRoomSessionID(agent, room.ID)
	binding, err := client.GetCollaborationSession(context.Background(), ref)
	if err != nil || binding.Effort != "" || binding.Model != "fake-model" {
		t.Fatalf("first DM binding = %#v, %v", binding, err)
	}
	// An identity edit after binding must not change the pinned default during
	// recovery, before the execution thread has persisted its own metadata.
	changed := agentRuntimeFromNamed(created.Agent)
	changed.EffortOverride = "high"
	selection, err = server.namedAgentPinnedSelection(changed, ref, ref)
	if err != nil || selection.Effort != "" || selection.Variant != "" {
		t.Fatalf("recovered binding selection = %#v, %v", selection, err)
	}
	thread, err := server.ensureAgentRuntimeSessionThreadLocked(changed, ref)
	if err != nil {
		t.Fatal(err)
	}
	if runner := thread.execRuntime.StreamRunner; runner.Effort != "" || runner.Variant != "" {
		t.Fatalf("DM execution inherited interactive reasoning: effort=%q variant=%q", runner.Effort, runner.Variant)
	}
}
