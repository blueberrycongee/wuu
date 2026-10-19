package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type routeTransport struct {
	server    *url.URL
	transport http.RoundTripper
	t         *testing.T
}

func (r routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.openai.com" || !strings.HasPrefix(req.URL.Path, "/v1/") {
		r.t.Errorf("unexpected destination %s", req.URL)
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	clone.URL.Scheme, clone.URL.Host = r.server.Scheme, r.server.Host
	return r.transport.RoundTrip(clone)
}
func mockClient(t *testing.T, handler http.HandlerFunc, token func(context.Context, string) (string, error)) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	client, err := New(ClientConfig{AccountID: "account-one", AccessToken: token, HTTPClient: &http.Client{Transport: routeTransport{u, server.Client().Transport, t}}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func request() providers.ChatRequest {
	return providers.ChatRequest{Provider: "chatgpt", Model: "account-model", Messages: []providers.ChatMessage{{Role: "user", Content: "Hello"}}}
}
func completed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}]}}\n\n")
}

// The wire contract must survive provider option overrides, a system-only
// history, and token renewal, without falling back to the legacy Codex route.
func TestAccountInferenceProtocol(t *testing.T) {
	var tokens, requests atomic.Int32
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if r.URL.Path != "/v1/responses" || r.Method != "POST" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer token-%d", n) {
			t.Errorf("stale bearer: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Error("SSE not requested")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["store"] != false || body["stream"] != true {
			t.Errorf("invalid state/stream: %v", body)
		}
		for _, key := range []string{"temperature", "max_output_tokens", "top_p", "background", "conversation", "metadata", "previous_response_id", "prompt_cache_retention", "user", "truncation", "max_tool_calls", "moderation", "multi_agent", "prompt", "safety_identifier", "top_logprobs"} {
			if _, ok := body[key]; ok {
				t.Errorf("unsupported field %s", key)
			}
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) == 0 {
			t.Errorf("missing full context: %v", body)
			return
		}
		for _, raw := range input {
			item, _ := raw.(map[string]any)
			if item["role"] == "system" {
				t.Error("system message sent")
			}
		}
		if n == 1 {
			tools, _ := body["tools"].([]any)
			if len(tools) != 1 {
				t.Errorf("tools = %v", tools)
			} else {
				ns, _ := tools[0].(map[string]any)
				nested, _ := ns["tools"].([]any)
				if ns["type"] != "namespace" || ns["name"] != "wuu" || len(nested) != 1 {
					t.Errorf("namespace = %v", ns)
				}
			}
			if len(input) != 4 {
				t.Errorf("history dropped: %v", input)
			}
			call, _ := input[1].(map[string]any)
			if call["namespace"] != "wuu" {
				t.Errorf("tool replay namespace = %v", call)
			}
		}
		completed(w)
	}, func(ctx context.Context, id string) (string, error) {
		if id != "account-one" {
			t.Errorf("account = %s", id)
		}
		return fmt.Sprintf("token-%d", tokens.Add(1)), nil
	})
	req := request()
	req.MaxTokens = 15
	req.Temperature = .5
	req.NativeDeferredToolDiscovery = true
	req.Tools = []providers.ToolDefinition{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}}
	req.Messages = []providers.ChatMessage{{Role: "system", Content: "Be helpful"}, {Role: "user", Content: "old"}, {Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{}`}}}, {Role: "tool", Name: "read_file", ToolCallID: "call-1", Content: "contents"}, {Role: "user", Content: "new"}}
	req.ProviderOptions = map[string]any{"store": true, "stream": false, "omitStore": true, "maxOutputTokens": 42, "temperature": 1, "topP": .5, "previous_response_id": "old-response"}
	for _, key := range []string{"background", "conversation", "metadata", "prompt_cache_retention", "user", "truncation", "max_tool_calls", "moderation", "multi_agent", "prompt", "safety_identifier", "top_logprobs"} {
		req.ProviderOptions[key] = "invalid"
	}
	response, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hello" {
		t.Errorf("response=%+v", response)
	}
	req = request()
	req.Messages = []providers.ChatMessage{{Role: "system", Content: "Summarize"}}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || tokens.Load() != 2 {
		t.Fatalf("requests=%d tokens=%d", requests.Load(), tokens.Load())
	}
}

func TestInferenceRequiresCompletedEvent(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"EOF", `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n"},
		{"done marker", "data: [DONE]\n\n"},
		{"legacy done", `data: {"type":"response.done","response":{"status":"completed","output":[]}}` + "\n\n"},
		{"incomplete", `data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}` + "\n\n"},
		{"failed after content", `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" + `data: {"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"Limit reached"}}}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.body)
			}, func(context.Context, string) (string, error) { return "token", nil })
			if _, err := client.Chat(context.Background(), request()); err == nil {
				t.Fatal("accepted stream without response.completed")
			}
		})
	}
}

func TestTokenFailureMakesNoHTTPRequest(t *testing.T) {
	sentinel := errors.New("sign in again")
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request sent without token") }, func(context.Context, string) (string, error) { return "", sentinel })
	_, err := client.Chat(context.Background(), request())
	if !errors.Is(err, sentinel) {
		t.Errorf("error=%v", err)
	}
}

