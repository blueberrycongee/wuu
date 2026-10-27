package host_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/appserver"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/phone"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type federationWireProvider struct {
	client         *phone.Client
	enabled        bool
	corruptReceipt bool
}

func (p *federationWireProvider) ID() string { return "fixture.session-transport" }
func (p *federationWireProvider) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StateActive}
}
func (p *federationWireProvider) Close(context.Context) error { return nil }
func (p *federationWireProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{pluginhost.SessionTransportDescriptor()}
}

func (*federationWireProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }

type federationCaller struct{}

func (*federationCaller) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }

func (*federationCaller) ID() string { return "fixture.cloud-agent" }
func (p *federationCaller) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StateActive}
}
func (*federationCaller) Close(context.Context) error { return nil }
func (p *federationCaller) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginhost.SessionTransportService, MajorVersion: 1, Required: true}}
}
func (p *federationWireProvider) InvokeService(ctx context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if !p.enabled {
		return nil, &pluginhost.HostServiceError{Code: "session_host_offline", Message: "selected host is offline"}
	}
	var result json.RawMessage
	err := p.client.Protocol().Call(ctx, "session/federation", input.Params, &result)
	if p.corruptReceipt && err == nil {
		return json.RawMessage(`{"version":1,"status":"completed","locator":{"version":1,"execution_host":"another-host"},"result":{"sensitive":"wrong host"}}`), nil
	}
	return result, err
}

