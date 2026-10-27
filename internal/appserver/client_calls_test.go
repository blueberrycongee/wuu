package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type messageWriter struct {
	messages chan []byte
}

func (w *messageWriter) Write(p []byte) (int, error) {
	copyOfP := append([]byte(nil), p...)
	w.messages <- copyOfP
	return len(p), nil
}

func TestCallClientRequiresNegotiatedMethod(t *testing.T) {
	writer := &messageWriter{messages: make(chan []byte, 2)}
	srv := New(newTestRuntime(t, &fakeClient{}), writer)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"initialize","params":{}}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	<-writer.messages

	if _, err := srv.callClient(context.Background(), MethodBrowserListTabs, BrowserListTabsParams{}); err == nil || !strings.Contains(err.Error(), "did not advertise") {
		t.Fatalf("expected capability error, got %v", err)
	}
	select {
	case unexpected := <-writer.messages:
		t.Fatalf("unnegotiated request reached client: %s", unexpected)
	default:
	}
}

func TestCallClientRejectsClosedServer(t *testing.T) {
	writer := &messageWriter{messages: make(chan []byte, 1)}
	srv := New(newTestRuntime(t, &fakeClient{}), writer)
	srv.setClientMethods([]string{MethodBrowserListTabs})
	srv.Close()

	if _, err := srv.callClient(context.Background(), MethodBrowserListTabs, BrowserListTabsParams{}); !errors.Is(err, errServerClosed) {
		t.Fatalf("callClient error = %v, want server closed", err)
	}
	select {
	case unexpected := <-writer.messages:
		t.Fatalf("closed server wrote reverse request: %s", unexpected)
	default:
	}
}

func TestCallClientCompletesNegotiatedReverseRPC(t *testing.T) {
	writer := &messageWriter{messages: make(chan []byte, 2)}
	srv := New(newTestRuntime(t, &fakeClient{}), writer)
	initialize := `{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v0.1","client":{"name":"cloud-host"},"capabilities":{"reverse_rpc":{"methods":["browser/list_tabs"]}}}}`
	if err := srv.handleLine(context.Background(), []byte(initialize)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	<-writer.messages

	type callResult struct {
		raw json.RawMessage
		err error
	}
	done := make(chan callResult, 1)
	go func() {
		raw, err := srv.callClient(context.Background(), MethodBrowserListTabs, BrowserListTabsParams{ThreadID: "thread-browser", Workdir: "/workspace"})
		done <- callResult{raw: raw, err: err}
	}()

	var request Request
	select {
	case raw := <-writer.messages:
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatalf("decode reverse request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reverse request")
	}
	if request.Method != MethodBrowserListTabs || string(request.ID) != `"srv-1"` {
		t.Fatalf("unexpected reverse request: %+v", request)
	}

	var params BrowserListTabsParams
	if err := json.Unmarshal(request.Params, &params); err != nil || params.ThreadID != "thread-browser" {
		t.Fatalf("reverse request lost resource binding: params=%s err=%v", request.Params, err)
	}

	srv.deliverClientResponse([]byte(`{"id":"srv-1","result":{"tab_ids":["tab-1"]}}`))
	select {
	case result := <-done:
		if result.err != nil || !strings.Contains(string(result.raw), "tab-1") {
			t.Fatalf("unexpected call result: raw=%s err=%v", result.raw, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reverse call completion")
	}
}
