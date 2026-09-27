package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

func TestResponsesStreamFinalMessage(t *testing.T) {
	message := func(id, phase, text string) string {
		data, err := json.Marshal(map[string]any{
			"id": id, "type": "message", "role": "assistant", "phase": phase, "status": "completed",
			"content": []map[string]string{{"type": "output_text", "text": text}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	done := func(index int, item string) string {
		return fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item)
	}
	completed := func(items ...string) string {
		return `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[` + strings.Join(items, ",") + `]}}`
	}
	image := `{"id":"ig_1","type":"image_generation_call","status":"completed","output_format":"png","result":"aW1hZ2U="}`
	answer := message("msg_1", "final_answer", "Hello world")
	added := `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","phase":"final_answer"}}`
	delta := `{"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","delta":"Hello"}`
	for _, tc := range []struct {
		name      string
		events    []string
		want      string
		phase     providers.MessagePhase
		itemID    string
		images    []providers.InputImage
		wantError bool
	}{
		{
			name: "done_without_added_or_deltas", events: []string{done(0, answer), completed()},
			want: "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "missing_deltas", events: []string{added, done(0, answer), completed()},
			want: "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "missing_suffix", events: []string{added, delta, done(0, answer), completed()},
			want: "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "snapshots_do_not_duplicate_deltas",
			events: []string{added, delta, `{"type":"response.output_text.delta","delta":" world"}`, done(0, answer), done(0, answer), completed(answer)},
			want:   "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "correction_preserves_earlier_message",
			events: []string{
				done(0, message("msg_comment", "commentary", "Checking.\n")),
				`{"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","delta":"Incorrect draft"}`,
				done(1, answer), completed(),
			},
			want: "Checking.\nHello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "distinct_messages_with_identical_text",
			events: []string{done(0, message("msg_comment", "commentary", "Hello world")), done(1, answer), completed()},
			want:   "Hello worldHello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "index_identifies_delta_before_item_id",
			events: []string{`{"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`, done(0, answer), completed(answer)},
			want:   "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "interleaved_items_follow_output_order",
			events: []string{
				`{"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","delta":"Hello"}`,
				`{"type":"response.output_text.delta","output_index":0,"item_id":"msg_comment","delta":"Checking."}`,
				done(1, answer), done(0, message("msg_comment", "commentary", "Checking.\n")), completed(),
			},
			want: "Checking.\nHello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "content_parts_do_not_introduce_separators",
			events: []string{added, delta, done(0, `{"id":"msg_1","type":"message","phase":"final_answer","content":[{"type":"output_text","text":"Hello"},{"type":"output_text","text":" world"}]}`), completed()},
			want:   "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "terminal_snapshot_only", events: []string{completed(answer)},
			want: "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "metadata_only_done_keeps_deltas",
			events: []string{added, delta, done(0, `{"id":"msg_1","type":"message","phase":"final_answer","status":"completed"}`), completed()},
			want:   "Hello", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "refusal_snapshot",
			events: []string{done(0, `{"id":"msg_1","type":"message","phase":"final_answer","status":"completed","content":[{"type":"refusal","refusal":"Cannot fulfill this request."}]}`), completed()},
			want:   "Cannot fulfill this request.", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name: "streamed_refusal_is_not_duplicated",
			events: []string{added, `{"type":"response.refusal.delta","item_id":"msg_1","output_index":0,"delta":"Cannot fulfill this request."}`,
				done(0, `{"id":"msg_1","type":"message","phase":"final_answer","content":[{"type":"refusal","refusal":"Cannot fulfill this request."}]}`), completed()},
			want: "Cannot fulfill this request.", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1",
		},
		{
			name:   "explicit_empty_snapshot_clears_draft",
			events: []string{added, delta, done(0, `{"id":"msg_1","type":"message","phase":"final_answer","content":[]}`), completed()},
		},
		{name: "native_empty_completion", events: []string{completed()}},
		{name: "malformed_image", events: []string{completed(`{"id":"ig_1","type":"image_generation_call","status":"completed","result":"not base64"}`)}, wantError: true},
		{name: "missing_image_result", events: []string{completed(`{"id":"ig_1","type":"image_generation_call","status":"failed"}`)}, wantError: true},
		{name: "image_only", events: []string{done(0, image), completed(image)}, images: []providers.InputImage{{ProviderItemID: "ig_1", MediaType: "image/png", Data: "aW1hZ2U="}}},
		{name: "image_in_final_snapshot", events: []string{completed(image)}, images: []providers.InputImage{{ProviderItemID: "ig_1", MediaType: "image/png", Data: "aW1hZ2U="}}},
		{name: "image_and_text_deduplicated", events: []string{done(0, image), done(0, image), done(1, answer), completed(image, answer)}, want: "Hello world", phase: providers.MessagePhaseFinalAnswer, itemID: "msg_1", images: []providers.InputImage{{ProviderItemID: "ig_1", MediaType: "image/png", Data: "aW1hZ2U="}}},
	} {
		for _, transport := range []providers.StreamTransportMode{providers.StreamTransportSSE, providers.StreamTransportWebSocket} {
			t.Run(tc.name+"/"+string(transport), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if transport == providers.StreamTransportSSE {
						w.Header().Set("Content-Type", "text/event-stream")
						for _, event := range tc.events {
							fmt.Fprintf(w, "data: %s\n\n", event)
						}
						return
					}
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					if _, _, err := conn.Read(ctx); err != nil {
						t.Error(err)
						return
					}
					for _, event := range tc.events {
						writeWSEvent(t, ctx, conn, event)
					}
				}))
				defer server.Close()
				store := false
				client, err := New(ClientConfig{
					BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesStore: &store,
					ResponsesTransport: transport, ResponsesWebSocketCache: NewResponsesWebSocketCache(),
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				runner := agent.StreamRunner{Client: client, Model: "gpt-test", PromptCacheKey: t.Name()}
				var visible strings.Builder
				result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "hello"}}, func(event providers.StreamEvent) {
					switch event.Type {
					case providers.EventContentDelta:
						visible.WriteString(event.Content)
					case providers.EventContentReplace:
						visible.Reset()
						visible.WriteString(event.Content)
					}
				})
				if tc.wantError {
					if err == nil {
						t.Fatal("invalid image was silently accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if result.Content != tc.want || visible.String() != tc.want {
					t.Fatalf("content: result=%q visible=%q want=%q", result.Content, visible.String(), tc.want)
				}
				if requests.Load() != 1 || result.FinishReason != providers.FinishReasonStop {
					t.Fatalf("unexpected continuation: requests=%d finish=%q", requests.Load(), result.FinishReason)
				}
				if tc.want != "" || len(tc.images) > 0 {
					if len(result.NewMessages) != 1 {
						t.Fatalf("final reply not persisted: %+v", result.NewMessages)
					}
					msg := result.NewMessages[0]
					if msg.Content != tc.want || msg.Phase != tc.phase || msg.ProviderItemID != tc.itemID || !reflect.DeepEqual(msg.Images, tc.images) {
						t.Fatalf("persisted message: %+v", msg)
					}
				} else if len(result.NewMessages) != 0 {
					t.Fatalf("empty final message retained stale content: %+v", result.NewMessages)
				}
			})
		}
	}
}
