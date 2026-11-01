package personalim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Protocol cases: ACK must follow durable acceptance; retries preserve event
// identity; bot echoes and edits never become owner input; cancellation closes
// the socket; ambiguous writes must never masquerade as retryable failures.
func TestSlackSocketAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	accepted := make(chan Incoming, 1)
	release := make(chan struct{})
	acked := make(chan string, 1)
	var url string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps.connections.open" {
			if r.Header.Get("Authorization") != "Bearer app-secret" {
				t.Error("missing app auth")
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": strings.Replace(url, "http", "ws", 1) + "/socket"})
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		event := map[string]any{"envelope_id": "env", "type": "events_api", "payload": map[string]any{"event_id": "Ev1", "team_id": "T1", "event": map[string]any{"type": "message", "user": "U1", "channel": "D1", "ts": "1.2", "text": "continue"}}}
		if err = wsjson.Write(ctx, c, event); err != nil {
			return
		}
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if wsjson.Read(ctx, c, &ack) == nil {
			acked <- ack.EnvelopeID
		}
		<-ctx.Done()
	}))
	defer server.Close()
	url = server.URL
	client := Slack{AppToken: "app-secret", BotToken: "bot-secret", APIURL: url, HTTPClient: server.Client()}
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx, func(_ context.Context, m Incoming) error { accepted <- m; <-release; return nil })
	}()
	select {
	case m := <-accepted:
		if m.EventID != "Ev1" || m.TenantID != "T1" || m.ThreadID != "1.2" || m.SenderID != "U1" {
			t.Fatalf("bad event: %+v", m)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-acked:
		t.Fatal("acknowledged before durable acceptance")
	default:
	}
	close(release)
	select {
	case id := <-acked:
		if id != "env" {
			t.Fatal(id)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("socket did not stop")
	}
}

func TestSlackPostReceiptAndUncertainOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		uncertain  bool
		code       string
	}{
		{"success", `{"ok":true,"channel":"D1","ts":"2.3"}`, 200, false, ""},
		{"rate limit", `{"ok":false,"error":"ratelimited"}`, 429, false, "ratelimited"},
		{"lost receipt", `{"ok":true}`, 200, true, "invalid_receipt"},
		{"server failure", `failure`, 502, true, "http_error"},
		{"partial effect", `{"ok":false,"error":"internal_error"}`, 200, true, "internal_error"},
		{"gone", `{"ok":false,"error":"channel_not_found"}`, 200, false, "channel_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/chat.postMessage" || r.Header.Get("Authorization") != "Bearer bot-secret" {
					t.Error("incorrect write transport")
				}
				var payload map[string]any
				json.NewDecoder(r.Body).Decode(&payload)
				if payload["client_msg_id"] != "stable-id" || payload["thread_ts"] != "1.2" || payload["text"] != "answer" {
					t.Errorf("bad post: %+v", payload)
				}
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := Slack{BotToken: "bot-secret", APIURL: server.URL, HTTPClient: server.Client()}
			receipt, err := client.Post(context.Background(), Outgoing{ConversationID: "D1", ThreadID: "1.2", Text: "answer", ClientMessageID: "stable-id"})
			if tc.code == "" {
				if err != nil || receipt.MessageID != "2.3" || receipt.ConversationID != "D1" {
					t.Fatalf("%+v %v", receipt, err)
				}
				return
			}
			var fault *SlackError
			if !errors.As(err, &fault) || fault.Code != tc.code || fault.Uncertain != tc.uncertain {
				t.Fatalf("unexpected error: %#v", err)
			}
			if tc.status == 429 && fault.RetryAfter != 2*time.Second {
				t.Fatal("missing retry-after")
			}
		})
	}
}

func TestSlackEnvelopeFilters(t *testing.T) {
	for _, event := range []string{
		`{"type":"message","user":"U1","bot_id":"B1","channel":"D1","ts":"1","text":"echo"}`,
		`{"type":"message","subtype":"message_changed","channel":"D1","ts":"1"}`,
		`{"type":"reaction_added","user":"U1"}`,
	} {
		m, ok, err := slackIncoming([]byte(`{"type":"events_api","envelope_id":"e","payload":{"team_id":"T1","event_id":"Ev1","event":` + event + `}}`))
		if err != nil || ok {
			t.Fatalf("unexpected input %+v %t %v", m, ok, err)
		}
	}
}

