package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Device connections are observers/controllers, not execution owners. Their
// shutdown cannot close the shared host or retire another device's requests.
func TestUserHostOutlivesConnectionsAndKeepsOneSessionAuthority(t *testing.T) {
	model := newBlockingStreamClient("background finished")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = providers.AdaptStreamClient(model)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	host, err := NewUserHost(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	created, err := rt.PluginSessionRouter.Create(context.Background(), "fixture", pluginhost.SessionCreateParams{RequestID: "always-on-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "main"})
	if err != nil {
		t.Fatal("headless session authority missing", err)
	}
	sent, err := rt.PluginSessionRouter.Send(context.Background(), "fixture", pluginhost.SessionSendParams{RequestID: "always-on-work", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "continue after phone leaves"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
	in, out := io.Pipe()
	buffer := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- host.Serve(context.Background(), in, buffer, nil) }()
	fmt.Fprintln(out, `{"id":"same","method":"initialize"}`)
	if got := waitForResponseByID(t, buffer, "same"); got["error"] != nil {
		t.Fatal(got)
	}
	fmt.Fprintln(out, `{"id":"bye","method":"shutdown"}`)
	waitForResponseByID(t, buffer, "bye")
	out.Close()
	<-done
	inspected, err := rt.PluginSessionRouter.Inspect(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "always-on-work"})
	if err != nil || inspected.Turn == nil || inspected.Turn.State != "running" {
		t.Fatalf("disconnect stopped execution: %+v %v", inspected, err)
	}
	close(model.release)
	result, err := rt.PluginSessionRouter.Inspect(context.Background(), "fixture", pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: "always-on-work", Wait: "terminal", TimeoutMS: 2000})
	if err != nil || result.Turn == nil || result.Turn.State != "completed" || result.Turn.TurnID != sent.TurnID {
		t.Fatalf("headless completion failed: %+v %v", result, err)
	}
	secondIn, secondOut := io.Pipe()
	reconnected := &lockedBuffer{}
	secondDone := make(chan error, 1)
	go func() { secondDone <- host.Serve(context.Background(), secondIn, reconnected, nil) }()
	fmt.Fprintln(secondOut, `{"id":"same","method":"initialize"}`)
	waitForResponseByID(t, reconnected, "same")
	if strings.Contains(reconnected.String(), `"id":"bye"`) {
		t.Fatal("reconnect replayed another connection response")
	}
	secondOut.Close()
	<-secondDone
	retry, err := rt.PluginSessionRouter.Create(context.Background(), "fixture", pluginhost.SessionCreateParams{RequestID: "always-on-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "main"})
	if err != nil || retry.SessionID != created.SessionID {
		t.Fatal("reconnect changed stable session", err)
	}
}

type userHostBlockedOutput struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *userHostBlockedOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestUserHostSlowClientDoesNotBlockOtherConnections(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	host, err := NewUserHost(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	slowIn, slowWrite := io.Pipe()
	slow := &userHostBlockedOutput{entered: make(chan struct{}), release: make(chan struct{})}
	slowDone := make(chan error, 1)
	go func() { slowDone <- host.Serve(context.Background(), slowIn, slow, nil) }()
	fmt.Fprintln(slowWrite, `{"id":"same","method":"initialize"}`)
	<-slow.entered
	in, write := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- host.Serve(context.Background(), in, out, nil) }()
	fmt.Fprintln(write, `{"id":"same","method":"initialize"}`)
	waitForResponseByID(t, out, "same")
	finished := make(chan struct{})
	go func() {
		for i := 0; i < 160; i++ {
			_ = host.server.writeNotification("synthetic/change", map[string]int{"index": i})
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("slow consumer blocked shared host")
	}
	close(slow.release)
	slowWrite.Close()
	<-slowDone
	write.Close()
	<-done
}

func TestUserHostRequiresExplicitUserBinding(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if host, err := NewUserHost(rt); err == nil {
		host.Close()
		t.Fatal("legacy Coding was moved to user host lifecycle")
	}
}

// An optional legacy async ID must not let a client's callback occupy the
// shared dispatch lock or extend a user submission's authority past EOF.
func TestUserHostOmittedRequestIDDoesNotBlockPeersOrRetainUserProof(t *testing.T) {
	client := &heldUserHostClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: "state-demo"}, started: make(chan context.Context, 2)}
	host, item := newHeldUserHost(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstIn, firstWrite := io.Pipe()
	firstOutput := &lockedBuffer{}
	firstDone := make(chan error, 1)
	go func() { firstDone <- host.Serve(ctx, firstIn, firstOutput, nil) }()
	relation := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: "main", Seq: 7}, Metadata: map[string]json.RawMessage{"task_links": json.RawMessage(`[{"task_id":"task","requirements_revision":2}]`)}}
	params := PluginClientRequestParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Method: "hold", UserInput: &runtime.ClientUserInput{RequestID: "business-message", Prompt: "literal submission", Relation: relation}}
	frame, _ := json.Marshal(map[string]any{"id": "held", "method": MethodPluginClientRequest, "params": params})
	fmt.Fprintln(firstWrite, string(frame))
	var proof context.Context
	select {
	case proof = <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("callback not admitted: %s", firstOutput.String())
	}
	if err := runtime.AuthorizeClientUserInput(proof, item.ID, "business-message", "main", "literal submission"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AuthorizeClientUserInputRelation(proof, item.ID, "business-message", relation); err != nil {
		t.Fatalf("wire envelope lost relation: %v", err)
	}
	if err := runtime.AuthorizeClientUserInputRelation(proof, item.ID, "business-message", nil); err == nil {
		t.Fatal("wire envelope accepted relation omission")
	}
	secondIn, secondWrite := io.Pipe()
	secondOutput := &lockedBuffer{}
	secondDone := make(chan error, 1)
	go func() { secondDone <- host.Serve(ctx, secondIn, secondOutput, nil) }()
	defer secondWrite.Close()
	fmt.Fprintln(secondWrite, `{"id":"peer","method":"initialize"}`)
	waitForResponseByID(t, secondOutput, "peer")
	firstWrite.Close()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("EOF did not detach held callback")
	}
	select {
	case <-proof.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("EOF retained callback authority")
	}
	if err := runtime.AuthorizeClientUserInput(context.WithoutCancel(proof), item.ID, "business-message", "main", "literal submission"); err == nil {
		t.Fatal("detached proof survived client EOF")
	}
	secondWrite.Close()
	<-secondDone
}

type heldUserHostClient struct {
	pluginClientRequestTestRuntime
	started chan context.Context
}

func (c *heldUserHostClient) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	select {
	case c.started <- ctx:
	case <-ctx.Done():
		return pluginhost.CapabilityInvokeResult{}, ctx.Err()
	}
	<-ctx.Done()
	return pluginhost.CapabilityInvokeResult{}, ctx.Err()
}
func newHeldUserHost(t *testing.T, client pluginhost.Client) (*UserHost, pluginpkg.Plugin) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.WuuHome, rt.RootDir = t.TempDir(), t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "plugin.json")
	writePluginPackageFile(t, manifestPath, `{"id":"state-demo","desktop":{"entry":"desktop.js"}}`)
	writePluginPackageFile(t, filepath.Join(filepath.Dir(manifestPath), "desktop.js"), `export const activate = () => {};`)
	item, err := pluginpkg.LoadManifest(manifestPath, "user")
	if err != nil {
		t.Fatal(err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}
	rt.ExtensionSettings = &extensions.Settings{}
	if err := rt.ExtensionSettings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	rt.PluginHost = pluginhost.New(client)
	host, err := NewUserHost(rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host, item
}

func TestUserHostTransportRejectsGenericMutationAndExport(t *testing.T) {
	client := &heldUserHostClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: "state-demo"}, started: make(chan context.Context, 1)}
	host, item := newHeldUserHost(t, client)
	in, writer := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- host.Serve(context.Background(), in, out, nil) }()
	defer func() { writer.Close(); <-done }()
	for _, method := range []string{MethodPluginStorageSet, MethodThreadListAll, MethodWorkspaceList, MethodConfigRead} {
		params := PluginStorageSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: PluginValueScopeUser, Key: "personal.state", Value: "client overwrite"}
		frame, _ := json.Marshal(map[string]any{"id": method, "method": method, "params": params})
		fmt.Fprintln(writer, string(frame))
		response := waitForResponseByID(t, out, method)
		if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "user host client method is unavailable") {
			t.Errorf("generic operation reached shared authority: %v", response)
		}
	}
	fmt.Fprintln(writer, `{"id":"still-live","method":"initialize","params":{"capabilities":{"reverse_rpc":{"methods":["browser/exec"]}}}}`)
	response := waitForResponseByID(t, out, "still-live")
	result := remarshal[InitializeResult](t, response["result"])
	if result.Features.Browser {
		t.Fatal("client activated headless browser authority")
	}
}

