package personalim

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These contracts keep untrusted message content separate from provider identity,
// and make failed durable admission visible to the provider before acknowledgement.
func TestFeishuDurableAdmission(t *testing.T) {
	f := &Feishu{AppID: "app", AppSecret: "secret", TenantID: "tenant"}
	failure := errors.New("storage unavailable")
	var got Incoming
	d := f.dispatcher(func(_ context.Context, m Incoming) error { got = m; return failure })
	payload := []byte(`{"schema":"2.0","header":{"event_id":"ev","event_type":"im.message.receive_v1","app_id":"app","tenant_key":"tenant"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_owner"},"tenant_key":"tenant"},"message":{"message_id":"om_msg","chat_id":"oc_chat","root_id":"om_root","message_type":"text","content":"{\"text\":\"continue\"}"}}}`)
	if _, err := d.Do(context.Background(), payload); !errors.Is(err, failure) {
		t.Fatalf("admission failure lost: %v", err)
	}
	if got.SenderID != "ou_owner" || got.ThreadID != "om_root" || got.EventID != "ev" || got.TenantID != "tenant" {
		t.Fatalf("bad provenance: %+v", got)
	}
	for _, change := range []struct {
		path  string
		value string
	}{{"app_id", "wrong"}, {"tenant_key", "wrong"}} {
		var body map[string]any
		json.Unmarshal(payload, &body)
		body["header"].(map[string]any)[change.path] = change.value
		altered, _ := json.Marshal(body)
		got = Incoming{}
		if _, err := d.Do(context.Background(), altered); err == nil || got.EventID != "" {
			t.Fatal("accepted foreign event")
		}
	}
	var body map[string]any
	json.Unmarshal(payload, &body)
	body["event"].(map[string]any)["sender"].(map[string]any)["sender_type"] = "bot"
	altered, _ := json.Marshal(body)
	got = Incoming{}
	if _, err := d.Do(context.Background(), altered); err != nil || got.EventID != "" {
		t.Fatal("bot echo became input")
	}
}

func TestFeishuSendReceiptAndIdempotency(t *testing.T) {
	for _, reply := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "reply"}[reply], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
					return
				}
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing auth")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["uuid"] != "stable" {
					t.Error("missing idempotency key")
				}
				if reply && r.URL.Path != "/open-apis/im/v1/messages/om_root/reply" {
					t.Error(r.URL.Path)
				}
				if !reply && (r.URL.Query().Get("receive_id_type") != "chat_id" || body["receive_id"] != "oc_chat") {
					t.Error("invalid destination")
				}
				w.Write([]byte(`{"code":0,"data":{"message_id":"om_sent","chat_id":"oc_chat"}}`))
			}))
			defer server.Close()
			f := &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}
			m := Outgoing{ConversationID: "oc_chat", Text: "answer", ClientMessageID: "stable"}
			if reply {
				m.ThreadID = "om_root"
			}
			receipt, err := f.Post(context.Background(), m)
			if err != nil || receipt.MessageID != "om_sent" {
				t.Fatalf("%+v %v", receipt, err)
			}
		})
	}
}

func TestFeishuLostReceiptIsUncertain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
			return
		}
		w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	f := &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}
	_, err := f.Post(context.Background(), Outgoing{ConversationID: "chat", Text: "answer", ClientMessageID: "stable"})
	var fault *FeishuError
	if !errors.As(err, &fault) || !fault.Uncertain {
		t.Fatalf("lost receipt not uncertain: %v", err)
	}
}