func TestSlackFilesAndIdentity(t *testing.T) {
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" && r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer")
		}
		switch r.URL.Path {
		case "/auth.test":
			w.Write([]byte(`{"ok":true,"team_id":"T1","user_id":"Ubot","bot_id":"B1"}`))
		case "/conversations.info":
			w.Write([]byte(`{"ok":true,"channel":{"id":"D1","is_im":true}}`))
		case "/files.getUploadURLExternal":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "upload_url": base + "/upload", "file_id": "F1"})
		case "/upload":
			if r.Header.Get("Authorization") != "" {
				t.Error("credential sent to upload URL")
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "content" {
				t.Error("incorrect file bytes")
			}
			w.Write([]byte("OK"))
		case "/files.completeUploadExternal":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["channel_id"] != "D1" || body["thread_ts"] != "1.2" {
				t.Errorf("incorrect share destination: %+v", body)
			}
			w.Write([]byte(`{"ok":true,"files":[{"id":"F1"}]}`))
		case "/download":
			w.Write([]byte("content"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	s := Slack{BotToken: "secret", APIURL: base, HTTPClient: server.Client()}
	identity, err := s.Identity(context.Background())
	if err != nil || identity.TeamID != "T1" || identity.BotID != "B1" {
		t.Fatalf("identity %+v %v", identity, err)
	}
	if err = s.ValidateConversation(context.Background(), "D1"); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Upload(context.Background(), Outgoing{ConversationID: "D1", ThreadID: "1.2"}, "test.txt", []byte("content"))
	if err != nil || receipt.AttachmentID != "F1" {
		t.Fatalf("upload %+v %v", receipt, err)
	}
	body, err := s.Download(context.Background(), Attachment{ID: "F1", URL: base + "/download", Size: 7})
	if err != nil || string(body) != "content" {
		t.Fatalf("download %q %v", body, err)
	}
	_, err = s.Download(context.Background(), Attachment{ID: "F1", URL: "https://example.com/file"})
	if err == nil {
		t.Fatal("sent credentials outside Slack")
	}
}

func TestSlackFileMessage(t *testing.T) {
	raw := []byte(`{"type":"events_api","payload":{"api_app_id":"A1","team_id":"T1","event_id":"Ev1","event":{"type":"message","subtype":"file_share","user":"U1","channel":"D1","ts":"1.2","files":[{"id":"F1","name":"report.pdf","mimetype":"application/pdf","size":7,"url_private_download":"https://files.slack.com/file"}]}}}`)
	message, ok, err := slackIncoming(raw)
	if err != nil || !ok || len(message.Attachments) != 1 || message.Attachments[0].ID != "F1" || message.AppID != "A1" {
		t.Fatalf("file message %+v %t %v", message, ok, err)
	}
}

func TestSlackReconcileOriginalThread(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth.test" {
			w.Write([]byte(`{"ok":true,"team_id":"T1","user_id":"UB1","bot_id":"B1"}`))
			return
		}
		calls++
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var query map[string]any
		json.NewDecoder(r.Body).Decode(&query)
		if query["channel"] != "D1" || query["ts"] != "1.2" {
			t.Error("reconciliation widened destination")
		}
		if calls == 1 {
			w.Write([]byte(`{"ok":true,"messages":[{"ts":"2","client_msg_id":"different"}],"response_metadata":{"next_cursor":"next"}}`))
			return
		}
		if query["cursor"] != "next" {
			t.Error("lost pagination")
		}
		w.Write([]byte(`{"ok":true,"messages":[{"ts":"3","client_msg_id":"stable","bot_id":"B1","text":"answer"}]}`))
	}))
	defer server.Close()
	s := Slack{BotToken: "secret", APIURL: server.URL, HTTPClient: server.Client()}
	receipt, found, err := s.FindReceipt(context.Background(), Outgoing{ConversationID: "D1", ThreadID: "1.2", ClientMessageID: "stable", Text: "answer"})
	if err != nil || !found || receipt.MessageID != "3" || calls != 2 {
		t.Fatalf("%+v %t %v", receipt, found, err)
	}
}

func TestSlackFailedAdmissionIsNotAcknowledgedAndRetryKeepsIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observations := make(chan string, 2)
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps.connections.open" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": strings.Replace(base, "http", "ws", 1) + "/socket"})
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		raw := json.RawMessage(`{"envelope_id":"envelope","type":"events_api","payload":{"team_id":"T1","event_id":"same","event":{"type":"message","user":"U1","channel":"D1","ts":"1.2","text":"hello"}}}`)
		if wsjson.Write(ctx, c, raw) != nil {
			return
		}
		var ack map[string]string
		if wsjson.Read(ctx, c, &ack) == nil {
			observations <- "ack"
		} else {
			observations <- "closed"
		}
	}))
	defer server.Close()
	base = server.URL
	s := Slack{AppToken: "app", APIURL: base, HTTPClient: server.Client()}
	var first Incoming
	err := s.receive(ctx, func(_ context.Context, m Incoming) error { first = m; return errors.New("disk unavailable") })
	var fault *SlackError
	if !errors.As(err, &fault) || fault.Code != "inbox_unavailable" {
		t.Fatalf("%v", err)
	}
	select {
	case state := <-observations:
		if state != "closed" {
			t.Fatal("ACK despite failed storage")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondCtx, secondCancel := context.WithCancel(ctx)
	defer secondCancel()
	done := make(chan error, 1)
	go func() {
		done <- s.receive(secondCtx, func(_ context.Context, m Incoming) error {
			if m.EventID != first.EventID || m.MessageID != first.MessageID {
				t.Error("retry identity changed")
			}
			return nil
		})
	}()
	select {
	case state := <-observations:
		if state != "ack" {
			t.Fatal("missing retry ACK")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondCancel()
	<-done
}
