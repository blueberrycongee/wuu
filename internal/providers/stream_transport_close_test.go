package providers

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"
)

func TestOwnedStreamingPoolCleanupPreservesBorrowedPeerAndActiveRequest(t *testing.T) {
	active := make(chan struct{})
	finish := make(chan struct{})
	closed := make(chan net.Conn, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/active" {
			close(active)
			<-finish
		}
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- conn
		}
	}
	server.Start()
	defer server.Close()
	base := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	defer base.CloseIdleConnections()
	owned := BuildStreamingHTTPClient(base, ResolveStreamTransportConfig(nil))
	request := func(client *http.Client, path string, trace *httptrace.ClientTrace) error {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+path, nil)
		if trace != nil {
			req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return err
	}
	if err := request(base, "/peer", nil); err != nil {
		t.Fatal(err)
	}
	if err := request(owned, "/idle", nil); err != nil {
		t.Fatal(err)
	}
	CloseOwnedStreamingHTTPClient(base, owned)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned idle connection remained open")
	}
	reused := false
	if err := request(base, "/peer", &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}); err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("borrowed peer connection was closed")
	}
	result := make(chan error, 1)
	go func() { result <- request(owned, "/active", nil) }()
	<-active
	CloseOwnedStreamingHTTPClient(base, owned)
	select {
	case err := <-result:
		t.Fatalf("active request was interrupted: %v", err)
	default:
	}
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	CloseOwnedStreamingHTTPClient(base, owned)
}
