package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCloseDisconnectsUnfinishedAndAuthenticatedConnections(t *testing.T) {
	srv := New(Options{Logf: func(string, ...any) {}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/connect"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pending, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.CloseNow()
	host := dialRelay(t, ctx, url)
	defer host.ws.CloseNow()
	host.auth(mustID(t), "host")
	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("server retained WebSocket handlers after shutdown")
	}
	if _, _, err := pending.Read(ctx); err == nil {
		t.Fatal("unfinished handshake survived shutdown")
	}
	if _, _, err := host.ws.Read(ctx); err == nil {
		t.Fatal("authenticated host survived shutdown")
	}
	response, err := http.Get(ts.URL + "/v1/connect")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("new connection status = %d", response.StatusCode)
	}
}