// Push navigation comes from the persisted named-agent binding, never the
// execution session ID or caller-provided notification fields.
func TestUserHostTerminalLocationsOnlyExposeResponsibleMainAgent(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("visible answer")})
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	host, err := NewUserHost(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	in, writer := io.Pipe()
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() { done <- host.Serve(context.Background(), in, out, nil) }()
	defer func() { writer.Close(); <-done }()
	fmt.Fprintln(writer, `{"id":"init","method":"initialize"}`)
	waitForResponseByID(t, out, "init")
	main, err := rt.PluginSessionRouter.Create(context.Background(), "fixture", pluginhost.SessionCreateParams{RequestID: "push-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "agent-main"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := rt.PluginSessionRouter.Create(context.Background(), "fixture", pluginhost.SessionCreateParams{RequestID: "push-task", Visibility: "plugin", ContextSource: "fresh", ParentSessionID: main.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{main.SessionID, child.SessionID} {
		_, err = rt.PluginSessionRouter.Send(context.Background(), "fixture", pluginhost.SessionSendParams{RequestID: "send-" + id, SessionID: id, Input: pluginhost.SessionInput{Prompt: "finish"}})
		if err != nil {
			t.Fatal(err)
		}
		messages := waitForTurnCompletedForThread(t, out, id)
		found := false
		for _, message := range messages {
			if message["method"] != NotificationTurnCompleted {
				continue
			}
			params, _ := message["params"].(map[string]any)
			if params["thread_id"] != id {
				continue
			}
			found = true
			if params["user_space_id"] != "local" || params["product_domain"] != "personal" {
				t.Errorf("terminal lost trusted scope: %v", params)
			}
			location, _ := params["user_location"].(map[string]any)
			if id == main.SessionID {
				if location["agent_id"] != "agent-main" || location["version"] != float64(1) || location["product_domain"] != "personal" || location["session_id"] != nil || location["thread_id"] != nil {
					t.Errorf("main location not authoritative: %v", location)
				}
			} else if location != nil {
				t.Errorf("background execution exposed push navigation: %v", location)
			}
		}
		if !found {
			t.Fatal("terminal missing")
		}
	}
	// A mismatched/unknown thread never gains a locator, including a supplied one.
	forged := []byte(`{"method":"turn/completed","params":{"thread_id":"missing","user_space_id":"other","product_domain":"coding","user_location":{"agent_id":"spoofed"}}}`)
	if _, err := host.Write(forged); err != nil {
		t.Fatal(err)
	}
	messages := waitForNotificationCount(t, out, NotificationTurnCompleted, 3)
	last := messages[len(messages)-1]["params"].(map[string]any)
	if last["user_space_id"] != "local" || last["product_domain"] != "personal" || last["user_location"] != nil {
		t.Fatalf("caller controlled navigation: %v", last)
	}
}
