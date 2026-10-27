package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Reload tests retain one synthetic endpoint across all selected generations.
// Native transport and graph publication remain real; only the model is fake.
func newReloadBehaviorRuntime(t *testing.T, previous *runtime.Session, client *fakeClient) *runtime.Session {
	t.Helper()
	preparePluginRuntimePathsForTest(t, previous)
	endpoint := newTestOpenAIEndpoint(t, "fake-provider", "reload-fixture-title", client.Chat)
	cfg, _, err := config.LoadPath(previous.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PTC.Enabled = false
	cfg.DefaultProvider = "fake-provider"
	cfg.Providers = map[string]config.ProviderConfig{"fake-provider": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "fixture-only", Model: "fake-model", Models: map[string]config.ProviderModelConfig{"fake-model": {Options: map[string]any{"promptCacheKeySupported": true}}}}}
	cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: "fake-provider", Model: "reload-fixture-title"}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	home := previous.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	credentials := runtime.NativeProviderFactory{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return credentialstore.NewService(credentialstore.OSModule, func() (credentialstore.Store, error) { return nativeCredentialFixture(t, home), nil }), nil
	}}
	selected, err := runtime.NewSession(runtime.Options{RootDir: previous.RootDir, HomeDir: home, WuuHome: previous.WuuHome, Config: cfg, ConfigPath: previous.ConfigPath, ConfigLoadMode: runtime.ConfigLoadFile, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{credentials}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := selected.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	return selected
}

// Exercise selected native model adapters against deterministic HTTP rather than
// replacing mutable runner fields that captured graph generations do not read.
func newTestOpenAIEndpoint(t *testing.T, provider, titleModel string, chat func(context.Context, providers.ChatRequest) (providers.ChatResponse, error)) *httptest.Server {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model          string `json:"model"`
			Stream         bool   `json:"stream"`
			PromptCacheKey string `json:"prompt_cache_key"`
			Tools          []struct {
				Function struct {
					Name        string         `json:"name"`
					Description string         `json:"description"`
					Parameters  map[string]any `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role       string          `json:"role"`
				Name       string          `json:"name"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", 400)
			return
		}
		request := providers.ChatRequest{Provider: provider, Model: in.Model}
		if in.PromptCacheKey != "" {
			request.CacheHint = &providers.CacheHint{PromptCacheKey: in.PromptCacheKey}
		}
		for _, tool := range in.Tools {
			request.Tools = append(request.Tools, providers.ToolDefinition{Name: tool.Function.Name, Description: tool.Function.Description, InputSchema: tool.Function.Parameters})
		}
		for _, item := range in.Messages {
			message := providers.ChatMessage{Role: item.Role, Name: item.Name, ToolCallID: item.ToolCallID}
			if err := json.Unmarshal(item.Content, &message.Content); err != nil && len(item.Content) != 0 && string(item.Content) != "null" {
				var parts []struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(item.Content, &parts); err != nil {
					t.Error(err)
					http.Error(w, "invalid fixture content", 400)
					return
				}
				for _, part := range parts {
					message.Content += part.Text
				}
			}
			for _, call := range item.ToolCalls {
				message.ToolCalls = append(message.ToolCalls, providers.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
			}
			request.Messages = append(request.Messages, message)
		}
		response := providers.ChatResponse{Content: "Fixture title"}
		if in.Model != titleModel {
			var err error
			response, err = chat(r.Context(), request)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		message := map[string]any{"role": "assistant", "content": response.Content}
		finish := "stop"
		if len(response.ToolCalls) > 0 {
			finish = "tool_calls"
			calls := make([]map[string]any, 0, len(response.ToolCalls))
			for index, call := range response.ToolCalls {
				calls = append(calls, map[string]any{"index": index, "id": call.ID, "type": "function", "function": map[string]string{"name": call.Name, "arguments": call.Arguments}})
			}
			message["tool_calls"] = calls
		}
		if in.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": nil}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			raw, _ = json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
		}
	}))
	t.Cleanup(endpoint.Close)
	return endpoint
}
