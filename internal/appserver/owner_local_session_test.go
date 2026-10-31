package appserver

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Owner-local routing must preserve actual Coding ownership and nonhuman input,
// reject private/retargeted work, and fence changed intent after lost replies.
func TestOwnerLocalSessionExistingTargetAndIntentFence(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "existing target result"}})
	s := New(rt, io.Discard, WithTrustedLocalTransport())
	defer s.Close()
	target := session.Session{ID: "existing-coding", Owner: "original-owner", Visibility: "user", CWD: rt.RootDir, Provider: "fake-provider", Model: "fake-model", PermissionMode: "standard"}
	if _, err := rt.Sessions.CreateInitialized(target, nil); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	source, _ := principal.AuthenticatedWithValidity(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "space", Subject: "controller"}, func() bool { return !revoked.Load() })
	locator := s.ownerLocalSessionLocator()
	locator.SessionID = target.ID
	control := ownerLocalController{Consumer: "personal-subject", UserSpaceID: "local", ProductDomain: "personal", SessionID: "main", RunID: "run"}
	route := pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "run", Locator: locator, Params: json.RawMessage(`{"session_id":"existing-coding","request_id":"run","if_running":"queue","input":{"prompt":"bounded work"}}`)}
	input := ownerLocalSessionRequest{Request: route, Controller: &control}
	first, err := s.dispatchOwnerLocalSession(context.Background(), source, input)
	if err != nil || first.Status != "completed" || first.Error != nil {
		t.Fatalf("send: %+v %v", first, err)
	}
	replay, err := s.dispatchOwnerLocalSession(context.Background(), source, input)
	if err != nil || string(replay.Result) != string(first.Result) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	input.Request.Params = json.RawMessage(`{"session_id":"existing-coding","request_id":"run","if_running":"queue","input":{"prompt":"different work"}}`)
	if _, err = s.dispatchOwnerLocalSession(context.Background(), source, input); err == nil {
		t.Fatal("same ID changed intent admitted")
	}
	input.Request = route
	input.Request.Operation = "inspect"
	input.Request.OperationID = "inspect-run"
	input.Request.Params = json.RawMessage(`{"session_id":"existing-coding","request_id":"run","wait":"terminal","timeout_ms":1000}`)
	result, err := s.dispatchOwnerLocalSession(context.Background(), source, input)
	if err != nil || result.Error != nil {
		t.Fatalf("inspect: %+v %v", result, err)
	}
	var inspected pluginhost.SessionInspectResult
	json.Unmarshal(result.Result, &inspected)
	if inspected.Turn == nil || inspected.Turn.RequestID != "run" {
		t.Fatalf("lost business run: %s", result.Result)
	}
	metadata, _, _ := rt.Sessions.Find(target.ID)
	if metadata.Owner != target.Owner || metadata.ParentID != "" || metadata.PermissionMode != target.PermissionMode || metadata.ToolPolicyJSON != "" {
		t.Fatal("owner broker changed target policy/ownership")
	}
	revoked.Store(true)
	if _, err = s.dispatchOwnerLocalSession(context.Background(), source, input); err == nil {
		t.Fatal("revoked source inspected cached result")
	}
	revoked.Store(false)
	if _, err = s.dispatchOwnerLocalSession(principal.WithContext(context.Background(), source), source, input); err == nil {
		t.Fatal("forwarded nonowner acquired owner broker")
	}
	input.Request.Locator.ExecutionHost = "other"
	if _, err = s.dispatchOwnerLocalSession(context.Background(), source, input); err == nil {
		t.Fatal("wrong execution host admitted")
	}
	target.ID = "foreign-private"
	target.Visibility = "plugin"
	if _, err := rt.Sessions.CreateInitialized(target, nil); err != nil {
		t.Fatal(err)
	}
	input.Request = route
	input.Request.Locator.SessionID = target.ID
	input.Request.OperationID = "private-run"
	control.RunID = "private-run"
	input.Request.Params = json.RawMessage(`{"session_id":"foreign-private","request_id":"private-run","input":{"prompt":"forbidden"}}`)
	denied, err := s.dispatchOwnerLocalSession(context.Background(), source, input)
	if err == nil && denied.Error == nil {
		t.Fatal("foreign private session controlled")
	}
}

