package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

func TestResponsesWebSocketContinuationUsesFinalMessage(t *testing.T) {
	answer := `{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Final answer"}]}`
	for _, tc := range []struct {
		name         string
		events       []string
		continuation bool
		imageOnly    bool
	}{
		{
			name:         "image_only_continuation",
			events:       []string{`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"ig_1","type":"image_generation_call","status":"completed","result":"aW1hZ2U="}]}}`},
			continuation: true,
			imageOnly:    true,
		},
		{
			name: "terminal_snapshot_only",
			events: []string{
				`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[` + answer + `]}}`,
			},
			continuation: true,
		},
		{
			name: "terminal_snapshot_corrects_done",
			events: []string{
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","phase":"final_answer","content":[{"type":"output_text","text":"Draft"}]}}`,
				`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[` + answer + `]}}`,
			},
			continuation: true,
		},
		{
			name: "metadata_only_snapshot_requires_full_replay",
			events: []string{
				`{"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","delta":"Final answer"}`,
				`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","phase":"final_answer"}]}}`,
			},
		},
		{
			name: "deltas_without_snapshots_require_full_replay",
			events: []string{
				`{"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","delta":"Final answer"}`,
				`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				readWSRequest(t, ctx, conn, requests)
				for _, event := range tc.events {
					writeWSEvent(t, ctx, conn, event)
				}
				readWSRequest(t, ctx, conn, requests)
				writeWSEvent(t, ctx, conn, `{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[]}}`)
			}))
			defer server.Close()
			store := false
			client, err := New(ClientConfig{
				BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesStore: &store,
				ResponsesTransport: providers.StreamTransportAuto, ResponsesWebSocketCache: NewResponsesWebSocketCache(),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runner := agent.StreamRunner{Client: client, Model: "gpt-test", PromptCacheKey: t.Name()}
			history := []providers.ChatMessage{{Role: "user", Content: "first"}}
			first, err := runner.RunWithCallback(ctx, history, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.imageOnly {
				if len(first.NewMessages) != 1 || len(first.NewMessages[0].Images) != 1 {
					t.Fatal("image reply missing")
				}
			} else if first.Content != "Final answer" {
				t.Fatalf("snapshot not recovered: %q", first.Content)
			}
			history = append(history, first.NewMessages...)
			history = append(history, providers.ChatMessage{Role: "user", Content: "second"})
			if _, err := runner.RunWithCallback(ctx, history, nil); err != nil {
				t.Fatal(err)
			}
			<-requests
			second := <-requests
			input := second["input"].([]any)
			if tc.continuation {
				if second["previous_response_id"] != "resp_1" || len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
					t.Fatalf("continuation must send only the new user input: %#v", second)
				}
			} else if second["previous_response_id"] != nil || len(input) != 3 || input[1].(map[string]any)["id"] != "msg_1" {
				t.Fatalf("incomplete baseline must replay full history without previous_response_id: %#v", second)
			}
		})
	}
}
