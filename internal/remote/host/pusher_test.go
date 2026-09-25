package host

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

func TestExpoPusherSendsRequest(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotBody   []byte
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"status":"ok","id":"ticket-1"}]`))
	}))
	defer ts.Close()

	p := &ExpoPusher{
		Endpoint:   ts.URL + "/--/api/v2/push/send",
		HTTPClient: ts.Client(),
	}
	p.Push(context.Background(), HostPushEvent{
		Device:   "device-1",
		Token:    "ExponentPushToken[abc123]",
		Platform: "ios",
		Hint:     "agent_done",
		ThreadID: "thread-42",
		At:       time.Unix(0, 0).UTC(),
	})

	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/--/api/v2/push/send") {
		t.Errorf("path: want suffix /--/api/v2/push/send, got %s", gotPath)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type: want application/json, got %s", gotCT)
	}
	var batch []ExpoPushRequest
	if err := json.Unmarshal(gotBody, &batch); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, gotBody)
	}
	if len(batch) != 1 {
		t.Fatalf("batch size: want 1, got %d", len(batch))
	}
	got := batch[0]
	if got.To != "ExponentPushToken[abc123]" {
		t.Errorf("to: want ExponentPushToken[abc123], got %q", got.To)
	}
	if got.Title == "" {
		t.Errorf("title: want non-empty")
	}
	if got.Body == "" {
		t.Errorf("body: want non-empty")
	}
	if got.Data["thread_id"] != "thread-42" {
		t.Errorf("data.thread_id: want thread-42, got %v", got.Data["thread_id"])
	}
	if got.Data["hint"] != "agent_done" {
		t.Errorf("data.hint: want agent_done, got %v", got.Data["hint"])
	}
	// The mobile tap handler only consumes data.url; without it a delivered
	// notification cannot deep-link into the thread.
	if got.Data["url"] != "wuu://thread/thread-42" {
		t.Errorf("data.url: want wuu://thread/thread-42, got %v", got.Data["url"])
	}
}

func TestExpoPusherSkipsNonExpoToken(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	p := &ExpoPusher{Endpoint: ts.URL, HTTPClient: ts.Client()}
	p.Push(context.Background(), HostPushEvent{
		Token: "raw-apns-token-no-prefix",
		Hint:  "agent_done",
	})
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("HTTP calls: want 0, got %d (non-Expo token should be skipped)", calls)
	}
}

func TestExpoPusherIsExpoToken(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"ExponentPushToken[abc123XYZ-_]", true},
		{"ExpoPushToken[abc123XYZ-_]", true},
		{"ExponentPushToken[]", false}, // too short
		{"plain", false},
		{"", false},
		{"ExponentPushToken[", false}, // no closing bracket, too short
	}
	for _, c := range cases {
		if got := isExpoToken(c.token); got != c.want {
			t.Errorf("isExpoToken(%q): want %v, got %v", c.token, c.want, got)
		}
	}
}

// The registrar is what binds device/push_register to the paired-device
// record; without it every mobile registration failed as "remote-only" and
// no token was ever stored.
func TestDevicePushRegistrarRoundTrip(t *testing.T) {
	store, pub := makeStore(t, "registrar-phone")
	registrar := devicePushRegistrar{store: store, devPub: pub}
	if err := registrar.RegisterDevicePush(appserver.DevicePushRegisterParams{
		Token: "ExponentPushToken[reg]", Platform: "ios",
	}); err != nil {
		t.Fatalf("RegisterDevicePush: %v", err)
	}
	token, platform, ok := store.DevicePush(pub)
	if !ok || token != "ExponentPushToken[reg]" || platform != "ios" {
		t.Fatalf("DevicePush after register = %q/%q/%v", token, platform, ok)
	}
	if err := registrar.UnregisterDevicePush(appserver.DevicePushUnregisterParams{}); err != nil {
		t.Fatalf("UnregisterDevicePush: %v", err)
	}
	if _, _, ok := store.DevicePush(pub); ok {
		t.Fatal("DevicePush must be cleared after unregister")
	}
}
