package openai

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/providers"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatSendsVideoURLWithoutPDFWrappingOrRoutingOptions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["video_input"]; ok {
			t.Error("local routing option leaked")
		}
		var messages []struct {
			Content []struct {
				Type     string        `json:"type"`
				VideoURL *chatImageURL `json:"video_url"`
				File     *chatFilePart `json:"file"`
			}
		}
		if err := json.Unmarshal(body["messages"], &messages); err != nil {
			t.Error(err)
		}
		if len(messages) != 1 || len(messages[0].Content) != 1 {
			t.Errorf("unexpected messages %s", body["messages"])
		} else {
			part := messages[0].Content[0]
			if part.Type != "video_url" || part.VideoURL == nil || part.VideoURL.URL != "data:video/mp4;base64,AAAA" || part.File != nil {
				t.Errorf("wrong video encoding: %+v", part)
			}
		}
		if string(body["stream"]) == "true" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Video received\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Video received"},"finish_reason":"stop"}]}`))
		}
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Chat(context.Background(), providers.ChatRequest{Model: "custom-video", ProviderOptions: map[string]any{"video_input": "video_url"}, MediaInput: providers.MediaInputPolicy{FileKnown: true}, Messages: []providers.ChatMessage{{Role: "user", Files: []providers.InputFile{{MediaType: "video/mp4", Data: "AAAA"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.StreamChat(context.Background(), providers.ChatRequest{Model: "custom-video", ProviderOptions: map[string]any{"video_input": "video_url"}, MediaInput: providers.MediaInputPolicy{FileKnown: true}, Messages: []providers.ChatMessage{{Role: "user", Files: []providers.InputFile{{MediaType: "video/mp4", Data: "AAAA"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Error != nil {
			t.Fatal(event.Error)
		}
	}
	if calls != 2 {
		t.Fatalf("requests=%d", calls)
	}
}

func TestResponsesRejectsVideoBeforeHTTP(t *testing.T) {
	client, _ := New(ClientConfig{BaseURL: "https://example.invalid", APIKey: "test", WireAPI: "responses"})
	_, err := client.buildResponsesRequest(providers.ChatRequest{Model: "custom", MediaInput: providers.MediaInputPolicy{Video: true, VideoKnown: true}, Messages: []providers.ChatMessage{{Role: "user", Files: []providers.InputFile{{MediaType: "video/mp4", Data: "AAAA"}}}}}, true)
	if err == nil {
		t.Fatal("Responses accepted unsupported video")
	}
}
