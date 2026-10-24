package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/titleservice"
)

type zeroTitlePolicy struct{ *titleservice.Service }

func (p *zeroTitlePolicy) InvokeService(_ context.Context, in pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	switch in.Method {
	case "plan":
		return json.RawMessage(`{"action":"infer","messages":[{"role":"user","content":"selected title input"}],"temperature":0}`), nil
	case "resolve":
		return json.RawMessage(`{"title":"selected title"}`), nil
	default:
		return nil, fmt.Errorf("unexpected method %s", in.Method)
	}
}

// Exercise the public model wire between title assembly and real HTTP encoding.
// ApplyRequest keeps the host execution/receipt authority from the base request.
type titleWireClient struct{ providers.StreamClient }

func (c titleWireClient) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	raw, err := json.Marshal(modelwire.RequestToWire(req))
	if err != nil {
		return nil, err
	}
	var wire pluginprotocol.LogicalModelRequestV1
	if err = json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	req, err = modelwire.ApplyRequest(req, wire)
	if err != nil {
		return nil, err
	}
	return c.StreamClient.StreamChat(ctx, req)
}

func TestSelectedTitleZeroTemperatureHTTP(t *testing.T) {
	for _, api := range []string{"chat", "responses", "anthropic"} {
		for _, supported := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/supported=%t", api, supported), func(t *testing.T) {
				requests := make(chan map[string]any, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					requests <- body
					w.Header().Set("Content-Type", "text/event-stream")
					switch api {
					case "chat":
						fmt.Fprint(w, "data: {\"id\":\"title\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"raw title\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					case "responses":
						fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"raw title\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"title\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
					case "anthropic":
						fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"title\",\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"raw title\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					}
				}))
				defer server.Close()
				var client providers.StreamClient
				var err error
				if api == "anthropic" {
					client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "synthetic"})
				} else {
					client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "synthetic", WireAPI: api})
				}
				if err != nil {
					t.Fatal(err)
				}
				rt := newTestRuntime(t, &fakeClient{})
				clients := []pluginhost.Client{&zeroTitlePolicy{titleservice.NewService()}, promptservice.NewService(), &embeddedCompactionFixture{compactionservice.NewService()}, &embeddedModelConsumer{}}
				registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
				rt.PluginHost = pluginhost.New(clients...)
				rt.PluginHost.AttachServiceRegistry(registry, conflicts)
				if err := rt.PluginHost.Activate(context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = rt.Cleanup() })
				binding := testRuntimeState(t, rt)
				if _, err := binding.Sessions.CreateWithMetadata("title-temperature", rt.RootDir); err != nil {
					t.Fatal(err)
				}
				options := map[string]any{"temperature": 0.75, "temperatureSupported": supported}
				result, err := (&Server{rt: rt}).generateThreadTitleUsingModel(binding, "title-temperature", []providers.ChatMessage{{Role: "user", Content: "task"}}, false, false, true, titleInferenceModel{client: titleWireClient{client}, name: "fixture-model", options: options})
				if err != nil {
					t.Fatalf("title inference: %v", err)
				}
				if result.CleanedTitle != "selected title" {
					t.Fatalf("selected result lost: %+v", result)
				}
				select {
				case body := <-requests:
					value, present := body["temperature"]
					if supported && (!present || value != float64(0)) {
						t.Errorf("selected zero replaced in HTTP payload: temperature=%v present=%t", value, present)
					}
					if !supported && present {
						t.Errorf("temperature capability bypassed in HTTP payload: %v", value)
					}
				default:
					t.Fatal("no HTTP request captured")
				}
				if options["temperature"] != 0.75 {
					t.Fatal("title request mutated configured provider options")
				}
			})
		}
	}
}