func TestOwnerLocalQueueRevocationAndExactCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	model := &fakeClient{response: providersResponse("done"), onChat: func(call int, _ providers.ChatRequest) {
		if call == 1 {
			close(started)
			<-release
		}
	}}
	rt := newTestRuntime(t, model)
	out := &lockedBuffer{}
	s := New(rt, out, WithTrustedLocalTransport())
	defer s.Close()
	defer once.Do(func() { close(release) })
	if _, err := rt.Sessions.CreateInitialized(session.Session{ID: "busy", Owner: "human", Visibility: "user", CWD: rt.RootDir, Provider: "fake-provider", Model: "fake-model", PermissionMode: "standard"}, nil); err != nil {
		t.Fatal(err)
	}
	identity := principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "local", Subject: "controller"}
	source, _ := principal.Authenticated(identity)
	locator := s.ownerLocalSessionLocator()
	locator.SessionID = "busy"
	makeInput := func(run string) ownerLocalSessionRequest {
		p, _ := json.Marshal(pluginhost.SessionSendParams{SessionID: "busy", RequestID: run, Input: pluginhost.SessionInput{Prompt: run}, IfRunning: "queue"})
		return ownerLocalSessionRequest{Controller: &ownerLocalController{Consumer: "personal", UserSpaceID: "local", ProductDomain: "personal", SessionID: "main", RunID: run}, Request: pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: run, Locator: locator, Params: p}}
	}
	first, err := s.dispatchOwnerLocalSession(context.Background(), source, makeInput("first"))
	if err != nil || first.Error != nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first not running")
	}
	second, err := s.dispatchOwnerLocalSession(context.Background(), source, makeInput("second"))
	if err != nil || second.Error != nil {
		t.Fatalf("queue: %+v %v", second, err)
	}
	var running, queued pluginhost.SessionSendResult
	json.Unmarshal(first.Result, &running)
	json.Unmarshal(second.Result, &queued)
	if queued.State != "queued" || queued.QueueID == "" {
		t.Fatalf("not queued: %+v", queued)
	}
	cancel := makeInput("second")
	cancel.Request.Operation = "cancel"
	cancel.Request.OperationID = "cancel-second"
	cancel.Request.Params, _ = json.Marshal(pluginhost.SessionCancelParams{SessionID: "busy", TurnID: running.TurnID})
	if _, err = s.dispatchOwnerLocalSession(context.Background(), source, cancel); err == nil {
		t.Fatal("cancel interrupted unrelated running request")
	}
	cancel.Request.Params, _ = json.Marshal(pluginhost.SessionCancelParams{SessionID: "busy", QueueID: queued.QueueID})
	cancelled, err := s.dispatchOwnerLocalSession(context.Background(), source, cancel)
	if err != nil || cancelled.Error != nil {
		t.Fatalf("exact cancel: %+v %v", cancelled, err)
	}
	var cancelResult pluginhost.SessionCancelResult
	json.Unmarshal(cancelled.Result, &cancelResult)
	if !cancelResult.Cancelled {
		t.Fatal("exact queued request not cancelled")
	}
	var revoked atomic.Bool
	limited, _ := principal.AuthenticatedWithValidity(identity, func() bool { return !revoked.Load() })
	third, err := s.dispatchOwnerLocalSession(context.Background(), limited, makeInput("third"))
	if err != nil || third.Error != nil {
		t.Fatalf("third queue: %+v %v", third, err)
	}
	revoked.Store(true)
	once.Do(func() { close(release) })
	waitForTurnCompletedForThread(t, out, "busy")
	inspect := makeInput("third")
	inspect.Request.Operation = "inspect"
	inspect.Request.OperationID = "inspect-third"
	inspect.Request.Params = json.RawMessage(`{"session_id":"busy","request_id":"third","wait":"terminal","timeout_ms":1000}`)
	result, err := s.dispatchOwnerLocalSession(context.Background(), source, inspect)
	if err != nil {
		t.Fatal(err)
	}
	var observed pluginhost.SessionInspectResult
	json.Unmarshal(result.Result, &observed)
	if observed.Turn != nil && (observed.Turn.State == "completed" || observed.Turn.State == "running") {
		t.Fatalf("revoked queued request executed: %s", result.Result)
	}
	replay, err := s.dispatchOwnerLocalSession(context.Background(), source, makeInput("third"))
	if err != nil || string(replay.Result) != string(third.Result) {
		t.Fatal("revocation erased accepted command receipt or restarted work")
	}
	model.mu.Lock()
	calls := len(model.requests)
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("revoked/cancelled queue reached model: %d", calls)
	}
	control, found, err := rt.Sessions.ReadControl("busy")
	if err != nil || found && control.ManagerID != "" {
		t.Fatal("broker took manager control")
	}
	state, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread, err := s.ensureThreadLoaded(state, "busy")
	if err != nil {
		t.Fatal(err)
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage && (!item.ReadOnly || item.Origin != "plugin") {
				t.Fatal("broker input acquired human provenance")
			}
		}
	}
}

func TestOwnerLocalBrokerCannotBeReachedAsOrdinaryOrForwardedRPC(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	s := New(rt, out, WithTrustedLocalTransport())
	defer s.Close()
	if err := s.handleLine(context.Background(), []byte(`{"id":"ordinary","method":"session/owner-local","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Fatal("ordinary app-server request acquired private broker")
	}
	validation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer validation.Close()
	input := trustedTransportEnvelope{Version: 1, PeerID: "forged-owner", Owner: true, Principal: principal.LocalOwner().Identity(), Validation: trustedTransportValidator{Address: strings.TrimPrefix(validation.URL, "http://"), Token: strings.Repeat("x", 32)}, Method: ownerLocalSessionMethod, Params: json.RawMessage(`{"request":{"version":1,"operation":"list","operation_id":"list","locator":{"version":1,"execution_host":"invented"},"params":{"scope":"shared"}}}`)}
	params, _ := json.Marshal(input)
	req := Request{ID: json.RawMessage(`"forged"`), Method: "transport/dispatch", Params: params}
	if err := s.handleTrustedTransport(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "source transport must not forward owner authority") {
		t.Fatalf("forwarded owner claim not rejected: %s", out.String())
	}
	guest, _ := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "local", Subject: "nested"})
	req.ID = json.RawMessage(`"nested"`)
	if err := s.handleTrustedTransport(principal.WithContext(context.Background(), guest), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nested transport forbidden") {
		t.Fatal("nested nonowner acquired local broker")
	}
}