// Two isolated runtime/store/app-server authorities, a real TCP cloud endpoint,
// and the existing encrypted relay/host controller transport. No inference
// credentials or installed external engine are used.
func TestSessionFederationTwoHostsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	localGate := &gatedClient{release: make(chan struct{}, 8)}
	local := newRemoteTestRuntime(t, localGate)
	cloud := newRemoteTestRuntime(t, &gatedClient{release: make(chan struct{}, 8)})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cloudDone := make(chan struct{})
	go func() {
		defer close(cloudDone)
		conn, e := listener.Accept()
		if e == nil {
			defer conn.Close()
			_ = appserver.RunStdio(ctx, cloud, conn, conn)
		}
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(); cancel(); <-cloudDone }()
	cloudRPC := wuuexec.NewProtocolClient(conn, conn)
	go func() {
		for range cloudRPC.Notifications() {
		}
	}()
	var defaultLocal appserver.ThreadStartResult
	if err = cloudRPC.Call(ctx, "thread/start", appserver.ThreadStartParams{}, &defaultLocal); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := cloud.Sessions.Find(defaultLocal.Thread.ID); !ok {
		t.Fatal("default GUI/appserver endpoint did not create locally")
	}
	reg, err := relay.OpenRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewServer(relay.New(relay.Options{Registry: reg, Logf: t.Logf}).Handler())
	defer relayServer.Close()
	relayURL := "ws" + strings.TrimPrefix(relayServer.URL, "http") + "/v1/connect"
	store, err := host.LoadOrCreateStore(filepath.Join(t.TempDir(), "remote.json"), "local")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetRelayURL(relayURL); err != nil {
		t.Fatal(err)
	}
	uri := make(chan string, 1)
	var mu sync.Mutex
	allowed := map[string]bool{}
	revoked := false
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		if !strings.HasPrefix(r.Action, "session/federation.") {
			return access.Decision{Allow: r.Owner}, nil
		}
		if r.Owner {
			t.Error("federation inherited paired controller ownership")
		}
		mu.Lock()
		defer mu.Unlock()
		allow := !revoked
		for _, resource := range r.Resources {
			if resource.Kind == "session" && !allowed[resource.ID] {
				allow = false
			}
		}
		return access.Decision{Allow: allow}, nil
	})
	h, err := host.New(host.Options{Runtime: local, Store: store, Pairing: &host.PairingConfig{DelegatedOnly: true, Once: true, OnURI: func(v string) { uri <- v }}, ReconnectMin: 10 * time.Millisecond,
		AppServerOptions: []appserver.ServerOption{appserver.WithAccessPolicy(policy), appserver.WithSessionFederation(appserver.SessionFederationConfig{HostID: "local-host", Models: map[string][]string{"fake-provider": {"fake-model"}}, ToolAllow: []string{"read"}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	hostCtx, hostCancel := context.WithCancel(ctx)
	hostDone := make(chan struct{})
	go func() { defer close(hostDone); _ = h.Run(hostCtx) }()
	defer func() { hostCancel(); <-hostDone }()
	var pairingURI string
	select {
	case pairingURI = <-uri:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	creds, err := phone.Pair(ctx, pairingURI, "cloud-agent")
	if err != nil {
		t.Fatal(err)
	}
	client, err := phone.NewClient(creds, phone.Options{ReconnectMin: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	clientCtx, clientCancel := context.WithCancel(ctx)
	clientDone := make(chan struct{})
	go func() { defer close(clientDone); _ = client.Run(clientCtx) }()
	defer func() { clientCancel(); <-clientDone }()
	if err = client.WaitAttached(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		for range client.Protocol().Notifications() {
		}
	}()
	transport := &federationWireProvider{client: client, enabled: true}
	if conflicts := cloud.PluginHost.ServiceRegistry().RegisterClients(transport, &federationCaller{}); len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	cloud.PluginHost.Add(transport)
	cloud.PluginHost.Add(&federationCaller{})
	binding, err := cloud.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	callCtx := runtime.WithInvocationState(ctx, binding)
	request := pluginhost.SessionRouteRequest{Version: 1, Operation: "create", OperationID: "cloud-create", Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "local-host", DisplayHost: "cloud"}, Params: json.RawMessage(`{"visibility":"user","context_source":"fresh","provider":"fake-provider","model":"fake-model"}`)}
	call := func(r pluginhost.SessionRouteRequest) pluginhost.SessionRouteResult {
		t.Helper()
		out, e := cloud.PluginSessionRouter.Route(callCtx, (&federationCaller{}).ID(), r)
		if e != nil {
			t.Fatal(e)
		}
		if out.Error != nil {
			t.Fatalf("federation error: %+v", out.Error)
		}
		return out
	}
	created := call(request)
	again := call(request)
	if created.Locator.SessionID == "" || created.Locator.SessionID != again.Locator.SessionID {
		t.Fatal("retry duplicated creation")
	}
	sessionID := created.Locator.SessionID
	if _, ok, _ := cloud.Sessions.Find(sessionID); ok {
		t.Fatal("remote route created a cloud-local session")
	}
	mu.Lock()
	allowed[sessionID] = true
	mu.Unlock()
	metadata, ok, err := local.Sessions.Find(sessionID)
	if err != nil || !ok || metadata.PermissionMode != "read_only" {
		t.Fatalf("remote narrowing: %+v %v", metadata, err)
	}
	transport.corruptReceipt = true
	malformed, err := cloud.PluginSessionRouter.Route(callCtx, (&federationCaller{}).ID(), pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "corrupt-receipt", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `"}`)})
	if err != nil || malformed.Status != "unknown" || len(malformed.Result) > 0 {
		t.Fatalf("invalid receipt not isolated: %+v %v", malformed, err)
	}
	transport.corruptReceipt = false
	transport.enabled = false
	request.OperationID = "disabled"
	if _, err = cloud.PluginSessionRouter.Route(callCtx, (&federationCaller{}).ID(), request); err == nil {
		t.Fatal("disabled remote provider accepted request")
	}
	if sessions, e := cloud.Sessions.List(0); e != nil || len(sessions) != 1 {
		t.Fatalf("disabled remote fell back to cloud: %d %v", len(sessions), e)
	}
	transport.enabled = true
	request = pluginhost.SessionRouteRequest{Version: 1, Operation: "send", OperationID: "remote-turn", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `","input":{"prompt":"do fixture work"}}`)}
	sent := call(request)
	resent := call(request)
	if string(sent.Result) != string(resent.Result) {
		t.Fatalf("retry did not return admission receipt: %s / %s", sent.Result, resent.Result)
	}
	// Disconnect the existing controller, finish on the execution host, and
	// reconnect through the same remote attach/replay transport before retrying.
	clientCancel()
	<-clientDone
	localGate.release <- struct{}{}
	clientCtx, clientCancel = context.WithCancel(ctx)
	clientDone = make(chan struct{})
	go func() { defer close(clientDone); _ = client.Run(clientCtx) }()
	if err = client.WaitAttached(ctx); err != nil {
		t.Fatal(err)
	}
	reconnected := call(request)
	if string(reconnected.Result) != string(sent.Result) {
		t.Fatal("reconnect repeated the accepted operation")
	}
	inspected := call(pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "observe", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `","request_id":"remote-turn","wait":"terminal","timeout_ms":20000}`)})
	var inspection pluginhost.SessionInspectResult
	if err = json.Unmarshal(inspected.Result, &inspection); err != nil || inspection.Turn == nil || inspection.Turn.FinalOutput != "remote turn done" {
		t.Fatalf("result=%s err=%v", inspected.Result, err)
	}
	// Cancellation is fenced to the accepted turn. Replaying an old cancel
	// must never stop a later turn in the same session.
	second := request
	second.OperationID = "cancelled-turn"
	secondResult := call(second)
	var turn pluginhost.SessionSendResult
	if err = json.Unmarshal(secondResult.Result, &turn); err != nil || turn.TurnID == "" {
		t.Fatalf("second admission=%s %v", secondResult.Result, err)
	}
	cancellation := pluginhost.SessionRouteRequest{Version: 1, Operation: "cancel", OperationID: "cancel-exact", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `","turn_id":"` + turn.TurnID + `"}`)}
	canceled := call(cancellation)
	call(pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "cancel-observe", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `","request_id":"cancelled-turn","wait":"terminal","timeout_ms":20000}`)})
	third := request
	third.OperationID = "after-cancel"
	call(third)
	cancelReplay := call(cancellation)
	if string(canceled.Result) != string(cancelReplay.Result) {
		t.Fatal("cancel replay lost exact receipt")
	}
	localGate.release <- struct{}{}
	final := call(pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "final-observe", Locator: created.Locator, Params: json.RawMessage(`{"session_id":"` + sessionID + `","request_id":"after-cancel","wait":"terminal","timeout_ms":20000}`)})
	if err = json.Unmarshal(final.Result, &inspection); err != nil || inspection.Turn == nil || inspection.Turn.FinalOutput != "remote turn done" {
		t.Fatalf("later turn canceled by stale retry: %s %v", final.Result, err)
	}
	denied := request
	denied.Locator.SessionID = "ungranted"
	denied.Params = json.RawMessage(`{"session_id":"ungranted","input":{"prompt":"no"}}`)
	if _, err = cloud.PluginSessionRouter.Route(callCtx, (&federationCaller{}).ID(), denied); err == nil {
		t.Fatal("ungranted session accepted")
	}
	mu.Lock()
	revoked = true
	mu.Unlock()
	if _, err = cloud.PluginSessionRouter.Route(callCtx, (&federationCaller{}).ID(), request); err == nil {
		t.Fatal("revocation did not fence receipt replay")
	}
	t.Logf("EVIDENCE cloud=%s local_relay=%s cloud_default=%s remote_session=%s operation=remote-turn status=%s", listener.Addr(), relayServer.URL, defaultLocal.Thread.ID, sessionID, inspection.Turn.State)
}