func TestFeishuRichPostAndMediaBoundary(t *testing.T) {
	f := &Feishu{AppID: "app", TenantID: "tenant"}
	var got Incoming
	d := f.dispatcher(func(_ context.Context, m Incoming) error { got = m; return nil })
	payload := []byte(`{"schema":"2.0","header":{"event_id":"ev","event_type":"im.message.receive_v1","app_id":"app","tenant_key":"tenant"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_owner"},"tenant_key":"tenant"},"message":{"message_id":"om_msg","chat_id":"oc_chat","message_type":"post","content":"{\"title\":\"Report\",\"content\":[[{\"tag\":\"text\",\"text\":\"Review this\"},{\"tag\":\"img\",\"image_key\":\"img_1\"}]]}"}}}`)
	if _, err := d.Do(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if got.Text != "Report\nReview this" || len(got.Attachments) != 1 || got.Attachments[0].MessageID != "om_msg" {
		t.Fatalf("lost post content: %+v", got)
	}
	if _, err := f.Download(context.Background(), Attachment{ID: "key", URL: "https://attacker.invalid"}); err == nil {
		t.Fatal("download without message provenance")
	}
}

func TestFeishuLongConnectionAcknowledgesAfterDurableStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	acked := make(chan int, 1)
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/callback/ws/endpoint" {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"URL": endpoint + "/socket?device_id=d&service_id=1"}})
			return
		}
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		frame := larkws.Frame{Method: int32(larkws.FrameTypeData), Headers: []larkws.Header{{Key: "type", Value: "event"}, {Key: "message_id", Value: "frame1"}, {Key: "sum", Value: "1"}, {Key: "seq", Value: "0"}}, Payload: []byte(`{"schema":"2.0","header":{"event_id":"ev","event_type":"im.message.receive_v1","app_id":"app","tenant_key":"tenant"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"},"tenant_key":"tenant"},"message":{"message_id":"om_msg","chat_id":"oc_chat","message_type":"text","content":"{\"text\":\"hello\"}"}}}`)}
		raw, _ := frame.Marshal()
		if socket.WriteMessage(websocket.BinaryMessage, raw) != nil {
			return
		}
		for {
			_, raw, err := socket.ReadMessage()
			if err != nil {
				return
			}
			var reply larkws.Frame
			if reply.Unmarshal(raw) != nil || reply.Method != int32(larkws.FrameTypeData) {
				continue
			}
			var response struct {
				Code int `json:"code"`
			}
			json.Unmarshal(reply.Payload, &response)
			acked <- response.Code
			return
		}
	}))
	defer server.Close()
	endpoint = strings.Replace(server.URL, "http", "ws", 1)
	f := &Feishu{AppID: "app", AppSecret: "secret", TenantID: "tenant", APIURL: server.URL, HTTPClient: server.Client()}
	done := make(chan error, 1)
	go func() {
		done <- f.Run(ctx, func(context.Context, Incoming) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-acked:
		t.Fatal("ack before durable store")
	default:
	}
	close(release)
	select {
	case code := <-acked:
		if code != 200 {
			t.Fatalf("ack code %d", code)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("connection did not cancel")
		}
	}
}

func TestFeishuMediaUploadDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
		case "/open-apis/im/v1/files":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing authentication")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("file_name") != "data.json" || r.FormValue("file_type") != "stream" {
				t.Error("invalid file metadata")
			}
			w.Write([]byte(`{"code":0,"data":{"file_key":"file1"}}`))
		case "/open-apis/im/v1/messages/msg1/resources/file1":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"hello":"world"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	f := &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}
	a, err := f.Upload(context.Background(), Attachment{Name: "data.json", Kind: "file"}, strings.NewReader(`{"hello":"world"}`))
	if err != nil || a.ID != "file1" {
		t.Fatalf("upload %+v %v", a, err)
	}
	a.MessageID = "msg1"
	data, err := f.Download(context.Background(), a)
	if err != nil || string(data) != `{"hello":"world"}` {
		t.Fatalf("download %s %v", data, err)
	}
}

func TestFeishuRateLimitPreservesProviderCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
			return
		}
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(400)
		w.Write([]byte(`{"code":230020,"msg":"This operation triggers the frequency limit"}`))
	}))
	defer server.Close()
	f := &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}
	_, err := f.Post(context.Background(), Outgoing{ConversationID: "chat", Text: "answer", ClientMessageID: "stable"})
	var fault *FeishuError
	if !errors.As(err, &fault) || !fault.Retryable || fault.Uncertain || fault.Code != "230020" || fault.RetryAfter != 3*time.Second {
		t.Fatalf("wrong rate limit classification: %#v", err)
	}
}

func TestFeishuLongOutboxKeyUsesBoundedStableUUID(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		id, _ := body["uuid"].(string)
		if len(id) > 50 || id == "" {
			t.Error("unbounded provider uuid")
		}
		if seen != "" && seen != id {
			t.Error("unstable provider uuid")
		}
		seen = id
		w.Write([]byte(`{"code":0,"data":{"message_id":"sent","chat_id":"chat"}}`))
	}))
	defer server.Close()
	f := &Feishu{AppID: "app", AppSecret: "secret", APIURL: server.URL, HTTPClient: server.Client()}
	for i := 0; i < 2; i++ {
		if _, err := f.Post(context.Background(), Outgoing{ConversationID: "chat", Text: "text", ClientMessageID: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
}
