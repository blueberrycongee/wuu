package openai

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientCloseReleasesOwnedWebSocketTimersNotBorrowedCache(t *testing.T) {
	owned, err := New(ClientConfig{BaseURL: "http://fixture.invalid", APIKey: "fixture", ResponsesTransport: providers.StreamTransportWebSocket})
	if err != nil {
		t.Fatal(err)
	}
	session := owned.responsesWSCache.session("fixture")
	fired := make(chan struct{}, 1)
	session.idleTimer = time.AfterFunc(time.Hour, func() { fired <- struct{}{} })
	session.continuation = &responsesWebSocketContinuation{lastResponseID: "fixture"}
	if err = owned.Close(); err != nil {
		t.Fatal(err)
	}
	if session.idleTimer != nil || session.continuation != nil || owned.responsesWSCache.session("resurrect") != nil {
		t.Fatal("owned cache resources survived close")
	}
	if err = owned.PrewarmResponsesWebSocket(context.Background(), "fixture"); err == nil {
		t.Fatal("closed cache permitted dialing")
	}
	if err = owned.Close(); err != nil {
		t.Fatal(err)
	}
	shared := NewResponsesWebSocketCache()
	borrowed, err := New(ClientConfig{BaseURL: "http://fixture.invalid", APIKey: "fixture", ResponsesTransport: providers.StreamTransportWebSocket, ResponsesWebSocketCache: shared})
	if err != nil {
		t.Fatal(err)
	}
	if err = borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if shared.session("peer") == nil {
		t.Fatal("transient client closed borrowed cache")
	}
	_ = shared.Close()
}

func TestClientCloseEndsPrewarmedWebSocket(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
		close(closed)
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "fixture", ResponsesTransport: providers.StreamTransportWebSocket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = client.PrewarmResponsesWebSocket(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("prewarmed connection survived provider close")
	}
}