func TestCatalogPreservesVisibilityOrderAndCapabilities(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Method != "GET" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"models":[{"slug":"z-model","display_name":"Zed","visibility":"list","context_window":12345},{"slug":"hidden","display_name":"Hidden","visibility":"hidden"},{"slug":"a-model","display_name":"Alpha","visibility":"list","supported_reasoning_levels":[{"effort":"medium"}]}]}`)
	}, func(context.Context, string) (string, error) { return "catalog-token", nil })
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Slug != "z-model" || models[1].Slug != "a-model" || models[0].DisplayName != "Zed" {
		t.Fatalf("models=%+v", models)
	}
	if !strings.Contains(string(models[0].Raw), `"context_window":12345`) {
		t.Errorf("capabilities dropped: %s", models[0].Raw)
	}
}

func TestCatalogRejectsUnrecognizedPayload(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"guessed-model"}]}`) }, func(context.Context, string) (string, error) { return "token", nil })
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("accepted non-account catalog")
	}
}

func TestInferenceRejectsHostedTools(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unsupported hosted tool sent") }, func(context.Context, string) (string, error) { return "token", nil })
	req := request()
	req.ProviderOptions = map[string]any{"tools": []any{map[string]any{"type": "tool_search"}}}
	if _, err := client.Chat(context.Background(), req); err == nil {
		t.Fatal("accepted unsupported hosted tool override")
	}
}

func TestAdmissionErrorsPreserveDiagnostics(t *testing.T) {
	for _, test := range []struct {
		status            int
		body, code, param string
	}{
		{401, `{"detail":"direct identity rejected"}`, "", ""},
		{403, `{"error":{"code":"subscription_sharing_route_not_supported","param":"route","message":"wrong route"}}`, "subscription_sharing_route_not_supported", "route"},
		{429, `{"error":{"code":"subscription_sharing_usage_limit_exceeded","param":"model","message":"app usage limit"}}`, "subscription_sharing_usage_limit_exceeded", "model"},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-request-id", "req-diagnostic")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}, func(context.Context, string) (string, error) { return "token", nil })
			_, err := client.Chat(context.Background(), request())
			var httpErr *providers.HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("error=%v", err)
			}
			if httpErr.Body != test.body || httpErr.ProviderCode != test.code || httpErr.ProviderParam != test.param || httpErr.RequestID != "req-diagnostic" {
				t.Errorf("error details=%+v", httpErr)
			}
		})
	}
}

func TestAccountCancellationStopsActiveStream(t *testing.T) {
	canceled := make(chan struct{})
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}, func(context.Context, string) (string, error) { return "token", nil })
	var disconnect context.CancelFunc
	client.bindRequest = func(ctx context.Context, id string) (context.Context, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		disconnect = cancel
		return ctx, cancel, nil
	}
	events, err := client.StreamChat(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	disconnect()
	for range events {
	}
	<-canceled
}

func TestRedirectDoesNotForwardBearer(t *testing.T) {
	var requests atomic.Int32
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "https://api.openai.com/v1/other")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}, func(context.Context, string) (string, error) { return "token", nil })
	if _, err := client.Chat(context.Background(), request()); err == nil {
		t.Fatal("redirect accepted")
	}
	if requests.Load() != 1 {
		t.Fatalf("redirect followed: %d requests", requests.Load())
	}
}

func TestToolCallRoundTripPreservesNamespaceAndResults(t *testing.T) {
	var requests atomic.Int32
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			fmt.Fprint(w, "data: "+`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","namespace":"wuu","name":"read_file","call_id":"call-1","arguments":""}}`+"\n\n"+"data: "+`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":\"README\"}"}`+"\n\n"+"data: "+`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","namespace":"wuu","name":"read_file","call_id":"call-1","arguments":"{\"path\":\"README\"}"}}`+"\n\n"+"data: "+`{"type":"response.completed","response":{"status":"completed","output":[]}}`+"\n\n")
			return
		}
		input := body["input"].([]any)
		var call, result bool
		for _, raw := range input {
			item := raw.(map[string]any)
			if item["type"] == "function_call" {
				call = item["name"] == "read_file" && item["namespace"] == "wuu" && item["arguments"] == `{"path":"README"}`
			}
			if item["type"] == "function_call_output" {
				result = item["call_id"] == "call-1" && item["output"] == "readme contents"
			}
		}
		if !call || !result {
			t.Errorf("incomplete tool roundtrip: %v", input)
		}
		completed(w)
	}, func(context.Context, string) (string, error) { return "token", nil })
	req := request()
	req.Tools = []providers.ToolDefinition{{Name: "read_file", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}
	first, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("calls=%+v", first.ToolCalls)
	}
	req.Messages = append(req.Messages, providers.ChatMessage{Role: "assistant", ToolCalls: first.ToolCalls, ProviderItems: first.ProviderItems}, providers.ChatMessage{Role: "tool", Name: "read_file", ToolCallID: first.ToolCalls[0].ID, Content: "readme contents"})
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsCannotReplaceAccountConversation(t *testing.T) {
	client := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("replacement history reached API") }, func(context.Context, string) (string, error) { return "token", nil })
	req := request()
	req.ProviderOptions = map[string]any{"input": []any{map[string]any{"role": "user", "content": "replaced"}}}
	if _, err := client.Chat(context.Background(), req); err == nil {
		t.Fatal("accepted replacement history")
	}
}
