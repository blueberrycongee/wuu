package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"github.com/blueberrycongee/wuu/internal/version"
)

type messagingE2EDriver struct {
	started, stopped chan struct{}
	incoming         chan personalim.Incoming
	admitted         chan error
	posts            chan personalim.Outgoing
}

func newMessagingE2EDriver() *messagingE2EDriver {
	return &messagingE2EDriver{started: make(chan struct{}), stopped: make(chan struct{}), incoming: make(chan personalim.Incoming), admitted: make(chan error, 8), posts: make(chan personalim.Outgoing, 8)}
}
func (d *messagingE2EDriver) Run(ctx context.Context, accept func(context.Context, personalim.Incoming) error) error {
	close(d.started)
	defer close(d.stopped)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-d.incoming:
			d.admitted <- accept(ctx, event)
		}
	}
}
func (d *messagingE2EDriver) Post(ctx context.Context, output personalim.Outgoing) (personalim.Receipt, error) {
	select {
	case <-ctx.Done():
		return personalim.Receipt{}, ctx.Err()
	case d.posts <- output:
		return personalim.Receipt{ConversationID: output.ConversationID, MessageID: "reply-" + output.ClientMessageID}, nil
	}
}
func (*messagingE2EDriver) Validate(context.Context, personalim.Binding) error { return nil }

