package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestNativeModelReconstructsSelectedRoleConfiguration(t *testing.T) {
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", Model: "main-alias", BaseURL: "http://invalid.example", Models: map[string]config.ProviderModelConfig{
		"main-alias":   {ID: "upstream-main", Provider: &config.ProviderModelProviderConfig{API: "http://main.invalid/v1", NPM: "@ai-sdk/openai"}, Options: map[string]any{"stream_transport": "http"}},
		"title-alias":  {ID: "upstream-title", Provider: &config.ProviderModelProviderConfig{API: "http://title.invalid/v1", NPM: "@ai-sdk/anthropic"}},
		"worker-alias": {ID: "upstream-worker", Headers: map[string]string{"x-fixture": "worker"}},
	}}}, Agent: config.AgentConfig{ModelRoles: config.ModelRolesConfig{Title: config.ModelRoleConfig{Model: "title-alias"}, Worker: config.ModelRoleConfig{Model: "worker-alias"}}}}
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	native := &nativeModelProvider{config: cfg}
	for _, role := range []modelroles.Selection{roles.Main, roles.Title, roles.Worker} {
		got, _, err := native.effectiveProvider(providers.ChatRequest{Provider: role.Provider, Model: role.APIModel}, role.Model)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, role.RuleProviderConfig) {
			t.Fatalf("%s effective config differs from selected role", role.Role)
		}
	}
}

func TestNativeModelCandidateValidationDoesNotCacheOrSubmit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	credentials := credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) {
		return credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json")), nil
	})
	if err := credentials.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer credentials.Close(context.Background())
	native := &nativeModelProvider{state: pluginhost.StateActive, home: t.TempDir(), clients: map[string]providers.StreamClient{}}
	registry, conflicts := pluginhost.BuildServiceRegistry(native, credentials)
	if len(conflicts) > 0 {
		t.Fatal(conflicts)
	}
	registry.Activate()
	native.registry = registry
	defer native.Close(context.Background())
	candidate := config.ProviderConfig{Type: "openai-compatible", BaseURL: server.URL, Model: "fixture", APIKey: "synthetic-candidate"}
	raw, _ := json.Marshal(candidate)
	input, _ := json.Marshal(pluginprotocol.ModelAdapterInput{Operation: "validate", Configuration: raw, Request: pluginprotocol.LogicalModelRequestV1{Provider: "fixture"}})
	result, err := native.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Input: input})
	if err != nil {
		t.Fatal(err)
	}
	var output pluginprotocol.ModelAdapterOutput
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if !output.Supported || requests.Load() != 0 || len(native.clients) != 0 {
		t.Fatalf("validation submitted or retained candidate: supported=%v requests=%d cached=%d", output.Supported, requests.Load(), len(native.clients))
	}
}

func TestStateOnlyModelRouteUsesSelectedHostReceipts(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		stream         bool
	}{
		{"compatible_stream", "openai-compatible", true},
		{"grok_stream", "grok-build", true},
		{"grok_unary", "grok-build", false},
		{"codex_unary", "openai-codex", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body["stream"] != true {
					t.Errorf("selected %s provider sent non-streaming wire request for logical stream=%v", tc.provider, tc.stream)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.provider == "openai-codex" {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"state route\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":4}}}\n\n")
				} else {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"state route\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":4,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			modules := []string{"wuu.session-state", "wuu.state.jsonl", "kernel", "wuu.default-loop", "wuu.session-execution", "wuu.tool-execution", StateServicesConsumer, credentialstore.FileModule, "wuu.settings.local", config.Module, "wuu.permissions.default", ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default", "wuu.title.default", "wuu.agent.policy.default", nativeModelModule, modelExecutionConsumer, "wuu.model.capacity.default", "wuu.model.capacity.resources"}
			cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: tc.provider, BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "synthetic", StreamTransport: "sse"}}}
			state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, NativeProviders: []NativeProviderFactory{{ID: config.Module, Build: func(NativeProviderContext) (pluginhost.Client, error) {
				return config.NewMemoryService(config.Module, cfg), nil
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			client, err := state.ModelClient(cfg.Providers["fixture"], "fixture")
			if err != nil {
				t.Fatal(err)
			}
			req, err := providers.EnsureInferenceAttemptContext(context.Background(), providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			done := false
			if tc.stream {
				stream, err := client.StreamChat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				for event := range stream {
					if event.Error != nil {
						t.Fatal(event.Error)
					}
					if event.Type == providers.EventContentDelta {
						text += event.Content
					}
					if event.Type == providers.EventDone {
						done = true
					}
				}
			} else {
				response, err := client.Chat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				text, done = response.Content, response.FinishReason == providers.FinishReasonStop
				if response.Usage == nil || response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 4 {
					t.Fatalf("unary response lost usage: %+v", response)
				}
			}
			if text != "state route" || !done {
				t.Fatalf("state route lost response: %q done=%v", text, done)
			}
			submissions := req.Attempt.SubmissionSnapshot()
			if len(submissions) != 1 || submissions[0].Outcome != providers.InferenceSubmissionSucceeded || submissions[0].ReportedUsage == nil {
				t.Fatalf("state route lost physical receipt: %+v", submissions)
			}
		})
	}
}
