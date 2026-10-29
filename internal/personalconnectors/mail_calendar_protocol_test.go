package personalconnectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGmailMIMEAndAcknowledgementContract(t *testing.T) {
	p, err := buildGmail(Connection{}, "gmail.send", providerJSON(map[string]any{"to": []string{"synthetic@example.invalid"}, "subject": "测试", "body": strings.Repeat("body", 100)}))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Raw string `json:"raw"`
	}
	if json.Unmarshal(p.Body, &envelope) != nil {
		t.Fatal("bad envelope")
	}
	message, err := base64.RawURLEncoding.DecodeString(envelope.Raw)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(message), "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("missing MIME separator")
	}
	for _, line := range strings.Split(parts[1], "\r\n") {
		if len(line) > 76 {
			t.Fatal("base64 MIME line exceeded RFC limit")
		}
	}
	if _, err = p.Filter([]byte(`{"success":true}`)); err == nil {
		t.Fatal("unidentified send acknowledged")
	}
	result, err := p.Filter([]byte(`{"id":"message1","threadId":"thread1"}`))
	if err != nil || !strings.Contains(string(result), "unverified") {
		t.Fatal("receipt must not claim recipient delivery")
	}
}
func TestCalendarPreflightAndReadbackContracts(t *testing.T) {
	p, err := buildCalendar(Connection{}, "calendar.create", json.RawMessage(`{"calendar_id":"primary","summary":"Meeting","start":"2026-10-28T09:00:00+08:00","end":"2026-10-28T10:00:00+08:00","timezone":"Asia/Shanghai","attendees":["synthetic@example.invalid"],"send_updates":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Preflight) == 0 || p.Verify == nil {
		t.Fatal("create lacks availability or readback")
	}
	if _, err = p.Preflight[0].Filter([]byte(`{"items":[{"id":"busy","status":"confirmed","transparency":"opaque"}]}`)); err == nil {
		t.Fatal("conflict not blocked")
	}
	if _, err = p.Preflight[0].Filter([]byte(`{"items":[],"nextPageToken":"more"}`)); err == nil {
		t.Fatal("incomplete availability accepted")
	}
	if _, err = p.Preflight[0].Filter([]byte(`{"items":[]}`)); err != nil {
		t.Fatal(err)
	}
	reads, err := p.Verify(json.RawMessage(`{"id":"event1","etag":"\"etag2\"","status":"confirmed"}`))
	if err != nil || len(reads) != 1 {
		t.Fatal("missing event verification")
	}
	if _, err = reads[0].Filter([]byte(`{"id":"event1","etag":"\"etag2\"","status":"confirmed","summary":"wrong"}`)); err == nil {
		t.Fatal("wrong event accepted")
	}
	for _, arg := range []string{`{"calendar_id":"primary","event_id":"event1","etag":"*","send_updates":"none"}`, `{"calendar_id":"primary","event_id":"event1","etag":"unquoted","send_updates":"none"}`} {
		if _, err = buildCalendar(Connection{}, "calendar.cancel", json.RawMessage(arg)); err == nil {
			t.Fatal("non-specific etag accepted")
		}
	}
}

func TestCalendarBrokerCreateVerificationAndCancelEndToEnd(t *testing.T) {
	for _, mode := range []string{"create", "reschedule", "mismatch", "cancel", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			a := Actor{"personal", "space", "session"}
			ctx := context.Background()
			reads, writes := 0, 0
			body := `{"id":"event1","etag":"\"new\"","status":"confirmed","summary":"Meeting","start":{"dateTime":"2026-10-28T09:00:00+08:00","timeZone":"Asia/Shanghai"},"end":{"dateTime":"2026-10-28T10:00:00+08:00","timeZone":"Asia/Shanghai"},"attendees":[{"email":"synthetic@example.invalid"}]}`
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				data := body
				status := 200
				if r.Method == "GET" {
					reads++
					if strings.Contains(r.URL.RawQuery, "timeMin") {
						data = `{"items":[]}`
						if mode == "conflict" {
							data = `{"items":[{"id":"busy","status":"confirmed"}]}`
						}
					} else if mode == "cancel" {
						if writes == 0 {
							data = `{"id":"event1","etag":"\"old\"","status":"confirmed"}`
						} else {
							status = 410
							data = `{"error":{"message":"gone"}}`
						}
					} else if mode == "reschedule" && writes == 0 {
						data = strings.Replace(body, `\"new\"`, `\"old\"`, 1)
					} else if mode == "mismatch" {
						data = strings.Replace(body, "Meeting", "Changed", 1)
					}
				} else if r.Method == "POST" || r.Method == "DELETE" || r.Method == "PATCH" {
					writes++
					if r.Method == "DELETE" {
						if r.Header.Get("If-Match") != `"old"` {
							t.Fatal("missing exact etag")
						}
						status = 204
						data = ""
					}
				} else {
					t.Fatal("unexpected request")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(data)), Header: http.Header{}}, nil
			})}
			b := newTestBroker(Options{Store: &memoryStore{values: map[string][]byte{}}, HTTPClient: client, Authorization: func(context.Context, Actor) (string, error) { return "user-turn", nil }})
			defer b.Close()
			err := b.RegisterVerified(ctx, a, Grant{Connection: Connection{ID: "calendar", Provider: "google-calendar", AccountID: "owner", Domain: "personal", Scopes: []string{"https://www.googleapis.com/auth/calendar.events"}, Resources: []string{"primary"}}, AccessToken: "fixture-secret-123", ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			request := Request{ConnectionID: "calendar", Operation: "calendar.create", RequestID: "once", Arguments: json.RawMessage(`{"calendar_id":"primary","summary":"Meeting","start":"2026-10-28T09:00:00+08:00","end":"2026-10-28T10:00:00+08:00","timezone":"Asia/Shanghai","attendees":["synthetic@example.invalid"],"send_updates":"none"}`)}
			if mode == "cancel" {
				request.Operation = "calendar.cancel"
				request.Arguments = json.RawMessage(`{"calendar_id":"primary","event_id":"event1","etag":"\"old\"","send_updates":"none"}`)
			}
			if mode == "reschedule" {
				request.Operation = "calendar.reschedule"
				request.Arguments = json.RawMessage(`{"calendar_id":"primary","event_id":"event1","etag":"\"old\"","start":"2026-10-28T09:00:00+08:00","end":"2026-10-28T10:00:00+08:00","timezone":"Asia/Shanghai","send_updates":"none"}`)
			}
			pending, err := b.Call(ctx, a, request)
			if err != nil || pending.State != "awaiting_confirmation" || reads != 0 || writes != 0 {
				t.Fatalf("unexpected initial network %#v %v", pending, err)
			}
			result, err := b.Decide(ctx, a, pending.ActionID, pending.Digest, true)
			if err != nil {
				t.Fatal(err)
			}
			expected := "completed"
			if mode == "mismatch" {
				expected = "uncertain"
			}
			if mode == "conflict" {
				expected = "failed"
				if writes != 0 {
					t.Fatal("wrote into conflicting calendar")
				}
			} else if writes != 1 || (mode != "reschedule" && reads != 2) || (mode == "reschedule" && reads != 3) {
				t.Fatalf("bad HTTP sequence %s", fmt.Sprint(reads, writes))
			}
			if result.State != expected {
				t.Fatalf("got %#v want %s", result, expected)
			}
			if expected == "completed" && result.Verification != "read_back_verified" {
				t.Fatal("missing verified receipt")
			}
			_, err = b.Call(ctx, a, request)
			if err != nil || writes > 1 {
				t.Fatal("effect repeated")
			}
		})
	}
}