// The real SDK, selected messaging/authorization/documents providers, Personal
// process and owner RPC are exercised. Only Slack network I/O is substituted.
func TestPersonalMessagingSDKCompositionE2E(t *testing.T) {
	if os.Getenv("WUU_MESSAGING_E2E") != "1" {
		t.Skip("set WUU_MESSAGING_E2E=1 with built bundled plugins")
	}
	oldVersion := version.Version
	version.Version = "2026.10.19"
	t.Cleanup(func() { version.Version = oldVersion })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	for _, key := range []string{"WUU_SLACK_APP_TOKEN", "WUU_SLACK_BOT_TOKEN", "WUU_FEISHU_APP_ID", "WUU_FEISHU_APP_SECRET"} {
		t.Setenv(key, "")
	}
	config := filepath.Join(t.TempDir(), "config.json")
	var modelCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var input struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &input)
		reply := "SDK_REPLY first"
		if strings.Contains(string(body), "SDK second") {
			reply = "SDK_REPLY second"
		}
		if input.Model == "im-title" {
			reply = "Messaging fixture"
		} else {
			modelCalls.Add(1)
		}
		if input.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": reply}, "finish_reason": nil}}})
			fmt.Fprintf(w, "data: %s\n\ndata: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", chunk)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": reply}, "finish_reason": "stop"}}})
	}))
	defer endpoint.Close()
	configBody, _ := json.Marshal(map[string]any{"default_provider": "fixture", "agent": map[string]any{"model_roles": map[string]any{"title": map[string]string{"provider": "fixture", "model": "im-title"}}}, "providers": map[string]any{"fixture": map[string]string{"type": "openai-compatible", "base_url": endpoint.URL + "/v1", "api_key": "fixture-unused", "model": "fixture"}}})
	if err := os.WriteFile(config, configBody, 0600); err != nil {
		t.Fatal(err)
	}
	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	owner, err := clientauthority.AdmitDesktop(ctx, binding, strings.NewReader(`{"version":1,"user_space_id":"local","product_domain":"personal"}`))
	if err != nil {
		t.Fatal(err)
	}
	options := Options{UserSpaceID: "local", ProductDomain: "personal", CompositionProfile: "personal", HomeDir: home, ConfigPath: config, NonInteractive: true}
	start := func(driver *messagingE2EDriver) *Runtime {
		options.PersonalMessagingDrivers = map[string]personalim.Driver{"slack": driver}
		r, e := New(options)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	connect := func(r *Runtime) (*Client, func(string, any) error) {
		c, e := r.Connect(owner, ClientOptions{Name: "messaging-fixture"})
		if e != nil {
			t.Fatal(e)
		}
		var id, fingerprint string
		for _, p := range r.core.ActivePlugins {
			if p.ID == "personal" {
				id = p.SubjectID
				fingerprint = p.Fingerprint
			}
		}
		if id == "" {
			t.Fatal("Personal not selected")
		}
		return c, func(method string, input any) error {
			raw, _ := json.Marshal(input)
			var out appserver.PluginClientRequestResult
			return c.rpc.call(ctx, appserver.MethodPluginClientRequest, appserver.PluginClientRequestParams{ID: id, Fingerprint: fingerprint, GraphGeneration: r.core.ExtensionSnapshot().GenerationID, Method: method, Input: raw}, &out)
		}
	}
	first := newMessagingE2EDriver()
	r := start(first)
	c, call := connect(r)
	defer func() { _ = c.Close(ctx); _ = r.Close() }()
	if err = call("im.configure", map[string]any{"platform": "slack", "binding": map[string]string{"tenant_id": "T1", "sender_id": "U1", "conversation_id": "D1"}}); err != nil {
		t.Fatal(err)
	}
	if err = call("im.enable", map[string]string{"platform": "slack"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	inject := func(driver *messagingE2EDriver, event personalim.Incoming) {
		t.Helper()
		select {
		case driver.incoming <- event:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		select {
		case e := <-driver.admitted:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	reply := func(driver *messagingE2EDriver, want string) {
		t.Helper()
		select {
		case out := <-driver.posts:
			if out.ConversationID != "D1" || out.ThreadID != "100.1" || out.Text != want {
				t.Fatalf("wrong reply destination/content: %+v", out)
			}
		case <-time.After(12 * time.Second):
			var status appserver.PluginClientRequestResult
			for _, plugin := range r.core.ActivePlugins {
				if plugin.ID == "personal" {
					_ = c.rpc.call(ctx, appserver.MethodPluginClientRequest, appserver.PluginClientRequestParams{ID: plugin.SubjectID, Fingerprint: plugin.Fingerprint, GraphGeneration: r.core.ExtensionSnapshot().GenerationID, Method: "im.status", Input: json.RawMessage(`{}`)}, &status)
				}
			}
			var state struct {
				Connections []struct {
					AgentID string `json:"agent_id"`
				} `json:"connections"`
			}
			_ = json.Unmarshal(status.Result, &state)
			var history appserver.PluginClientRequestResult
			if len(state.Connections) > 0 {
				for _, plugin := range r.core.ActivePlugins {
					if plugin.ID == "personal" {
						raw, _ := json.Marshal(map[string]any{"agent_id": state.Connections[0].AgentID, "start_seq": 1, "limit": 100})
						_ = c.rpc.call(ctx, appserver.MethodPluginClientRequest, appserver.PluginClientRequestParams{ID: plugin.SubjectID, Fingerprint: plugin.Fingerprint, GraphGeneration: r.core.ExtensionSnapshot().GenerationID, Method: "agents.channel_history", Input: raw}, &history)
					}
				}
			}
			t.Fatalf("Personal reply did not return; model calls=%d status=%+v history=%s", modelCalls.Load(), status, history.Result)
		}
	}
	event := personalim.Incoming{Platform: "slack", TenantID: "T1", SenderID: "U1", ConversationID: "D1", ThreadID: "100.1", MessageID: "100.1", EventID: "Ev1", Text: "SDK first"}
	inject(first, event)
	reply(first, "SDK_REPLY first")
	replay := event
	replay.EventID = "Ev1-retry"
	inject(first, replay)
	secondEvent := event
	secondEvent.MessageID = "100.2"
	secondEvent.EventID = "Ev2"
	secondEvent.Text = "SDK second"
	inject(first, secondEvent)
	reply(first, "SDK_REPLY second")
	if modelCalls.Load() != 2 {
		t.Fatalf("duplicate source reran the model: %d calls", modelCalls.Load())
	}
	if err = c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.stopped:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second := newMessagingE2EDriver()
	r = start(second)
	// No client or renderer has connected to this new host yet.
	select {
	case <-second.started:
	case <-ctx.Done():
		t.Fatal("enabled transport did not recover at cold startup")
	}
	c, call = connect(r)
	if err = call("im.disable", map[string]string{"platform": "slack"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.stopped:
	case <-ctx.Done():
		t.Fatal("disable did not close transport")
	}
}
